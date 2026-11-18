package helper

import "database/sql"

// Exec is exported but called only from repo: it is not an Entry, so it
// passes.
func Exec(db *sql.DB) {
	_, _ = db.Exec("INSERT INTO t VALUES (1)")
}

// Unused is exported and has no caller: it is an Entry, so it fails.
func Unused(db *sql.DB) {
	_, _ = db.Exec("INSERT INTO t VALUES (2)") // FAIL
}
