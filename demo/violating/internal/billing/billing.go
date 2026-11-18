// Package billing computes invoices for the authenticated user.
package billing

import (
	"context"
	"errors"

	"example.com/demo/internal/auth"
	"example.com/demo/internal/auth/private" // VIOLATION auth-private-import (forbid_import)
	"example.com/demo/internal/repo"
)

// Summary is an invoice summary.
type Summary struct {
	User  string
	Cents int64
}

// ErrNoUser is returned when the context carries no authenticated user.
var ErrNoUser = errors.New("billing: no authenticated user")

// Summarize totals items for the user in ctx; admins get 10% off.
func Summarize(ctx context.Context, items []repo.Item) (Summary, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return Summary{}, ErrNoUser
	}
	var total int64
	for _, it := range items {
		total += it.Cents
	}
	if private.IsAdmin(user) {
		total -= total / 10
	}
	return Summary{User: user, Cents: total}, nil
}
