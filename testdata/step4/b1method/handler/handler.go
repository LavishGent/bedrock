package handler

import "database/sql"

// Q's Raw method is bound as a concept symbol; its body is still checked.
type Q struct{ DB *sql.DB }

// Raw queries outside repo.
func (q *Q) Raw() {
	_, _ = q.DB.Exec("outside") // FAIL
}
