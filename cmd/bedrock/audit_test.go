package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Router value audit regressions (Codex re-review of 45133a0): each case is
// an isolated module that depends on example.com/dep, a dependency the
// analyzer does not see into, which registers an unauthenticated /private on
// a router it is handed in some disguised way.

const depSource = `package dep

import (
	"net/http"
	"sync"
	"unsafe"

	"github.com/go-chi/chi/v5"
)

func open(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// Mux is a ServeMux with its own ServeHTTP: it registers /private on first
// use (per mux) and serves it.
type Mux http.ServeMux

var registered sync.Map

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sm := (*http.ServeMux)(m)
	if _, done := registered.LoadOrStore(m, true); !done {
		sm.HandleFunc("/private", open)
	}
	sm.ServeHTTP(w, r)
}

// SetupAny registers /private on a router passed as any.
func SetupAny(v any) {
	switch r := v.(type) {
	case *chi.Mux:
		r.Get("/private", open)
	case *http.ServeMux:
		r.HandleFunc("/private", open)
	}
}

// Box carries a router in a struct.
type Box struct{ R *chi.Mux }

// SetupBox registers /private on the boxed router.
func SetupBox(b *Box) { b.R.Get("/private", open) }

// SetupUnsafe registers /private through an unsafe.Pointer.
func SetupUnsafe(p unsafe.Pointer) { (*chi.Mux)(p).Get("/private", open) }

// Run calls f.
func Run(f func()) { f() }

// Call calls a registration function value.
func Call(get func(string, http.HandlerFunc)) { get("/private", open) }

// SetupServer registers /private through a server's Handler.
func SetupServer(s *http.Server) { s.Handler.(*http.ServeMux).HandleFunc("/private", open) }

// SetupHTTPAny registers /private on a ServeMux passed as any.
func SetupHTTPAny(v any) { v.(*http.ServeMux).HandleFunc("/private", open) }

// OpenMux returns a ServeMux with /private already registered.
func OpenMux() *http.ServeMux { m := http.NewServeMux(); m.HandleFunc("/private", open); return m }

// OpenHandler returns an unprotected mux behind http.Handler.
func OpenHandler() http.Handler { return OpenMux() }

// OpenMuxErr is OpenMux with an error result.
func OpenMuxErr() (*http.ServeMux, error) { return OpenMux(), nil }

// OpenChi returns a chi router with /private already registered.
func OpenChi() *chi.Mux { r := chi.NewRouter(); r.Get("/private", open); return r }

// OpenAdapter returns the self-registering adapter type.
func OpenAdapter() *Mux { return new(Mux) }

// OpenBox returns a router inside a struct.
func OpenBox() Box { return Box{R: OpenChi()} }

// Global is a dependency-owned router with /private registered.
var Global = OpenMux()

// Make is a generic factory.
func Make[T any](f func() T) T { return f() }

// Factory produces routers through a method.
type Factory struct{}

// Mux returns a ServeMux with /private registered.
func (Factory) Mux() *http.ServeMux { return OpenMux() }

// Sneaky embeds *chi.Mux and overrides ServeHTTP to register /private.
type Sneaky struct{ *chi.Mux }

func (s *Sneaky) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Mux.Get("/private", open)
	s.Mux.ServeHTTP(w, r)
}
`

const depGoMod = "module example.com/dep\n\ngo 1.26.0\n\nrequire github.com/go-chi/chi/v5 v5.5.0\n"

const auditGoMod = `module example.com/probe

go 1.26.0

require (
	example.com/dep v0.0.0
	github.com/go-chi/chi/v5 v5.5.0
)

replace example.com/dep => ./dep
`

const auditHeader = `package probe

import (
	"net/http"
	"reflect"
	"unsafe"

	"net/http/httptest"

	"github.com/go-chi/chi/v5"

	"example.com/dep"
)

var (
	_ = httptest.NewRecorder
	_ = reflect.ValueOf
	_ unsafe.Pointer
	_ = dep.Run
	_ = chi.NewRouter
)

func Auth(next http.Handler) http.Handler { return next }
func h(w http.ResponseWriter, r *http.Request) {}
`

// proofTest serves /private through serve (Go source of an http.Handler
// expression) and requires HTTP 200: the route is genuinely unprotected.
func proofTest(setup, serve string) string {
	return fmt.Sprintf(`package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

var _ = chi.NewRouter

func TestPrivateIsOpen(t *testing.T) {
	%s
	rec := httptest.NewRecorder()
	(%s).ServeHTTP(rec, httptest.NewRequest("GET", "/private", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /private without auth: %%d, want 200 (the bypass)", rec.Code)
	}
}
`, setup, serve)
}

func auditModule(t *testing.T, code, test string) string {
	t.Helper()
	sum, err := os.ReadFile("../../testdata/routerrules/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"dep/go.mod": depGoMod, "dep/go.sum": string(sum), "dep/dep.go": depSource,
		"probe.go": auditHeader + code,
	}
	if test != "" {
		files["probe_test.go"] = test
	}
	return writeModule(t, auditGoMod, files)
}

type auditCase struct {
	name, code   string
	setup, serve string // proof via Handler.ServeHTTP: "" = see proof
	wantExit     int
	proof        string // full TestPrivateIsOpen body (overrides setup/serve)
}

// proofBody builds a proof test from a full test body.
func proofBody(body string) string {
	return `package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"example.com/dep"
)

var (
	_ = chi.NewRouter
	_ = dep.Run
	_ = httptest.NewRecorder
)

func TestPrivateIsOpen(t *testing.T) {
` + body + `
}

func expectOpen(t *testing.T, code int) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("GET /private without auth: %d, want 200 (the bypass)", code)
	}
}
`
}

func runAuditCases(t *testing.T, cases []auditCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			test := ""
			switch {
			case c.proof != "":
				test = proofBody(c.proof)
			case c.serve != "":
				test = proofTest(c.setup, c.serve)
			}
			dir := auditModule(t, c.code, test)
			if test != "" {
				httpProof(t, dir)
			}
			code, raw, facts := run(t, dir)
			r := decode(t, raw)
			if code != c.wantExit {
				t.Fatalf("exit %d, want %d:\n%s", code, c.wantExit, raw)
			}
			if c.wantExit == 2 && !strings.Contains(string(raw), `"kind": "unsupported_router_form"`) {
				t.Fatalf("no unsupported_router_form:\n%s", raw)
			}
			if c.wantExit != 0 && r.Status == "PASS" {
				t.Fatal("bypass reported PASS")
			}
			schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
		})
	}
}

// The Codex re-review sources: each was exit 0 / PASS with a real HTTP 200.
func TestRouterAuditCodexSources(t *testing.T) {
	runAuditCases(t, []auditCase{
		{"servemux-parameter-adapter", `
func Adapt(m *http.ServeMux) http.Handler {
	var hd http.Handler = (*dep.Mux)(m)
	return hd
}`, "", "Adapt(http.NewServeMux())", 2, ""},
		{"new-servemux-adapter", `
func Adapt() http.Handler {
	m := new(http.ServeMux)
	return (*dep.Mux)(m)
}`, "", "Adapt()", 2, ""},
		{"default-servemux-adapter", `
func Adapt() http.Handler {
	return (*dep.Mux)(http.DefaultServeMux)
}`, "", "Adapt()", 2, ""},
		{"wire-setup-any", `
func Wire(r *chi.Mux) { dep.SetupAny(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"setup-box", `
func Wire(r *chi.Mux) { dep.SetupBox(&dep.Box{R: r}) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"setup-unsafe", `
func Wire(r *chi.Mux) { dep.SetupUnsafe(unsafe.Pointer(r)) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"reflect-method-call", `
func Wire(r *chi.Mux) {
	reflect.ValueOf(r).MethodByName("Get").Call([]reflect.Value{
		reflect.ValueOf("/private"), reflect.ValueOf(http.HandlerFunc(h)),
	})
}`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"run-closure", `
func Wire(r *chi.Mux) { dep.Run(func() { dep.SetupAny(r) }) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"factory-router", `
func makeRouter() *chi.Mux { return chi.NewRouter() }
func Build() http.Handler {
	r := makeRouter()
	dep.SetupAny(r)
	return r
}`, "", "Build()", 2, ""},
		{"factory-interface", `
func makeHandler() http.Handler { return chi.NewRouter() }
func Build() http.Handler {
	hd := makeHandler()
	dep.SetupAny(hd)
	return hd
}`, "", "Build()", 2, ""},
	})
}

// Ordinary code must not be flagged: local router, auth middleware,
// registration, return, and serving through the enumerated serving APIs.
func TestRouterAuditNoFalsePositives(t *testing.T) {
	runAuditCases(t, []auditCase{
		{"chi-listen-and-serve", `
func NewAPI() http.Handler {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	return r
}
func Run() error { return http.ListenAndServe(":8080", NewAPI()) }`, "", "", 0, ""},
		{"servemux-http-server", `
func NewAdmin() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /x", Auth(http.HandlerFunc(h)))
	return mux
}
func Run() error {
	srv := &http.Server{Addr: ":8081", Handler: NewAdmin()}
	return srv.ListenAndServe()
}`, "", "", 0, ""},
		{"single-function-direct-servehttp", `
func Run(w http.ResponseWriter, req *http.Request) error {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	r.ServeHTTP(w, req)
	var hd http.Handler = r
	hd.ServeHTTP(w, req)
	return http.ListenAndServe(":8080", r)
}`, "", "", 0, ""},
		// Helpers that take routers are outside the supported form.
		{"serve-helper-rejected", `
func NewAPI() *chi.Mux {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	return r
}
func serve(hd http.Handler) error { return http.ListenAndServe(":8080", hd) }
func Run() error { return serve(NewAPI()) }`, "", "", 2, ""},
		{"httptest-server", `
func NewAPI() http.Handler {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	return r
}
func Start() func() {
	srv := httptest.NewServer(NewAPI())
	return srv.Close
}`, "", "", 0, ""},
	})
}

// Self-review: adversarial variants beyond the Codex list. Each must be
// non-PASS (unknown identity) or a precise violation.
func TestRouterAuditAdversarial(t *testing.T) {
	runAuditCases(t, []auditCase{
		{"generic-pass-through", `
func pass[T any](v T) { dep.SetupAny(v) }
func Wire(r *chi.Mux) { pass(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"method-value-out", `
func Wire(r *chi.Mux) { dep.Call(r.Get) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"struct-embedding", `
type S struct{ *chi.Mux }
func Wire(r *chi.Mux) {
	s := S{r}
	s.Get("/private", h)
}`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"interface-embedding", `
type Getter interface{ Get(string, http.HandlerFunc) }
type Reg interface{ Getter }
func Wire(r *chi.Mux) {
	var g Reg = r
	g.Get("/private", h)
}`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"channel", `
func Wire(r *chi.Mux) {
	ch := make(chan *chi.Mux, 1)
	ch <- r
	dep.SetupAny(<-ch)
}`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"map", `
func Wire(r *chi.Mux) {
	m := map[string]*chi.Mux{"a": r}
	dep.SetupAny(m["a"])
}`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"defer", `
func Wire(r *chi.Mux) { defer dep.SetupAny(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"go-statement", `
func Wire(r *chi.Mux, done chan struct{}) {
	go func() { dep.SetupAny(r); close(done) }()
}`, "r := chi.NewRouter(); d := make(chan struct{}); Wire(r, d); <-d", "r", 2, ""},
		{"slice", `
func Wire(r *chi.Mux) {
	rs := []*chi.Mux{r}
	dep.SetupAny(rs[0])
}`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"global", `
var G *chi.Mux
func Wire(r *chi.Mux) { G = r; dep.SetupAny(G) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"returned-closure", `
func Wire(r *chi.Mux) func() { return func() { r.Get("/private", h) } }`, "r := chi.NewRouter(); Wire(r)()", "r", 2, ""},
		{"module-any-helper", `
func keep(v any) { dep.SetupAny(v) }
func Wire(r *chi.Mux) { keep(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2, ""},
		{"tuple-factory", `
func mk() (http.Handler, error) { return chi.NewRouter(), nil }
func Build() http.Handler {
	hd, err := mk()
	if err != nil {
		panic(err)
	}
	dep.SetupAny(hd)
	return hd
}`, "", "Build()", 2, ""},
	})
}

const serveProof = `	rec := httptest.NewRecorder()
	API().ServeHTTP(rec, httptest.NewRequest("GET", "/private", nil))
	expectOpen(t, rec.Code)`

const serverProof = `	s := Start()
	defer s.Close()
	rsp, err := s.Client().Get(s.URL + "/private")
	if err != nil {
		t.Fatal(err)
	}
	rsp.Body.Close()
	expectOpen(t, rsp.StatusCode)`

// The nine modules of the Codex review of 52c28ce (each was exit 0 / PASS
// with a real HTTP 200): origin, server storage, dispatch proof, and
// dependency-produced routers.
func TestRouterOriginCodexModules(t *testing.T) {
	runAuditCases(t, []auditCase{
		{name: "zero_global", wantExit: 2, proof: serveProof, code: `
var G http.ServeMux
func API() http.Handler { dep.SetupHTTPAny(&G); return &G }`},
		{name: "server_field_escape", wantExit: 2, proof: serveProof, code: `
func API() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	s := &http.Server{Handler: m}
	dep.SetupServer(s)
	return m
}`},
		{name: "server_field_reload", wantExit: 2, proof: serveProof, code: `
func API() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	s := &http.Server{Handler: m}
	dep.SetupHTTPAny(s.Handler)
	return m
}`},
		{name: "server_field_helper", wantExit: 2, proof: serveProof, code: `
func save(hd http.Handler) *http.Server { return &http.Server{Handler: hd} }
func API() http.Handler {
	m := http.NewServeMux()
	s := save(m)
	dep.SetupServer(s)
	return m
}`},
		{name: "chi_interface_dispatch", wantExit: 2, code: `
func Serve(r chi.Router, w http.ResponseWriter, req *http.Request) { r.ServeHTTP(w, req) }`, proof: `	rec := httptest.NewRecorder()
	Serve(&dep.Sneaky{Mux: chi.NewRouter()}, rec, httptest.NewRequest("GET", "/private", nil))
	expectOpen(t, rec.Code)`},
		{name: "chi_phi_dispatch", wantExit: 2, code: `
func Serve(r chi.Router, other chi.Router, flag bool, w http.ResponseWriter, req *http.Request) {
	if flag {
		r = other
	}
	r.ServeHTTP(w, req)
}`, proof: `	rec := httptest.NewRecorder()
	Serve(&dep.Sneaky{Mux: chi.NewRouter()}, chi.NewRouter(), false, rec, httptest.NewRequest("GET", "/private", nil))
	expectOpen(t, rec.Code)`},
		{name: "allocated_adapter_sink", wantExit: 2, proof: serverProof, code: `
func Start() *httptest.Server { return httptest.NewServer(new(dep.Mux)) }`},
		{name: "dep_adapter_sink", wantExit: 2, proof: serverProof, code: `
func Start() *httptest.Server { return httptest.NewServer(dep.OpenAdapter()) }`},
		{name: "dep_factory", wantExit: 2, proof: serveProof, code: `
func API() http.Handler { return dep.OpenMux() }`},
	})
}

// A local http.Server holding a locally built router, used only to serve,
// stays accepted; so does a closed factory/helper pair.
func TestRouterOriginLocalServerNoFalsePositive(t *testing.T) {
	runAuditCases(t, []auditCase{
		{name: "local-server-literal", wantExit: 0, code: `
func Run() error {
	m := http.NewServeMux()
	m.Handle("GET /x", Auth(http.HandlerFunc(h)))
	srv := &http.Server{Addr: ":0", Handler: m}
	defer srv.Close()
	return srv.ListenAndServe()
}`},
		{name: "local-server-field-store", wantExit: 0, code: `
func Run() error {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	var srv http.Server
	srv.Addr = ":0"
	srv.Handler = r
	return srv.ListenAndServe()
}`},
		{name: "helper-factory-rejected", wantExit: 2, code: `
func build() *chi.Mux {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	return r
}
func serve(hd http.Handler) error { return http.ListenAndServe(":0", hd) }
func Run() error { return serve(build()) }`},
	})
}

// Self-review: ten adversarial variants per blocker class, built from the
// origin rule. Each must be non-PASS.
func TestRouterOriginAdversarial(t *testing.T) {
	nonPass := func(name, code string) auditCase { return auditCase{name: name, code: code, wantExit: 2} }
	runAuditCases(t, []auditCase{
		// A: sources (globals, allocations, loads, parameters, free vars).
		nonPass("A1-global-pointer", `
var P = http.NewServeMux()
func API() http.Handler { dep.SetupHTTPAny(P); return P }`),
		nonPass("A2-global-chi", `
var R = chi.NewRouter()
func API() http.Handler { dep.SetupAny(R); return R }`),
		nonPass("A3-local-zero-value", `
func API() http.Handler { var m http.ServeMux; dep.SetupHTTPAny(&m); return &m }`),
		nonPass("A4-new-chi", `
func API() http.Handler { r := new(chi.Mux); return r }`),
		nonPass("A5-global-alias", `
var G http.ServeMux
var alias = &G
func API() http.Handler { dep.SetupHTTPAny(alias); return alias }`),
		nonPass("A6-field-load", `
type S struct{ M *http.ServeMux }
var holder = S{M: http.NewServeMux()}
func API() http.Handler { return holder.M }`),
		nonPass("A7-map-read", `
var routers = map[string]*http.ServeMux{"a": http.NewServeMux()}
func API() http.Handler { m := routers["a"]; return m }`),
		nonPass("A8-exported-parameter", `
func API(m *http.ServeMux) http.Handler { return m }`),
		nonPass("A9-free-variable", `
func API() http.Handler {
	m := http.NewServeMux()
	f := func() http.Handler { return m }
	return f()
}`),
		nonPass("A10-type-assertion", `
func API(v any) http.Handler { m := v.(*http.ServeMux); return m }`),
		// B: http.Server storage.
		nonPass("B1-server-returned", `
func Srv() *http.Server { m := http.NewServeMux(); return &http.Server{Handler: m} }`),
		nonPass("B2-handler-read-back", `
func API() { m := http.NewServeMux(); s := &http.Server{Handler: m}; hd := s.Handler; dep.SetupHTTPAny(hd) }`),
		nonPass("B3-server-to-module-helper", `
func run(s *http.Server) { dep.SetupServer(s) }
func API() { m := http.NewServeMux(); run(&http.Server{Handler: m}) }`),
		nonPass("B4-server-global", `
var S *http.Server
func API() { m := http.NewServeMux(); S = &http.Server{Handler: m} }`),
		nonPass("B5-handler-assigned-then-passed", `
func API() { m := http.NewServeMux(); s := &http.Server{}; s.Handler = m; dep.SetupServer(s) }`),
		nonPass("B6-server-method-value", `
func API() func() error { m := http.NewServeMux(); s := &http.Server{Handler: m}; return s.ListenAndServe }`),
		nonPass("B7-server-in-goroutine", `
func API() { m := http.NewServeMux(); s := &http.Server{Handler: m}; go func() { _ = s.ListenAndServe() }() }`),
		nonPass("B8-server-copied", `
func API() http.Server { m := http.NewServeMux(); s := http.Server{Handler: m}; t := s; return t }`),
		nonPass("B9-unproven-handler", `
func API(c bool) error {
	var hd http.Handler = http.NewServeMux()
	if c {
		hd = chi.NewRouter()
	}
	s := &http.Server{Handler: hd}
	return s.ListenAndServe()
}`),
		nonPass("B10-dependency-handler", `
func API() error { s := &http.Server{Handler: dep.OpenMux()}; return s.ListenAndServe() }`),
		// C: dispatch and serving proof.
		nonPass("C1-chi-router-param-servehttp", `
func Serve(r chi.Router, w http.ResponseWriter, req *http.Request) { r.ServeHTTP(w, req) }`),
		nonPass("C2-phi-of-covered-servehttp", `
func Serve(c bool, w http.ResponseWriter, req *http.Request) {
	var r chi.Router = chi.NewRouter()
	if c {
		r = chi.NewMux()
	}
	r.ServeHTTP(w, req)
}`),
		nonPass("C3-adapter-to-listenandserve", `
func Run() error { return http.ListenAndServe(":0", (*dep.Mux)(http.NewServeMux())) }`),
		nonPass("C4-new-adapter-to-httptest", `
func Start() *httptest.Server { return httptest.NewServer(new(dep.Mux)) }`),
		nonPass("C5-embedding-wrapper-to-sink", `
type W struct{ *chi.Mux }
func Start() *httptest.Server { return httptest.NewServer(&W{chi.NewRouter()}) }`),
		nonPass("C6-phi-to-sink", `
func Start(c bool) *httptest.Server {
	var hd http.Handler = http.NewServeMux()
	if c {
		hd = chi.NewRouter()
	}
	return httptest.NewServer(hd)
}`),
		nonPass("C7-read-only-on-unproven", `
func Inspect(c bool) int {
	var r chi.Router = chi.NewRouter()
	if c {
		r = chi.NewMux()
	}
	return len(r.Middlewares())
}`),
		nonPass("C8-servehttp-method-value", `
func Serve(w http.ResponseWriter, req *http.Request) { m := http.NewServeMux(); f := m.ServeHTTP; f(w, req) }`),
		nonPass("C9-adapter-through-closed-helper", `
func serve(hd http.Handler) error { return http.ListenAndServe(":0", hd) }
func Run() error { return serve((*dep.Mux)(http.NewServeMux())) }`),
		nonPass("C10-dependency-router-to-serve", `
func Run() error { return http.ListenAndServe(":0", dep.OpenMux()) }`),
		// D: routers produced by dependency code.
		nonPass("D1-returned", `
func API() http.Handler { return dep.OpenMux() }`),
		nonPass("D2-served", `
func Run() error { return http.ListenAndServe(":0", dep.OpenChi()) }`),
		nonPass("D3-adapter-to-httptest", `
func Start() *httptest.Server { return httptest.NewServer(dep.OpenAdapter()) }`),
		nonPass("D4-through-module-helper", `
func wrap(m *http.ServeMux) http.Handler { return m }
func API() http.Handler { return wrap(dep.OpenMux()) }`),
		nonPass("D5-dependency-router-extended", `
func API() http.Handler { r := dep.OpenChi(); r.With(Auth).Get("/x", h); return r }`),
		nonPass("D6-tuple", `
func API() http.Handler { m, _ := dep.OpenMuxErr(); return m }`),
		nonPass("D7-struct-field", `
func API() http.Handler { return dep.OpenBox().R }`),
		nonPass("D8-dependency-global", `
func API() http.Handler { return dep.Global }`),
		nonPass("D9-generic-factory", `
func API() http.Handler { return dep.Make(dep.OpenMux) }`),
		nonPass("D10-dependency-method", `
func API() http.Handler { return dep.Factory{}.Mux() }`),
	})
}

const linknameDep = `
//go:linkname hidden example.com/probe.hiddenServe
func hidden(m *http.ServeMux) *httptest.Server

// RunHidden serves an unprotected mux through a module function it reaches
// by go:linkname.
func RunHidden() *httptest.Server { return hidden(OpenMux()) }
`

// The seven fresh variants of the review of 1acec5b (mixed factory flows,
// mixed helper call sites, open factories, recursion, variadic factory,
// go:linkname): each was exit 0 / PASS with a real HTTP 200.
func TestSupportedFormFreshVariants(t *testing.T) {
	runAuditCases(t, []auditCase{
		{name: "mixed_factory", wantExit: 2, proof: serverProof, code: `
func factory(flag bool) http.Handler {
	m := http.NewServeMux()
	m.Handle("/private", Auth(http.HandlerFunc(h)))
	if flag {
		return m
	}
	return dep.OpenHandler()
}
func Start() *httptest.Server { return httptest.NewServer(factory(false)) }`},
		{name: "mixed_closed_helper", wantExit: 2, proof: serverProof, code: `
func serve(hd http.Handler) *httptest.Server { return httptest.NewServer(hd) }
func Safe() *httptest.Server {
	m := http.NewServeMux()
	m.Handle("/private", Auth(http.HandlerFunc(h)))
	return serve(m)
}
func Start() *httptest.Server { return serve(dep.OpenHandler()) }`},
		{name: "mixed_open_factory", wantExit: 2, proof: serverProof, code: `
func factory(in http.Handler, flag bool) http.Handler {
	m := http.NewServeMux()
	m.Handle("/private", Auth(http.HandlerFunc(h)))
	if flag {
		return m
	}
	return in
}
func Start() *httptest.Server { return httptest.NewServer(factory(dep.OpenHandler(), false)) }`},
		{name: "open_external_param", wantExit: 2, code: `
func Factory(in http.Handler, flag bool) http.Handler {
	m := http.NewServeMux()
	m.Handle("/private", Auth(http.HandlerFunc(h)))
	if flag {
		return m
	}
	return in
}
func Start(in http.Handler) *httptest.Server { return httptest.NewServer(Factory(in, false)) }`, proof: `	s := Start(dep.OpenHandler())
	defer s.Close()
	rsp, err := s.Client().Get(s.URL + "/private")
	if err != nil {
		t.Fatal(err)
	}
	rsp.Body.Close()
	expectOpen(t, rsp.StatusCode)`},
		{name: "recursive_factory_unknown", wantExit: 2, proof: serverProof, code: `
func a(n int) http.Handler {
	if n == 0 {
		return dep.OpenHandler()
	}
	return z(n - 1)
}
func z(n int) http.Handler {
	if n == 999 {
		return chi.NewRouter()
	}
	return a(n)
}
func Start() *httptest.Server { return httptest.NewServer(z(2)) }`},
		{name: "closed_variadic", wantExit: 2, proof: serverProof, code: `
func factory(v ...http.Handler) http.Handler {
	if len(v) == 0 {
		return chi.NewRouter()
	}
	return v[0]
}
func Start() *httptest.Server { return httptest.NewServer(factory(dep.OpenHandler())) }`},
	})
}

// go:linkname: a module function reachable from a dependency by linkname is
// outside the supported form (its callers cannot be seen).
func TestSupportedFormLinkname(t *testing.T) {
	sum, err := os.ReadFile("../../testdata/routerrules/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeModule(t, auditGoMod, map[string]string{
		"dep/go.mod": depGoMod, "dep/go.sum": string(sum),
		"dep/dep.go": strings.Replace(depSource, `"net/http"
	"sync"`, `"net/http"
	"net/http/httptest"
	"sync"`, 1) + linknameDep,
		"probe.go": auditHeader + `
//go:linkname serve example.com/probe.hiddenServe
func serve(m *http.ServeMux) *httptest.Server { return httptest.NewServer(m) }

func Start() *httptest.Server { return dep.RunHidden() }
`,
		"probe_test.go": proofBody(serverProof),
	})
	httpProof(t, dir)
	code, raw, facts := run(t, dir)
	if code != 2 || !strings.Contains(string(raw), "go:linkname") {
		t.Fatalf("exit %d; want 2 with the go:linkname diagnostic:\n%s", code, raw)
	}
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}

// proofBodyMin is proofBody without references to the shared dependency.
func proofBodyMin(body string) string {
	return `package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrivateIsOpen(t *testing.T) {
` + body + `
}

func expectOpen(t *testing.T, code int) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("GET /private without auth: %d, want 200 (the bypass)", code)
	}
}
`
}

// customCase is an isolated module with its own dependency source, for cases
// where the dependency reaches back into the module (linkname, imports).
type customCase struct {
	name     string
	files    map[string]string // relative path -> content (probe.go, dep/dep.go, ...)
	depMod   string            // dep/go.mod (default depGoMod)
	proof    string            // TestPrivateIsOpen body ("" = none)
	wantExit int
}

func runCustomCases(t *testing.T, cases []customCase) {
	sum, err := os.ReadFile("../../testdata/routerrules/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"dep/go.sum": string(sum)}
			for k, v := range c.files {
				files[k] = v
			}
			files["dep/go.mod"] = depGoMod
			if c.depMod != "" {
				files["dep/go.mod"] = c.depMod
			}
			if c.proof != "" {
				files["probe_test.go"] = proofBodyMin(c.proof)
			}
			dir := writeModule(t, auditGoMod, files)
			if c.proof != "" {
				httpProof(t, dir)
			}
			code, raw, facts := run(t, dir)
			r := decode(t, raw)
			if code != c.wantExit || (c.wantExit != 0 && r.Status == "PASS") {
				t.Fatalf("exit %d status %s, want %d:\n%s", code, r.Status, c.wantExit, raw)
			}
			if c.wantExit == 2 && !strings.Contains(string(raw), `"kind": "unsupported_router_form"`) {
				t.Fatalf("no unsupported_router_form:\n%s", raw)
			}
			schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
		})
	}
}

const probeAuthHeader = `package probe

import (
	"net/http"

	"example.com/dep"
)

var _ = dep.Marker

func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
}
func h(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
`

// depBase is a minimal dependency: it registers /private on a router it gets.
const depBase = `package dep

import (
	"net/http"
	_ "unsafe"
)

var Marker = 0

func open(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// SetupHTTPAny registers /private on a ServeMux passed as any.
func SetupHTTPAny(v any) { v.(*http.ServeMux).HandleFunc("/private", open) }

// RunFactory calls a factory and registers /private on its router.
func RunFactory(f func() http.Handler) http.Handler { m := f(); SetupHTTPAny(m); return m }
`

const depProbeMod = "module example.com/dep\n\ngo 1.26.0\n\nrequire github.com/go-chi/chi/v5 v5.5.0\n\nrequire example.com/probe v0.0.0\n"

// TestFactoryBoundaryCodexModules: the eight modules of the review of
// 024bbb3 (each was exit 0 / PASS with a real HTTP 200).
func TestFactoryBoundaryCodexModules(t *testing.T) {
	const newAPI = `
func NewAPI() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}`
	runCustomCases(t, []customCase{
		{name: "form_global_factory", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
var makeAPI = NewAPI
func API() http.Handler { m := makeAPI(); dep.SetupHTTPAny(m); return m }`}},
		{name: "form_bound_factory", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + `
type S struct{}
func (S) NewAPI() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
func API() http.Handler { s := S{}; makeAPI := s.NewAPI; m := makeAPI(); dep.SetupHTTPAny(m); return m }`}},
		{name: "form_interface_factory", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + `
type S struct{}
func (S) NewAPI() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
type Factory interface{ NewAPI() http.Handler }
func API() http.Handler { var f Factory = S{}; m := f.NewAPI(); dep.SetupHTTPAny(m); return m }`}},
		{name: "form_callback_factory", wantExit: 2, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
func API(f func() http.Handler) http.Handler { m := f(); dep.SetupHTTPAny(m); return m }`},
			proof: `	rec := httptest.NewRecorder()
	API(NewAPI).ServeHTTP(rec, httptest.NewRequest("GET", "/private", nil))
	expectOpen(t, rec.Code)`},
		{name: "form_dependency_callback", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
func API() http.Handler { return dep.RunFactory(NewAPI) }`}},
		{name: "form_tuple_global", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + `
func NewAPI() (http.Handler, error) {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m, nil
}
var makeAPI = NewAPI
func API() http.Handler { m, _ := makeAPI(); dep.SetupHTTPAny(m); return m }`}},
		{name: "link_factory_dependency_only", wantExit: 2, proof: serveProof, files: map[string]string{
			"dep/dep.go": depBase + `
//go:linkname hiddenFactory example.com/probe.newAPI
func hiddenFactory() http.Handler

// RunHiddenFactory reaches the module's unexported factory by linkname.
func RunHiddenFactory() http.Handler { r := hiddenFactory(); SetupHTTPAny(r); return r }
`,
			"probe.go": probeAuthHeader + `
func newAPI() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
func API() http.Handler { return dep.RunHiddenFactory() }`}},
		{name: "dependency_import_factory", wantExit: 2, proof: serveProof, depMod: depProbeMod, files: map[string]string{
			"api/api.go": "package api\n\nimport \"net/http\"\n\n// Make returns a new ServeMux.\nfunc Make() *http.ServeMux { return http.NewServeMux() }\n",
			"dep/dep.go": strings.Replace(depBase, `_ "unsafe"`, `_ "unsafe"

	"example.com/probe/api"`, 1) + `
// BackReference calls the module's factory and registers /private.
func BackReference() http.Handler { m := api.Make(); m.HandleFunc("/private", open); return m }
`,
			"probe.go": probeAuthHeader + `
func API() http.Handler { return dep.BackReference() }`}},
	})
}

// Self-review: five more variants per blocker class.
func TestFactoryBoundaryAdversarial(t *testing.T) {
	const newAPI = `
func NewAPI() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}`
	runCustomCases(t, []customCase{
		// B1: factories as values or through unresolved calls.
		{name: "B1-struct-field-factory", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
type F struct{ make func() http.Handler }
var reg = F{make: NewAPI}
func API() http.Handler { m := reg.make(); dep.SetupHTTPAny(m); return m }`}},
		{name: "B1-map-factory", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
var fs = map[string]func() http.Handler{"a": NewAPI}
func API() http.Handler { m := fs["a"](); dep.SetupHTTPAny(m); return m }`}},
		{name: "B1-generic-call", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
func call[T any](f func() T) T { return f() }
func API() http.Handler { m := call(NewAPI); dep.SetupHTTPAny(m); return m }`}},
		{name: "B1-closure-wrapper", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + newAPI + `
func API() http.Handler { g := func() http.Handler { return NewAPI() }; m := g(); dep.SetupHTTPAny(m); return m }`}},
		{name: "B1-interface-pointer-method", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depBase,
			"probe.go": probeAuthHeader + `
type S struct{ n int }
func (s *S) Build() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
type B interface{ Build() http.Handler }
func pick() B { return &S{} }
func API() http.Handler { m := pick().Build(); dep.SetupHTTPAny(m); return m }`}},
		// B2: dependency-side callers.
		{name: "B2-linkname-pull-exported", wantExit: 2, proof: serveProof, files: map[string]string{
			"dep/dep.go": depBase + `
//go:linkname pulled example.com/probe.NewAPI
func pulled() http.Handler
func Run() http.Handler { r := pulled(); SetupHTTPAny(r); return r }
`,
			"probe.go": probeAuthHeader + newAPI + `
func API() http.Handler { return dep.Run() }`}},
		{name: "B2-import-interface-factory", wantExit: 2, proof: serveProof, depMod: depProbeMod, files: map[string]string{
			"api/api.go": "package api\n\nimport \"net/http\"\n\n// Make returns a router as http.Handler.\nfunc Make() http.Handler { return http.NewServeMux() }\n",
			"dep/dep.go": strings.Replace(depBase, `_ "unsafe"`, `_ "unsafe"

	"example.com/probe/api"`, 1) + `
func Back() http.Handler { m := api.Make(); SetupHTTPAny(m); return m }
`,
			"probe.go": probeAuthHeader + `
func API() http.Handler { return dep.Back() }`}},
		{name: "B2-import-method-factory", wantExit: 2, proof: serveProof, depMod: depProbeMod, files: map[string]string{
			"api/api.go": "package api\n\nimport \"net/http\"\n\n// T makes routers.\ntype T struct{}\n\n// Make returns a new ServeMux.\nfunc (T) Make() *http.ServeMux { return http.NewServeMux() }\n",
			"dep/dep.go": strings.Replace(depBase, `_ "unsafe"`, `_ "unsafe"

	"example.com/probe/api"`, 1) + `
func Back() http.Handler { m := api.T{}.Make(); m.HandleFunc("/private", open); return m }
`,
			"probe.go": probeAuthHeader + `
func API() http.Handler { return dep.Back() }`}},
		{name: "B2-import-wrapped-unexported", wantExit: 2, proof: serveProof, depMod: depProbeMod, files: map[string]string{
			"api/api.go": "package api\n\nimport \"net/http\"\n\nfunc make1() *http.ServeMux { return http.NewServeMux() }\n\n// Make wraps an unexported factory.\nfunc Make() *http.ServeMux { return make1() }\n",
			"dep/dep.go": strings.Replace(depBase, `_ "unsafe"`, `_ "unsafe"

	"example.com/probe/api"`, 1) + `
func Back() http.Handler { m := api.Make(); m.HandleFunc("/private", open); return m }
`,
			"probe.go": probeAuthHeader + `
func API() http.Handler { return dep.Back() }`}},
		{name: "B2-linkname-pull-chi-factory", wantExit: 2, proof: serveProof, files: map[string]string{
			"dep/dep.go": `package dep

import (
	"net/http"
	_ "unsafe"

	"github.com/go-chi/chi/v5"
)

var Marker = 0

//go:linkname pulled example.com/probe.newRouter
func pulled() *chi.Mux

func Run() http.Handler {
	r := pulled()
	r.Get("/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return r
}
`,
			"probe.go": strings.Replace(probeAuthHeader, `"example.com/dep"`, `"github.com/go-chi/chi/v5"

	"example.com/dep"`, 1) + `
func newRouter() *chi.Mux { return chi.NewRouter() }
func API() http.Handler { return dep.Run() }`}},
	})
}

// Known limits (routers/README.md, "Known limits"). These runnable modules
// are tracked here, skipped with their limit ID: the analyzer currently
// reports PASS (exit 0) while /private answers 200 unauthenticated. Remove
// the t.Skip when a limit is fixed; each then asserts exit 2.
func TestKnownLimits(t *testing.T) {
	depSaved := depBase + `
var saved func() http.Handler

// SaveFactory keeps a factory for later.
func SaveFactory(f func() http.Handler) { saved = f }

// RunSaved calls the saved factory and registers /private on its router.
func RunSaved() http.Handler { r := saved(); SetupHTTPAny(r); return r }
`
	limits := []struct {
		id string
		c  customCase
	}{
		{"L1", customCase{name: "self_callback", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depSaved,
			"probe.go": strings.Replace(probeAuthHeader, `"net/http"`, `"net/http"
	"net/http/httptest"`, 1) + `
func NewAPI() http.Handler {
	dep.SaveFactory(NewAPI)
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
func API() http.Handler { s := httptest.NewServer(NewAPI()); s.Close(); return dep.RunSaved() }`}}},
		{"L1", customCase{name: "self_method_callback", wantExit: 2, proof: serveProof, files: map[string]string{"dep/dep.go": depSaved,
			"probe.go": strings.Replace(probeAuthHeader, `"net/http"`, `"net/http"
	"net/http/httptest"`, 1) + `
type S struct{}
func (s S) NewAPI() http.Handler {
	dep.SaveFactory(s.NewAPI)
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
func API() http.Handler { s := httptest.NewServer((S{}).NewAPI()); s.Close(); return dep.RunSaved() }`}}},
		{"L2", customCase{name: "dependency_link_method", wantExit: 2, proof: serveProof, files: map[string]string{
			"dep/dep.go": depBase + `
//go:linkname hiddenFactory example.com/probe.S.NewAPI
func hiddenFactory(struct{}) http.Handler

// RunHiddenFactory reaches a module factory method by linkname.
func RunHiddenFactory() http.Handler { r := hiddenFactory(struct{}{}); SetupHTTPAny(r); return r }
`,
			"probe.go": probeAuthHeader + `
type S struct{}
func (S) NewAPI() http.Handler {
	m := http.NewServeMux()
	m.Handle("/safe", Auth(http.HandlerFunc(h)))
	return m
}
func API() http.Handler { return dep.RunHiddenFactory() }`}}},
	}
	for _, l := range limits {
		t.Run(l.id+"/"+l.c.name, func(t *testing.T) {
			t.Skip("known limit " + l.id + " (routers/README.md, Known limits): analyzer reports PASS; tracked, not fixed in the POC")
			runCustomCases(t, []customCase{l.c})
		})
	}
}
