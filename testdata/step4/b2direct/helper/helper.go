package helper

import "database/sql"

// Raw queries outside repo; only reflection reaches it from handler.
func Raw(db *sql.DB) {
	_, _ = db.Exec("outside")
}
