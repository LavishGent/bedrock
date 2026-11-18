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
	setup, serve string // proof: "" = no runtime proof
	wantExit     int
}

func runAuditCases(t *testing.T, cases []auditCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			test := ""
			if c.serve != "" {
				test = proofTest(c.setup, c.serve)
			}
			dir := auditModule(t, c.code, test)
			if c.serve != "" {
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
}`, "", "Adapt(http.NewServeMux())", 2},
		{"new-servemux-adapter", `
func Adapt() http.Handler {
	m := new(http.ServeMux)
	return (*dep.Mux)(m)
}`, "", "Adapt()", 2},
		{"default-servemux-adapter", `
func Adapt() http.Handler {
	return (*dep.Mux)(http.DefaultServeMux)
}`, "", "Adapt()", 2},
		{"wire-setup-any", `
func Wire(r *chi.Mux) { dep.SetupAny(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"setup-box", `
func Wire(r *chi.Mux) { dep.SetupBox(&dep.Box{R: r}) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"setup-unsafe", `
func Wire(r *chi.Mux) { dep.SetupUnsafe(unsafe.Pointer(r)) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"reflect-method-call", `
func Wire(r *chi.Mux) {
	reflect.ValueOf(r).MethodByName("Get").Call([]reflect.Value{
		reflect.ValueOf("/private"), reflect.ValueOf(http.HandlerFunc(h)),
	})
}`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"run-closure", `
func Wire(r *chi.Mux) { dep.Run(func() { dep.SetupAny(r) }) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"factory-router", `
func makeRouter() *chi.Mux { return chi.NewRouter() }
func Build() http.Handler {
	r := makeRouter()
	dep.SetupAny(r)
	return r
}`, "", "Build()", 2},
		{"factory-interface", `
func makeHandler() http.Handler { return chi.NewRouter() }
func Build() http.Handler {
	hd := makeHandler()
	dep.SetupAny(hd)
	return hd
}`, "", "Build()", 2},
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
func Run() error { return http.ListenAndServe(":8080", NewAPI()) }`, "", "", 0},
		{"servemux-http-server", `
func NewAdmin() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /x", Auth(http.HandlerFunc(h)))
	return mux
}
func Run() error {
	srv := &http.Server{Addr: ":8081", Handler: NewAdmin()}
	return srv.ListenAndServe()
}`, "", "", 0},
		{"serve-helper-and-direct-servehttp", `
func NewAPI() *chi.Mux {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	return r
}
func serve(hd http.Handler) error { return http.ListenAndServe(":8080", hd) }
func Run(w http.ResponseWriter, req *http.Request) error {
	r := NewAPI()
	r.ServeHTTP(w, req)
	var hd http.Handler = r
	hd.ServeHTTP(w, req)
	return serve(r)
}`, "", "", 0},
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
}`, "", "", 0},
	})
}

// Self-review: adversarial variants beyond the Codex list. Each must be
// non-PASS (unknown identity) or a precise violation.
func TestRouterAuditAdversarial(t *testing.T) {
	runAuditCases(t, []auditCase{
		{"generic-pass-through", `
func pass[T any](v T) { dep.SetupAny(v) }
func Wire(r *chi.Mux) { pass(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"method-value-out", `
func Wire(r *chi.Mux) { dep.Call(r.Get) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"struct-embedding", `
type S struct{ *chi.Mux }
func Wire(r *chi.Mux) {
	s := S{r}
	s.Get("/private", h)
}`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"interface-embedding", `
type Getter interface{ Get(string, http.HandlerFunc) }
type Reg interface{ Getter }
func Wire(r *chi.Mux) {
	var g Reg = r
	g.Get("/private", h)
}`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"channel", `
func Wire(r *chi.Mux) {
	ch := make(chan *chi.Mux, 1)
	ch <- r
	dep.SetupAny(<-ch)
}`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"map", `
func Wire(r *chi.Mux) {
	m := map[string]*chi.Mux{"a": r}
	dep.SetupAny(m["a"])
}`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"defer", `
func Wire(r *chi.Mux) { defer dep.SetupAny(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"go-statement", `
func Wire(r *chi.Mux, done chan struct{}) {
	go func() { dep.SetupAny(r); close(done) }()
}`, "r := chi.NewRouter(); d := make(chan struct{}); Wire(r, d); <-d", "r", 2},
		{"slice", `
func Wire(r *chi.Mux) {
	rs := []*chi.Mux{r}
	dep.SetupAny(rs[0])
}`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"global", `
var G *chi.Mux
func Wire(r *chi.Mux) { G = r; dep.SetupAny(G) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"returned-closure", `
func Wire(r *chi.Mux) func() { return func() { r.Get("/private", h) } }`, "r := chi.NewRouter(); Wire(r)()", "r", 2},
		{"module-any-helper", `
func keep(v any) { dep.SetupAny(v) }
func Wire(r *chi.Mux) { keep(r) }`, "r := chi.NewRouter(); Wire(r)", "r", 2},
		{"tuple-factory", `
func mk() (http.Handler, error) { return chi.NewRouter(), nil }
func Build() http.Handler {
	hd, err := mk()
	if err != nil {
		panic(err)
	}
	dep.SetupAny(hd)
	return hd
}`, "", "Build()", 2},
	})
}
