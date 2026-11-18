// Package handler holds the spike's HTTP handler.
package handler

import (
	"net/http"

	"example.com/spike/internal/repo"
)

// Items serves the items endpoint.
type Items struct {
	Store *repo.Store
}

// Create stores the "name" form value.
func (h *Items) Create(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	if err := h.Store.SaveItem(r.Context(), name); err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
