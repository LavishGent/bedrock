package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/demo/internal/handler"
	"example.com/demo/internal/repo"
	"example.com/demo/internal/server"
)

type fakeStore struct{ items []repo.Item }

func (f *fakeStore) ListItems(context.Context) ([]repo.Item, error) { return f.items, nil }
func (f *fakeStore) CreateItem(_ context.Context, name string, cents int64) error {
	f.items = append(f.items, repo.Item{ID: int64(len(f.items) + 1), Name: name, Cents: cents})
	return nil
}
func (f *fakeStore) ListUsers(context.Context) ([]string, error) {
	return []string{"admin", "alice"}, nil
}

func do(t *testing.T, h http.Handler, method, path, token, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRoutes(t *testing.T) {
	h := handler.New(&fakeStore{items: []repo.Item{{ID: 1, Name: "a", Cents: 100}}})
	api, admin := server.NewAPI(h), server.NewAdmin(h)
	cases := []struct {
		name              string
		srv               http.Handler
		method, path, tok string
		body              string
		want              int
	}{
		{"healthz is public", api, "GET", "/healthz", "", "", 200},
		{"items needs auth", api, "GET", "/api/v1/items", "", "", 401},
		{"items with auth", api, "GET", "/api/v1/items", "demo-token-alice", "", 200},
		{"create needs auth", api, "POST", "/api/v1/items", "", `{"name":"b","cents":5}`, 401},
		{"create with auth", api, "POST", "/api/v1/items", "demo-token-alice", `{"name":"b","cents":5}`, 201},
		{"billing with auth", api, "GET", "/api/v1/billing/summary", "demo-token-admin", "", 200},
		{"export needs auth", admin, "GET", "/internal/export", "", "", 401},
		{"export with auth", admin, "GET", "/internal/export", "demo-token-admin", "", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := do(t, c.srv, c.method, c.path, c.tok, c.body); got != c.want {
				t.Fatalf("%s %s: status %d, want %d", c.method, c.path, got, c.want)
			}
		})
	}
}
