// Package server registers every route. It is the scope of policy rule
// routes-require-auth; registrations follow the router support matrix v1.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/demo/internal/auth"
	"example.com/demo/internal/handler"
)

// NewAPI returns the public API router (chi).
func NewAPI(h *handler.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", h.Healthz)               // public: policy exception "GET /healthz"
	r.Get("/api/v1/admin/users", h.AdminUsers) // VIOLATION routes-require-auth (require_middleware)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/items", h.ListItems)
		r.Post("/items", h.CreateItem)
		r.Get("/billing/summary", h.BillingSummary)
	})
	return r
}

// NewAdmin returns the internal admin mux (net/http), using the one supported
// direct wrapper form mux.Handle(pattern, auth.RequireAuth(handler)).
func NewAdmin(h *handler.Handlers) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /internal/export", auth.RequireAuth(http.HandlerFunc(h.Export)))
	return mux
}
