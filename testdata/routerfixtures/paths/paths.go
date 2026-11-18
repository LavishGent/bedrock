// Package paths holds the router matrix v1 normalization fixtures
// (docs/superpowers/specs/routers/README.md, section 5). Each case records
// the registration, the normalized method and path (or diagnostic) the
// analyzer must emit, and requests whose outcome against the real router
// pins the behavior the normalization rule relies on. paths_test.go checks
// the requests against chi v5.5.0 and go1.26.11 net/http.
package paths

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// Probe is one request and the status the real router returns.
type Probe struct {
	Method, Path string
	Status       int
}

// Case is one registration fixture.
type Case struct {
	Name       string
	Code       string // the registration as written
	Normalized string // "METHOD path" the analyzer emits, or "diag:<kind>"
	Build      func() http.Handler
	Probes     []Probe
	Panics     bool // the real router rejects the registration
}

func chiR(f func(r chi.Router)) func() http.Handler {
	return func() http.Handler { r := chi.NewRouter(); f(r); return r }
}

func mux(pattern string) func() http.Handler {
	return func() http.Handler { m := http.NewServeMux(); m.HandleFunc(pattern, ok); return m }
}

// Cases are the accepted and rejected normalization fixtures.
var Cases = []Case{
	// chi: /api and /api/ are different routes.
	{"chi-no-slash", `r.Get("/api", h)`, "GET /api", chiR(func(r chi.Router) { r.Get("/api", ok) }),
		[]Probe{{"GET", "/api", 200}, {"GET", "/api/", 404}}, false},
	{"chi-slash", `r.Get("/api/", h)`, "GET /api/", chiR(func(r chi.Router) { r.Get("/api/", ok) }),
		[]Probe{{"GET", "/api/", 200}, {"GET", "/api", 404}}, false},
	// chi Route root: path is prefix+"/" = "/api/"; chi also serves the bare
	// prefix through its mount stub, which the spec records, not normalizes away.
	{"chi-route-root", `r.Route("/api", func(s chi.Router){ s.Get("/", h) })`, "GET /api/",
		chiR(func(r chi.Router) { r.Route("/api", func(s chi.Router) { s.Get("/", ok) }) }),
		[]Probe{{"GET", "/api/", 200}, {"GET", "/api", 200}}, false},
	{"chi-route-trailing-prefix", `r.Route("/api/", func(s chi.Router){ s.Get("/x", h) })`, "GET /api/x",
		chiR(func(r chi.Router) { r.Route("/api/", func(s chi.Router) { s.Get("/x", ok) }) }),
		[]Probe{{"GET", "/api/x", 200}, {"GET", "/api//x", 404}}, false},
	{"chi-route-slash-child", `r.Route("/api", func(s chi.Router){ s.Get("/x/", h) })`, "GET /api/x/",
		chiR(func(r chi.Router) { r.Route("/api", func(s chi.Router) { s.Get("/x/", ok) }) }),
		[]Probe{{"GET", "/api/x/", 200}, {"GET", "/api/x", 404}}, false},
	{"chi-param-regexp", `r.Get("/items/{id:[0-9]+}", h)`, "GET /items/{id}",
		chiR(func(r chi.Router) { r.Get("/items/{id:[0-9]+}", ok) }),
		[]Probe{{"GET", "/items/7", 200}, {"GET", "/items/x", 404}}, false},
	{"chi-method-lower", `r.Method("patch", "/x", h)`, "PATCH /x",
		chiR(func(r chi.Router) { r.Method("patch", "/x", http.HandlerFunc(ok)) }),
		[]Probe{{"PATCH", "/x", 200}}, false},
	{"chi-method-unknown", `r.Method("FOO", "/x", h)`, "diag:unsupported_router_form",
		chiR(func(r chi.Router) { r.Method("FOO", "/x", http.HandlerFunc(ok)) }), nil, true},
	{"chi-no-leading-slash", `r.Get("x", h)`, "diag:unsupported_router_form",
		chiR(func(r chi.Router) { r.Get("x", ok) }), nil, true},
	{"chi-wildcard-not-last", `r.Get("/a/*/b", h)`, "diag:unsupported_router_form",
		chiR(func(r chi.Router) { r.Get("/a/*/b", ok) }), nil, true},
	{"chi-double-slash", `r.Get("//x", h)`, "diag:unsupported_router_form (bounded: chi accepts it)",
		chiR(func(r chi.Router) { r.Get("//x", ok) }), nil, false},

	// net/http: the method token is case-sensitive and kept as written.
	{"nethttp-upper", `mux.HandleFunc("GET /x", h)`, "GET /x", mux("GET /x"),
		[]Probe{{"GET", "/x", 200}, {"HEAD", "/x", 200}, {"get", "/x", 405}}, false},
	{"nethttp-lower", `mux.HandleFunc("get /x", h)`, "get /x", mux("get /x"),
		[]Probe{{"get", "/x", 200}, {"GET", "/x", 405}}, false},
	{"nethttp-any", `mux.HandleFunc("/x", h)`, "* /x", mux("/x"),
		[]Probe{{"GET", "/x", 200}, {"POST", "/x", 200}}, false},
	{"nethttp-spaces", "mux.HandleFunc(\"GET \\t /x\", h)", "GET /x", mux("GET \t /x"),
		[]Probe{{"GET", "/x", 200}}, false},
	{"nethttp-subtree", `mux.HandleFunc("/files/", h)`, "* /files/", mux("/files/"),
		[]Probe{{"GET", "/files/a/b", 200}}, false},
	{"nethttp-exact", `mux.HandleFunc("/files/{$}", h)`, "* /files/{$}", mux("/files/{$}"),
		[]Probe{{"GET", "/files/", 200}, {"GET", "/files/a", 404}}, false},
	{"nethttp-token-not-letters", `mux.HandleFunc("M-SEARCH /x", h)`, "diag:unsupported_router_form (bounded: Go accepts it)",
		mux("M-SEARCH /x"), []Probe{{"M-SEARCH", "/x", 200}}, false},
	{"nethttp-star-method", `mux.HandleFunc("* /x", h)`, "diag:unsupported_router_form (bounded: Go accepts it)",
		mux("* /x"), []Probe{{"*", "/x", 200}}, false},
	{"nethttp-host", `mux.HandleFunc("example.com/x", h)`, "diag:unsupported_router_form (bounded: Go accepts it)",
		mux("example.com/x"), nil, false},
	{"nethttp-dotdot", `mux.HandleFunc("/a/../b", h)`, "diag:unsupported_router_form (bounded: Go accepts it)",
		mux("/a/../b"), nil, false},
	{"nethttp-invalid-wildcard", `mux.HandleFunc("/x/{a", h)`, "diag:unsupported_router_form", mux("/x/{a"), nil, true},
	{"nethttp-dollar-not-last", `mux.HandleFunc("/{$}/x", h)`, "diag:unsupported_router_form", mux("/{$}/x"), nil, true},
	{"nethttp-no-slash", `mux.HandleFunc("x", h)`, "diag:unsupported_router_form", mux("x"), nil, true},
}
