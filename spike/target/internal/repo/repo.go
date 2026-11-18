// Package repo is the spike's approved repository boundary.
package repo

import (
	"context"
	"database/sql"
)

// Store wraps a database handle.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// SaveItem inserts one item. This is the spike's only database/sql call.
func (s *Store) SaveItem(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO items(name) VALUES (?)", name)
	return err
}
