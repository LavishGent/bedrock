package mid

import "database/sql"

// C is called by repo.B and by U.
func C(db *sql.DB) {
	_, _ = db.Exec("SELECT 1") // FAIL
}

// U calls C. U has no caller, so it is an Entry and reaches C
// without passing through B: Entry = {B, U}, C fails.
func U(db *sql.DB) { C(db) }
