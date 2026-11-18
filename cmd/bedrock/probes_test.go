package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Step 3 review probes (Codex). Each probe is a one-file module checked in
// isolation, because unknown registration identity is module-wide by design.
// Every bypass must be non-PASS; S1 and S2 must be accepted and schema-valid.

const probePolicy = `version = 1
module = "example.com/probe"

[boundary.all]
packages = ["example.com/probe/**"]

[concept."auth.middleware"]
symbols = ["example.com/probe.Auth"]

[[rule]]
id = "needs-auth"
kind = "require_middleware"
scope = "all"
middleware = "auth.middleware"
message = "Every route must be authenticated."
remediation.message = "Wrap it with probe.Auth."
`

const probePrelude = `package probe

import (
	"net/http"
	"github.com/go-chi/chi/v5"
)

var _ = chi.NewRouter

func Auth(next http.Handler) http.Handler { return next }
func Open(next http.Handler) http.Handler { return next }
func h(w http.ResponseWriter, r *http.Request) {}
`

func probeModule(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "probe")
	sum, err := os.ReadFile("../../testdata/routerrules/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":               "module example.com/probe\n\ngo 1.26.0\n\nrequire github.com/go-chi/chi/v5 v5.5.0\n",
		"go.sum":               string(sum),
		"probe.go":             probePrelude + body,
		".bedrock/policy.toml": probePolicy,
	}
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReviewProbes(t *testing.T) {
	cases := []struct {
		name string
		body string
		exit int    // expected exit code
		kind string // a diagnostic kind that must be present ("" = none required)
	}{
		// B1: aliases of chi.Router are recognized (the unprotected routes are violations).
		{"B1-alias-route-param", `
type R = chi.Router
func API() http.Handler {
	r := chi.NewRouter()
	r.Route("/a", func(s R) { s.Get("/x", h) })
	return r
}`, 1, ""},
		{"B1-alias-variable", `
type R = chi.Router
func API() http.Handler {
	var r R = chi.NewRouter()
	r.Get("/y", h)
	return r
}`, 1, ""},
		// B1: a registration through an arbitrary interface on a tracked router.
		{"B1-foreign-interface", `
func Admin() http.Handler {
	mux := http.NewServeMux()
	var reg interface{ Handle(string, http.Handler) } = mux
	reg.Handle("/private", http.HandlerFunc(h))
	return mux
}`, 2, "unsupported_router_form"},
		// B2: user-created middleware slices whose elements are overwritten.
		{"B2-overwrite-use", `
func API() http.Handler {
	r := chi.NewRouter()
	ms := []func(http.Handler) http.Handler{Auth}
	ms[0] = Open
	r.Use(ms...)
	r.Get("/x", h)
	return r
}`, 2, "unsupported_middleware_form"},
		{"B2-conditional-overwrite-use", `
func API(c bool) http.Handler {
	r := chi.NewRouter()
	ms := []func(http.Handler) http.Handler{Auth}
	if c {
		ms[0] = Open
	}
	r.Use(ms...)
	r.Get("/x", h)
	return r
}`, 2, "unsupported_middleware_form"},
		{"B2-overwrite-with", `
func API() http.Handler {
	r := chi.NewRouter()
	ms := []func(http.Handler) http.Handler{Auth}
	ms[0] = Open
	r.With(ms...).Get("/x", h)
	return r
}`, 2, "unsupported_middleware_form"},
		// B3: registration functions used as values.
		{"B3-package-function-alias", `
var register = http.HandleFunc
func Setup() { register("/private", h) }`, 2, "unsupported_router_form"},
		// SSA resolves a local alias to a static call: the registration is
		// recognized precisely and its unprotected route is a violation.
		{"B3-local-indirect", `
func Setup() {
	f := http.HandleFunc
	f("/p", h)
}`, 1, ""},
		{"B3-passed-as-argument", `
func call(reg func(string, func(http.ResponseWriter, *http.Request))) { reg("/q", h) }
func Setup() { call(http.HandleFunc) }`, 2, "unsupported_router_form"},
		// A method expression called directly is likewise a static call on r.
		{"B3-method-expression", `
func Setup() http.Handler {
	r := chi.NewRouter()
	get := (*chi.Mux).Get
	get(r, "/z", h)
	return r
}`, 1, ""},
		// A method expression that escapes into a variable used elsewhere is not.
		{"B3-method-expression-escapes", `
var get = (*chi.Mux).Get
func Setup() http.Handler {
	r := chi.NewRouter()
	get(r, "/z", h)
	return r
}`, 2, "unsupported_router_form"},
		// S2: registration in init(): the owning init symbol is emitted.
		// DefaultServeMux (http.Handle) is a global router outside the
		// supported form: protected or not, it is unknown identity.
		{"S2-init-registration", `
func init() {
	http.Handle("/x", Auth(http.HandlerFunc(h)))
}`, 2, "unsupported_router_form"},
		{"S2-init-unprotected", `
func init() {
	http.HandleFunc("/open", h)
}`, 1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := probeModule(t, c.body)
			code, raw, facts := run(t, dir)
			r := decode(t, raw)
			if code != c.exit {
				t.Fatalf("exit %d, want %d:\n%s", code, c.exit, raw)
			}
			if c.kind != "" && !strings.Contains(string(raw), `"kind": "`+c.kind+`"`) {
				t.Fatalf("no %s diagnostic:\n%s", c.kind, raw)
			}
			if c.exit != 0 && r.Status == "PASS" {
				t.Fatal("bypass reported PASS")
			}
			schemacheck(t, runDocs(t, facts, raw)...)
		})
	}
}

// S1: an alias-typed middleware is accepted as a middleware concept symbol.
func TestAliasMiddlewareConcept(t *testing.T) {
	dir := probeModule(t, `
type H = http.Handler
func AuthH(next H) H { return next }
func API() http.Handler {
	r := chi.NewRouter()
	r.Use(AuthH)
	r.Get("/x", h)
	return r
}`)
	pol := strings.Replace(probePolicy, `"example.com/probe.Auth"`, `"example.com/probe.AuthH"`, 1)
	if err := os.WriteFile(filepath.Join(dir, ".bedrock/policy.toml"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	code, raw, _ := run(t, dir)
	if code != 0 {
		t.Fatalf("alias-typed middleware rejected: exit %d\n%s", code, raw)
	}
	// A genuinely wrong signature is still policy_invalid.
	dir = probeModule(t, `
func NotMW(s string) string { return s }
func API() http.Handler { r := chi.NewRouter(); r.Get("/x", h); return r }`)
	pol = strings.Replace(probePolicy, `"example.com/probe.Auth"`, `"example.com/probe.NotMW"`, 1)
	if err := os.WriteFile(filepath.Join(dir, ".bedrock/policy.toml"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	code, raw, _ = run(t, dir)
	if code != 2 || !strings.Contains(string(raw), "policy_invalid") {
		t.Fatalf("wrong signature: exit %d\n%s", code, raw)
	}
}

// S2 committed fixture: routes registered in init#1, init#2 and an
// initializer closure; the shard references only emitted symbols.
func TestRouterInitFixture(t *testing.T) {
	code, raw, facts := run(t, "../../testdata/routerinit")
	r := decode(t, raw)
	if code != 1 || len(r.Violations) != 1 || r.Violations[0].Location.Line != 19 {
		t.Fatalf("exit %d violations %+v; want 1 with only the unprotected /second (line 19)", code, r.Violations)
	}
	sh, err := os.ReadFile(filepath.Join(facts, "shards", "example.com%2Frouterinit.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{`"id": "example.com/routerinit.init#1"`, `"id": "example.com/routerinit.init#2"`, `"id": "example.com/routerinit.init"`} {
		if !strings.Contains(string(sh), id) {
			t.Fatalf("shard lacks symbol %s", id)
		}
	}
	schemacheck(t, runDocs(t, facts, raw)...)
}

// writeModule writes a probe module with the given extra files (paths
// relative to the module root) plus the shared policy and chi go.sum.
func writeModule(t *testing.T, goMod string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "probe")
	sum, err := os.ReadFile("../../testdata/routerrules/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = goMod
	files["go.sum"] = string(sum)
	files[".bedrock/policy.toml"] = probePolicy
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// httpProof runs the probe module's own tests, which issue a real httptest
// request to the unauthenticated route and require HTTP 200: the route is
// genuinely unprotected, so anything but non-PASS would be a silent bypass.
func httpProof(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "test", "-count=1", "-run", "TestPrivateIsOpen", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("probe module's httptest proof failed: %v\n%s", err, out)
	}
}

func requireUnknownIdentity(t *testing.T, dir string) {
	t.Helper()
	code, raw, facts := run(t, dir)
	r := decode(t, raw)
	if code != 2 || r.Status != "INDETERMINATE" || !strings.Contains(string(raw), `"kind": "unsupported_router_form"`) {
		t.Fatalf("exit %d status %s; want 2 with unsupported_router_form:\n%s", code, r.Status, raw)
	}
	schemacheck(t, runDocs(t, facts, raw)...)
}

// Codex recheck B1: a dependency's defined type Mux http.ServeMux with its own
// ServeHTTP registers and serves an unauthenticated /private. Converting the
// module's ServeMux to *adapter.Mux changes the method set: escape, not PASS.
func TestRecheckB1AdapterConversion(t *testing.T) {
	dir := writeModule(t, `module example.com/probe

go 1.26.0

require (
	example.com/adapter v0.0.0
	github.com/go-chi/chi/v5 v5.5.0
)

replace example.com/adapter => ./adapter
`, map[string]string{
		"adapter/go.mod": "module example.com/adapter\n\ngo 1.26.0\n",
		"adapter/adapter.go": `package adapter

import (
	"net/http"
	"sync"
)

// Mux is a ServeMux with its own ServeHTTP that registers an
// unauthenticated route on first use (per mux) and then serves it.
type Mux http.ServeMux

var registered sync.Map

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sm := (*http.ServeMux)(m)
	if _, done := registered.LoadOrStore(m, true); !done {
		sm.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	sm.ServeHTTP(w, r)
}
`,
		"probe.go": probePrelude + `
import_placeholder
`,
		"probe_test.go": `package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrivateIsOpen(t *testing.T) {
	rec := httptest.NewRecorder()
	Serve(rec, httptest.NewRequest("GET", "/private", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /private without auth: %d, want 200 (the bypass)", rec.Code)
	}
}
`,
	})
	// The module code: a recognized ServeMux converted to the adapter type.
	src := strings.Replace(probePrelude, `"github.com/go-chi/chi/v5"`, `"github.com/go-chi/chi/v5"
	"example.com/adapter"`, 1) + `
// Serve converts a recognized ServeMux to the adapter type, assigns it to
// http.Handler and calls ServeHTTP (the Codex repro).
func Serve(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.Handle("GET /safe", Auth(http.HandlerFunc(h)))
	var hd http.Handler = (*adapter.Mux)(mux)
	hd.ServeHTTP(w, r)
}
`
	if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	httpProof(t, dir)
	requireUnknownIdentity(t, dir)
}

// Codex recheck B3: Register(reg func(*chi.Mux, string, http.HandlerFunc), r)
// calls reg(r, "/private", h); an external test caller passes (*chi.Mux).Get.
// A receiver-first dynamic call is registration-shaped: unknown identity.
func TestRecheckB3ReceiverFirstExternalCaller(t *testing.T) {
	dir := writeModule(t, `module example.com/probe

go 1.26.0

require github.com/go-chi/chi/v5 v5.5.0
`, map[string]string{
		"probe.go": probePrelude + `
// Register lets the caller choose how to register /private.
func Register(reg func(*chi.Mux, string, http.HandlerFunc), r *chi.Mux) {
	reg(r, "/private", h)
}
`,
		"probe_ext_test.go": `package probe_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"example.com/probe"
)

func TestPrivateIsOpen(t *testing.T) {
	r := chi.NewRouter()
	probe.Register((*chi.Mux).Get, r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/private", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /private without auth: %d, want 200 (the bypass)", rec.Code)
	}
}
`,
	})
	httpProof(t, dir)
	requireUnknownIdentity(t, dir)
}

// A router-typed parameter handed to code outside the module (not analyzed)
// is unknown identity as well.
func TestRecheckRouterParamToDependency(t *testing.T) {
	dir := writeModule(t, `module example.com/probe

go 1.26.0

require (
	example.com/dep v0.0.0
	github.com/go-chi/chi/v5 v5.5.0
)

replace example.com/dep => ./dep
`, map[string]string{
		"dep/go.mod": "module example.com/dep\n\ngo 1.26.0\n\nrequire github.com/go-chi/chi/v5 v5.5.0\n",
		"dep/go.sum": "",
		"dep/dep.go": `package dep

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Setup registers an unauthenticated route on the caller's router.
func Setup(r *chi.Mux) {
	r.Get("/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}
`,
		"probe.go": strings.Replace(probePrelude, `"github.com/go-chi/chi/v5"`, `"github.com/go-chi/chi/v5"
	"example.com/dep"`, 1) + `
// Wire hands its router parameter to the dependency.
func Wire(r *chi.Mux) { dep.Setup(r) }
`,
		"probe_test.go": `package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestPrivateIsOpen(t *testing.T) {
	r := chi.NewRouter()
	Wire(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/private", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /private without auth: %d, want 200 (the bypass)", rec.Code)
	}
}
`,
	})
	sum, _ := os.ReadFile("../../testdata/routerrules/go.sum")
	if err := os.WriteFile(filepath.Join(dir, "dep", "go.sum"), sum, 0o644); err != nil {
		t.Fatal(err)
	}
	httpProof(t, dir)
	requireUnknownIdentity(t, dir)
}
