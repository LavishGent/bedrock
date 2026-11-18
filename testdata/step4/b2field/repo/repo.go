package repo

import (
	"database/sql"

	"example.com/s4/b2field/helper"
)

// Save is the only static caller of helper.Raw.
func Save(db *sql.DB) { helper.Raw(db) }
