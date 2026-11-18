package handler

import (
	"database/sql"

	"example.com/s4/sharedhelper/repo"
	"example.com/s4/sharedhelper/util"
)

// Serve has no caller, so it is an Entry.
func Serve(db *sql.DB) {
	repo.Load(db)
	util.Shared(db)
}
