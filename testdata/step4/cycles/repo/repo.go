package repo

import (
	"database/sql"

	"example.com/s4/cycles/cyc"
)

// Run is the only way into the A/B cycle.
func Run(db *sql.DB, n int) { cyc.A(db, n) }
