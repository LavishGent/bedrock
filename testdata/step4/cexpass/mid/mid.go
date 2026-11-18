package mid

import "database/sql"

// C is called by repo.B and by U.
func C(db *sql.DB) {
	_, _ = db.Exec("SELECT 1")
}

// U calls C. U is called by repo.B, so Entry = {B}: every path to C
// passes through B and C passes.
func U(db *sql.DB) { C(db) }
