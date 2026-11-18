// Package handler holds the HTTP handlers. Handlers reach the database only
// through Store, which repo.Store implements.
package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"example.com/demo/internal/billing"
	"example.com/demo/internal/repo"
)

// Store is the repository API the handlers use.
type Store interface {
	ListItems(ctx context.Context) ([]repo.Item, error)
	CreateItem(ctx context.Context, name string, cents int64) error
	ListUsers(ctx context.Context) ([]string, error)
}

// Handlers serves the demo API.
type Handlers struct {
	Store Store
	DB    *sql.DB
}

// New returns Handlers backed by store and db.
func New(store Store, db *sql.DB) *Handlers { return &Handlers{Store: store, DB: db} }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Healthz is the public liveness probe.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ListItems returns all items.
func (h *Handlers) ListItems(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.ListItems(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// CreateItem stores the posted item.
func (h *Handlers) CreateItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Cents int64  `json:"cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Store.CreateItem(r.Context(), in.Name, in.Cents); err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// BillingSummary returns the caller's invoice summary.
func (h *Handlers) BillingSummary(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.ListItems(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	s, err := billing.Summarize(r.Context(), items)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, s)
}

// AdminUsers lists users.
func (h *Handlers) AdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, users)
}

// Export dumps all items for internal tooling.
func (h *Handlers) Export(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.QueryContext(r.Context(), "SELECT id, name, cents FROM items ORDER BY id") // VIOLATION db-in-repo (restrict_concept_to_boundary)
	if err != nil {
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var items []repo.Item
	for rows.Next() {
		var it repo.Item
		if err := rows.Scan(&it.ID, &it.Name, &it.Cents); err != nil {
			http.Error(w, "export failed", http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	writeJSON(w, items)
}
