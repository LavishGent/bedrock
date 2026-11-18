package handler

import "database/sql"

// Raw is bound and runs its query from a closure.
func Raw(db *sql.DB) {
	run := func() {
		_, _ = db.Exec("outside") // FAIL
	}
	run()
}
