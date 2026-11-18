// Package private holds auth internals. Policy rule auth-private-import
// forbids importing it from outside example.com/demo/internal/auth. Go's own
// internal rule allows any package under example.com/demo to import it, so
// both demo states compile; the restriction is policy, not the compiler.
package private

import "crypto/subtle"

var tokens = map[string]string{
	"demo-token-alice": "alice",
	"demo-token-admin": "admin",
}

// Lookup returns the user for a bearer token.
func Lookup(token string) (string, bool) {
	for t, user := range tokens {
		if subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			return user, true
		}
	}
	return "", false
}

// IsAdmin reports whether user has the admin role.
func IsAdmin(user string) bool { return user == "admin" }
