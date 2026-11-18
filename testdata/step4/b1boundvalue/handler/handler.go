package handler

import "database/sql"

// Q's Raw is bound; it is called through a method value ($bound wrapper).
type Q struct{ DB *sql.DB }

// Raw queries outside repo.
func (q *Q) Raw() {
	_, _ = q.DB.Exec("outside") // FAIL
}

// Serve calls the bound method value.
func Serve(q *Q) {
	f := q.Raw
	f() // FAIL
}
