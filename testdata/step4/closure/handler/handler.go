package handler

import (
	"database/sql"

	"example.com/s4/closure/repo"
)

// Serve queries from a closure outside repo: the closure is the caller.
func Serve(db *sql.DB) {
	run := func() {
		_, _ = db.Exec("SELECT 1") // FAIL
	}
	run()
	repo.WithTx(db)
}
