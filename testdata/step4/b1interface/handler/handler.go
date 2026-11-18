package handler

import "database/sql"

// Execer is dispatched dynamically.
type Execer interface{ Run() }

// Q's Run is bound and reached through the interface.
type Q struct{ DB *sql.DB }

// Run queries outside repo.
func (q *Q) Run() {
	_, _ = q.DB.Exec("outside") // FAIL
}

// Serve dispatches through Execer.
func Serve(db *sql.DB) {
	var e Execer = &Q{DB: db}
	e.Run() // FAIL
}
