// Package auth provides the approved authentication middleware (bound to the
// auth.middleware concept) and the public auth API. Protected by CODEOWNERS.
package auth

import (
	"context"
	"net/http"
	"strings"

	"example.com/demo/internal/auth/private"
)

type ctxKey struct{}

// RequireAuth rejects requests without a valid bearer token with 401 and
// stores the authenticated user in the request context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, ok := private.Lookup(token)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

// UserFromContext returns the user stored by RequireAuth.
func UserFromContext(ctx context.Context) (string, bool) {
	u, ok := ctx.Value(ctxKey{}).(string)
	return u, ok
}

// IsAdmin is the public role check.
func IsAdmin(user string) bool { return private.IsAdmin(user) }
