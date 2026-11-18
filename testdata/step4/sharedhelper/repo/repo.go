package repo

import (
	"database/sql"

	"example.com/s4/sharedhelper/util"
)

// Load is the approved path; it uses both helpers.
func Load(db *sql.DB) {
	util.OnlyFromRepo(db)
	util.Shared(db)
}
