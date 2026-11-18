package handler

import (
	"database/sql"

	"example.com/s4/iface/repo"
)

// Querier is dispatched dynamically; VTA resolves both implementations.
type Querier interface{ Count() }

type direct struct{ db *sql.DB }

// Count queries outside repo and is reached through the interface.
func (d direct) Count() {
	_, _ = d.db.Exec("SELECT count(*) FROM u") // FAIL
}

// Serve dispatches through the interface.
func Serve(db *sql.DB, useRepo bool) {
	var q Querier = direct{db}
	if useRepo {
		q = &repo.Store{DB: db}
	}
	q.Count()
}
