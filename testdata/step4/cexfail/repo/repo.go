package repo

import (
	"database/sql"

	"example.com/s4/cexfail/mid"
)

// B is the boundary symbol: edges b->c only.
func B(db *sql.DB) { mid.C(db) }
