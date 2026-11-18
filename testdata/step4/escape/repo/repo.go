package repo

import (
	"context"
	"database/sql"
)

// Prepare returns a statement that escapes the boundary.
func Prepare(db *sql.DB) (*sql.Stmt, error) {
	return db.Prepare("SELECT * FROM t WHERE id = ?")
}

// Conn returns a connection that escapes the boundary.
func Conn(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	return db.Conn(ctx)
}
