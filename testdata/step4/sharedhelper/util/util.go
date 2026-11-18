package util

import "database/sql"

// OnlyFromRepo is called only from repo: every Entry path passes through repo.
func OnlyFromRepo(db *sql.DB) {
	_, _ = db.Exec("DELETE FROM t")
}

// Shared is also called from the handler, which bypasses repo.
func Shared(db *sql.DB) {
	_, _ = db.Exec("UPDATE t SET x = 1") // FAIL
}
