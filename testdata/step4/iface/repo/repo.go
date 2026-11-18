package repo

import "database/sql"

// Store implements handler.Querier inside the boundary.
type Store struct{ DB *sql.DB }

// Count runs inside repo: it passes.
func (s *Store) Count() {
	_, _ = s.DB.Exec("SELECT count(*) FROM t")
}
