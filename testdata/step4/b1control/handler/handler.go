package handler

import "database/sql"

// Raw is the same function without the extra binding (review B1 control).
func Raw(db *sql.DB) {
	_, _ = db.Exec("outside") // FAIL
}
