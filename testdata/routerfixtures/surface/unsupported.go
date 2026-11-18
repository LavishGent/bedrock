// Package surface is the router matrix v1 API audit fixture
// (docs/superpowers/specs/routers/README.md, section 1). Unsupported holds one
// call per registration API that v1 does not support. Each call carries a
// "want" comment naming the diagnostic the step 3 analyzer must emit there;
// surface_test.go checks that every unsupported API in the spec's audit table
// has such a call here, and that the table covers chi's whole exported API.
// The function is never executed (several calls panic in chi).
package surface

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func ok(w http.ResponseWriter, r *http.Request) {}

func mw(next http.Handler) http.Handler { return next }

// Unsupported registers routes in every unsupported form.
func Unsupported() http.Handler {
	r := chi.NewRouter()
	r.Handle("/handle", http.HandlerFunc(ok))                               // want unsupported_router_form
	r.HandleFunc("/handlefunc", ok)                                         // want unsupported_router_form
	r.MethodFunc("GET", "/methodfunc", ok)                                  // want unsupported_router_form
	r.NotFound(ok)                                                          // want unsupported_router_form
	r.MethodNotAllowed(ok)                                                  // want unsupported_router_form
	r.Mount("/mount", http.HandlerFunc(ok))                                 // want unsupported_router_form
	chi.RegisterMethod("LINK")                                              // want unsupported_router_form
	r.Get("/chain", chi.Chain(mw).HandlerFunc(ok).ServeHTTP)                // want unsupported_middleware_form
	r.Method("GET", "/chainh", chi.Chain(mw).Handler(http.HandlerFunc(ok))) // want unsupported_middleware_form

	m := http.NewServeMux()
	m.Handle("GET /strip", http.StripPrefix("/strip", http.HandlerFunc(ok))) // want unsupported_middleware_form
	m.Handle("GET /nested", mw(mw(http.HandlerFunc(ok))))                    // want unsupported_middleware_form
	return r
}
