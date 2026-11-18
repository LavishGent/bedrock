// Command demo runs the API (chi, :8080) and the internal admin mux
// (net/http, :8081). The SQL driver is linked by the deployment; the demo is
// built, tested and analyzed, not run in CI.
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	"example.com/demo/internal/handler"
	"example.com/demo/internal/repo"
	"example.com/demo/internal/server"
)

func main() {
	db, err := sql.Open(os.Getenv("DEMO_SQL_DRIVER"), os.Getenv("DEMO_SQL_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	h := handler.New(repo.New(db), db)
	go func() { log.Fatal(http.ListenAndServe(":8081", server.NewAdmin(h))) }()
	log.Fatal(http.ListenAndServe(":8080", server.NewAPI(h)))
}
