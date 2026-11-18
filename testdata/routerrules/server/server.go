// Package server is the require_middleware two-rule fixture (step 3): one
// attachment satisfies one rule and violates another, and an except_routes
// entry that matches nothing does not hide a real unprotected route.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Auth is the authentication middleware (concept auth.middleware).
func Auth(next http.Handler) http.Handler { return next }

// Audit is the audit middleware (concept audit.middleware).
func Audit(next http.Handler) http.Handler { return next }

func export(w http.ResponseWriter, r *http.Request) {}

func open(w http.ResponseWriter, r *http.Request) {}

// Admin wraps its route in Auth only: satisfies needs-auth, violates needs-audit.
func Admin() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /export", Auth(http.HandlerFunc(export)))
	return mux
}

// API registers a route with no middleware: violates both rules.
func API() http.Handler {
	r := chi.NewRouter()
	r.Get("/open", open)
	return r
}
