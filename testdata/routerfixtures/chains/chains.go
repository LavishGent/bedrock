// Package chains holds the router matrix v1 middleware-chain fixtures
// (docs/superpowers/specs/routers/README.md, section 4). Each Accepted
// fixture lists the chain the analyzer must compute per route; chains_test.go
// checks those chains against real chi v5.5.0 at run time, so the spec cannot
// drift from chi. Each Rejected fixture is a late Use, which chi rejects with
// a panic; the analyzer must emit unsupported_middleware_form for it and must
// not credit the middleware.
package chains

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func mark(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Chain", name)
		next.ServeHTTP(w, r)
	})
}

// A, B, C and D are direct-reference middlewares (the supported form).
func A(next http.Handler) http.Handler { return mark("A", next) }
func B(next http.Handler) http.Handler { return mark("B", next) }
func C(next http.Handler) http.Handler { return mark("C", next) }
func D(next http.Handler) http.Handler { return mark("D", next) }

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// Fixture is one accepted router setup and the chain of every route.
type Fixture struct {
	Name   string
	Build  func() http.Handler
	Chains map[string][]string // "METHOD /path" -> chain, outermost first
}

// Accepted fixtures.
var Accepted = []Fixture{
	{"root-use", func() http.Handler {
		r := chi.NewRouter()
		r.Use(A)
		r.Get("/x", ok)
		return r
	}, map[string][]string{"GET /x": {"A"}}},

	{"use-then-with", func() http.Handler {
		r := chi.NewRouter()
		r.Use(A)
		r.With(B).Get("/x", ok)
		r.Get("/y", ok)
		return r
	}, map[string][]string{"GET /x": {"A", "B"}, "GET /y": {"A"}}},

	{"with-with", func() http.Handler {
		r := chi.NewRouter()
		r.With(A).With(B).Get("/x", ok)
		return r
	}, map[string][]string{"GET /x": {"A", "B"}}},

	{"with-snapshot", func() http.Handler {
		// w2 copies w's list when With is called; the later w.Use(C) is not
		// inherited by w2, but does apply to routes registered on w afterwards.
		r := chi.NewRouter()
		w := r.With(A)
		w2 := w.With(B)
		w.Use(C)
		w.Get("/w", ok)
		w2.Get("/w2", ok)
		return r
	}, map[string][]string{"GET /w": {"A", "C"}, "GET /w2": {"A", "B"}}},

	{"with-group", func() http.Handler {
		r := chi.NewRouter()
		r.With(A).Group(func(g chi.Router) {
			g.Use(B)
			g.Get("/x", ok)
		})
		return r
	}, map[string][]string{"GET /x": {"A", "B"}}},

	{"with-route", func() http.Handler {
		r := chi.NewRouter()
		r.With(A).Route("/p", func(s chi.Router) {
			s.Use(B)
			s.Get("/x", ok)
		})
		return r
	}, map[string][]string{"GET /p/x": {"A", "B"}}},

	{"route-with", func() http.Handler {
		r := chi.NewRouter()
		r.Use(A)
		r.Route("/p", func(s chi.Router) {
			s.Use(B)
			s.With(C).Get("/x", ok)
			s.Get("/y", ok)
		})
		return r
	}, map[string][]string{"GET /p/x": {"A", "B", "C"}, "GET /p/y": {"A", "B"}}},

	{"nested-groups", func() http.Handler {
		r := chi.NewRouter()
		r.Group(func(g chi.Router) {
			g.Use(A)
			g.Group(func(h chi.Router) {
				h.Use(B)
				h.Get("/x", ok)
			})
			g.Get("/y", ok)
		})
		r.Get("/z", ok)
		return r
	}, map[string][]string{"GET /x": {"A", "B"}, "GET /y": {"A"}, "GET /z": {}}},

	{"group-with-route-use", func() http.Handler {
		r := chi.NewRouter()
		r.Use(A)
		r.Group(func(g chi.Router) {
			g.Use(B)
			g.With(C).Route("/p", func(s chi.Router) {
				s.Use(D)
				s.Get("/x", ok)
			})
		})
		return r
	}, map[string][]string{"GET /p/x": {"A", "B", "C", "D"}}},
}

// Rejected fixtures: late Use. chi panics; the analyzer emits
// unsupported_middleware_form on the Use and credits nothing.
var Rejected = []struct {
	Name  string
	Build func()
}{
	{"use-after-route", func() {
		r := chi.NewRouter()
		r.Get("/x", ok)
		r.Use(A)
	}},
	{"use-after-with", func() {
		r := chi.NewRouter()
		r.With(B).Get("/x", ok)
		r.Use(A)
	}},
	{"use-after-group", func() {
		r := chi.NewRouter()
		r.Group(func(g chi.Router) { g.Get("/x", ok) })
		r.Use(A)
	}},
	{"use-after-route-mount", func() {
		r := chi.NewRouter()
		r.Route("/p", func(s chi.Router) { s.Get("/x", ok) })
		r.Use(A)
	}},
	{"inline-use-after-route", func() {
		r := chi.NewRouter()
		r.Group(func(g chi.Router) {
			g.Get("/x", ok)
			g.Use(A)
		})
	}},
}
