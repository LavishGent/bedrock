package handler

import "database/sql"

// Raw is an ordinary function that is also bound as a concept symbol: its
// own SQL call is still checked (review B1).
func Raw(db *sql.DB) {
	_, _ = db.Exec("outside") // FAIL
}
