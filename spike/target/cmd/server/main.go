// Command server wires the spike service: one chi route over one handler.
package main

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/spike/internal/handler"
	"example.com/spike/internal/repo"
)

func main() {
	// No driver is registered: the spike only has to compile and be analyzed.
	db, err := sql.Open("spike-driver", "")
	if err != nil {
		log.Fatal(err)
	}
	h := &handler.Items{Store: repo.New(db)}

	r := chi.NewRouter()
	r.Post("/items", h.Create)

	log.Fatal(http.ListenAndServe(":8080", r))
}
