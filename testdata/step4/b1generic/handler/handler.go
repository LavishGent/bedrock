package handler

import "database/sql"

// Raw is a bound generic function: its instantiated body is real code.
func Raw[T any](db *sql.DB, v T) {
	_, _ = db.Exec("outside", v) // FAIL
}

// Serve instantiates Raw.
func Serve(db *sql.DB) {
	Raw(db, 1) // FAIL
}
