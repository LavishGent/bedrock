package repo

import "database/sql"

// Save is the approved query.
func Save(db *sql.DB) {
	_, _ = db.Exec("INSERT INTO t VALUES (1)")
}
