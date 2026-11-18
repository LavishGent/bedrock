// Package conditional holds router matrix v1 rejected forms that need static
// analysis rather than a run-time probe: conditional and looped registration
// or middleware, non-identity receivers, escapes, unresolved paths and
// handlers, and per-mux duplicates. Each line with a "want" comment must get
// that diagnostic from the analyzer (internal/analyze routes_test.go), and no
// route it concerns may count as authenticated.
package conditional

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// M is a direct-reference middleware.
func M(next http.Handler) http.Handler { return next }

func ok(w http.ResponseWriter, r *http.Request) {}

func mk() func(http.Handler) http.Handler { return M }

func makeHandler() http.HandlerFunc { return ok }

// ConditionalUse adds middleware only on one branch.
func ConditionalUse(cond bool) http.Handler {
	r := chi.NewRouter()
	if cond {
		r.Use(M) // want unsupported_middleware_form
	}
	r.Get("/cond-use", ok)
	return r
}

// LoopUse adds middleware in a loop.
func LoopUse(n int) http.Handler {
	r := chi.NewRouter()
	for i := 0; i < n; i++ {
		r.Use(M) // want unsupported_middleware_form
	}
	r.Get("/loop-use", ok)
	return r
}

// ConditionalRoute registers a route only on one branch.
func ConditionalRoute(cond bool) http.Handler {
	r := chi.NewRouter()
	r.Use(M)
	if cond {
		r.Get("/cond-route", ok) // want unsupported_router_form
	}
	return r
}

// LoopRoute registers routes in a loop.
func LoopRoute(paths []string) http.Handler {
	r := chi.NewRouter()
	for _, p := range paths {
		r.Get(p, ok) // want unsupported_router_form
	}
	return r
}

// PhiReceiver registers on a value merged from two routers.
func PhiReceiver(prod bool) http.Handler {
	var r chi.Router
	if prod {
		r = chi.NewRouter()
	} else {
		r = chi.NewMux()
	}
	r.Get("/phi", ok) // want unsupported_router_form
	return r
}

// register is also used as a function value (NamedGroup), so it can be
// called from outside the audited call graph: its router parameter has an
// unknown origin.
func register(r chi.Router) { // want unsupported_router_form
	r.Get("/helper", ok) // want unsupported_router_form
}

// Helper passes a router to a module helper: helpers are outside the
// supported form, so the pass is diagnosed (and so is the registration inside
// register, on a parameter of unknown identity).
func Helper() http.Handler {
	r := chi.NewRouter()
	register(r) // want unsupported_router_form
	return r
}

// ComputedMiddleware uses a middleware that is a call result.
func ComputedMiddleware() http.Handler {
	r := chi.NewRouter()
	r.Use(mk()) // want unsupported_middleware_form
	r.Get("/computed-mw", ok)
	return r
}

// DynamicPath registers a non-constant path.
func DynamicPath(prefix string) http.Handler {
	r := chi.NewRouter()
	r.Use(M)
	r.Get(prefix+"/x", ok) // want unresolved_dynamic_route
	return r
}

// DynamicHandler registers a handler that is a call result.
func DynamicHandler() http.Handler {
	r := chi.NewRouter()
	r.Get("/dyn-handler", makeHandler()) // want unresolved_dynamic_route
	return r
}

// NamedGroup passes a named function to Group.
func NamedGroup() http.Handler {
	r := chi.NewRouter()
	r.Group(register) // want unsupported_router_form
	return r
}

// SameMuxConflict registers the same pattern twice on one ServeMux.
func SameMuxConflict() http.Handler {
	m := http.NewServeMux()
	m.Handle("GET /a", M(http.HandlerFunc(ok)))
	m.Handle("GET /a", M(http.HandlerFunc(ok))) // want unsupported_router_form
	return m
}

// SeparateMuxes registers the same pattern on two muxes: both are kept.
func SeparateMuxes() (http.Handler, http.Handler) {
	m1, m2 := http.NewServeMux(), http.NewServeMux()
	m1.Handle("GET /b", M(http.HandlerFunc(ok)))
	m2.Handle("GET /b", M(http.HandlerFunc(ok))) // want duplicate_route
	return m1, m2
}
