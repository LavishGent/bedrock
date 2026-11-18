// Package repo is the approved repository boundary: the only package allowed
// to execute database queries (policy rule db-in-repo).
package repo

import (
	"context"
	"database/sql"
)

// Item is one catalog item.
type Item struct {
	ID    int64
	Name  string
	Cents int64
}

// Store executes all SQL for the demo.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ListItems returns every item.
func (s *Store) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, cents FROM items ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Name, &it.Cents); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// CreateItem inserts an item.
func (s *Store) CreateItem(ctx context.Context, name string, cents int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO items(name, cents) VALUES (?, ?)", name, cents)
	return err
}

// ListUsers returns every user name.
func (s *Store) ListUsers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
