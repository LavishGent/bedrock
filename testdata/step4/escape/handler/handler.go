package handler

import (
	"context"
	"database/sql"

	"example.com/s4/escape/repo"
)

// Serve executes the escaped Stmt and Conn outside repo.
func Serve(ctx context.Context, db *sql.DB) {
	if st, err := repo.Prepare(db); err == nil {
		_, _ = st.Query(1) // FAIL
	}
	if c, err := repo.Conn(ctx, db); err == nil {
		_, _ = c.QueryContext(ctx, "SELECT 1") // FAIL
	}
}
