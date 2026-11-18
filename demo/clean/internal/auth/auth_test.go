package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/demo/internal/auth"
)

// These tests fail if RequireAuth is replaced by a no-op wrapper
// (func(next http.Handler) http.Handler { return next }): Bedrock only checks
// that RequireAuth is attached, so its behavior must be tested (plan section 9).

func protected(t *testing.T) (http.Handler, *bool) {
	t.Helper()
	called := false
	h := auth.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := auth.UserFromContext(r.Context()); !ok {
			t.Error("handler reached without an authenticated user in context")
		}
		w.WriteHeader(http.StatusOK)
	}))
	return h, &called
}

func TestRequireAuthRejects(t *testing.T) {
	for name, header := range map[string]string{
		"missing":   "",
		"malformed": "Token demo-token-alice",
		"unknown":   "Bearer not-a-token",
	} {
		t.Run(name, func(t *testing.T) {
			h, called := protected(t)
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if *called {
				t.Fatal("next handler ran for an unauthenticated request")
			}
		})
	}
}

func TestRequireAuthAccepts(t *testing.T) {
	h, called := protected(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer demo-token-alice")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !*called {
		t.Fatalf("status = %d, called = %v; want 200, true", rec.Code, *called)
	}
}
