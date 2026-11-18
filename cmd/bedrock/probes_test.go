package main

import (
	"os"
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
		// S1: middleware declared with an alias of http.Handler is accepted.
		{"S1-alias-middleware-type", `
type H = http.Handler
func AuthH(next H) H { return next }
func API() http.Handler {
	r := chi.NewRouter()
	r.Use(Auth)
	r.Get("/x", h)
	return r
}`, 0, ""},
		// S2: registration in init(): the owning init symbol is emitted.
		{"S2-init-registration", `
func init() {
	http.Handle("/x", Auth(http.HandlerFunc(h)))
}`, 0, ""},
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
			schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
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
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}
