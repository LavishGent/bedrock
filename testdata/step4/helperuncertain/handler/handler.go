package handler

import (
	"database/sql"

	"example.com/s4/helperuncertain/ext"
	"example.com/s4/helperuncertain/repo"
)

// Serve is a governed Entry; it reaches the reflective call in ext.
func Serve(db *sql.DB, f any) {
	repo.Save(db)
	ext.Do(f)
}
