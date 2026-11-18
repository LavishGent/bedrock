package repo

import (
	"database/sql"

	"example.com/s4/exportedhelper/helper"
)

// Save is the approved caller of helper.Exec.
func Save(db *sql.DB) { helper.Exec(db) }
