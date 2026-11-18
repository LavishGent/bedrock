package repo

import "database/sql"

// WithTx queries from a closure inside repo: the closure is owned by repo.
func WithTx(db *sql.DB) {
	do := func() {
		_, _ = db.Exec("SELECT 2")
	}
	do()
}
