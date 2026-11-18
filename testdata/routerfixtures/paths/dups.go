package paths

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func body(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(s)) }
}

// DupCase is a duplicate/conflict fixture (router matrix, section 5,
// "Duplicates and conflicts"). Facts lists the route facts the analyzer emits,
// in registration order; "diag:<kind>" marks a registration that yields a
// diagnostic and no route fact.
type DupCase struct {
	Name   string
	Code   string
	Facts  []string
	Build  func() (http.Handler, any) // handler to probe, panic value (nil if none)
	Probe  Probe
	Body   string // response body the probe must see
	Panics bool
}

func replay(f func(m *http.ServeMux)) func() (http.Handler, any) {
	return func() (h http.Handler, p any) {
		m := http.NewServeMux()
		defer func() { h, p = m, recover() }()
		f(m)
		return m, nil
	}
}

// DupCases are checked against the real routers by dups_test.go.
var DupCases = []DupCase{
	{"nethttp-same-mux-identical",
		`mux.Handle("GET /a", A); mux.Handle("GET /a", B)`,
		[]string{"route:GET /a#0 (handler A)", "diag:unsupported_router_form (conflict; no fact for B)"},
		replay(func(m *http.ServeMux) { m.Handle("GET /a", body("A")); m.Handle("GET /a", body("B")) }),
		Probe{"GET", "/a", 200}, "A", true},
	{"nethttp-same-mux-conflicting-wildcards",
		`mux.Handle("GET /x/{a}", A); mux.Handle("GET /x/{b}", B)`,
		[]string{"route:GET /x/{a}#0 (handler A)", "diag:unsupported_router_form (conflict; no fact for B)"},
		replay(func(m *http.ServeMux) { m.Handle("GET /x/{a}", body("A")); m.Handle("GET /x/{b}", body("B")) }),
		Probe{"GET", "/x/1", 200}, "A", true},
	{"nethttp-same-mux-method-and-any",
		`mux.Handle("GET /x", A); mux.Handle("/x", B)`,
		[]string{"route:GET /x#0 (handler A)", "route:* /x#0 (handler B)"},
		replay(func(m *http.ServeMux) { m.Handle("GET /x", body("A")); m.Handle("/x", body("B")) }),
		Probe{"POST", "/x", 200}, "B", false},
	{"nethttp-separate-muxes-identical",
		`m1.Handle("GET /a", A); m2.Handle("GET /a", B)  // same function, two muxes`,
		[]string{"route:GET /a#0 (handler A)", "route:GET /a#1 (handler B) + duplicate_route warning"},
		func() (http.Handler, any) {
			m1, m2 := http.NewServeMux(), http.NewServeMux()
			m1.Handle("GET /a", body("A"))
			m2.Handle("GET /a", body("B"))
			return m2, nil
		},
		Probe{"GET", "/a", 200}, "B", false},
	{"chi-same-mux-identical",
		`r.Get("/a", A); r.Get("/a", B)`,
		[]string{"route:GET /a#0 (handler A)", "route:GET /a#1 (handler B) + duplicate_route warning"},
		func() (http.Handler, any) {
			r := chi.NewRouter()
			r.Get("/a", body("A"))
			r.Get("/a", body("B"))
			return r, nil
		},
		Probe{"GET", "/a", 200}, "B", false},
}
