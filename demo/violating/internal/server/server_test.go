package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	return serve(t, h, method, path, token, body).Code
}

func serve(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRoutes(t *testing.T) {
	h := handler.New(&fakeStore{items: []repo.Item{{ID: 1, Name: "a", Cents: 100}}}, openStub("ok"))
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := do(t, c.srv, c.method, c.path, c.tok, c.body); got != c.want {
				t.Fatalf("%s %s: status %d, want %d", c.method, c.path, got, c.want)
			}
		})
	}
}

// Export is exercised with one stub DB per case, passed both as the Store
// (through repo.New) and as the DB, so the assertions hold for the seeded
// implementation (h.DB.QueryContext) and for the approved fix
// (h.Store.ListItems) alike.
func TestExport(t *testing.T) {
	want := []repo.Item{{ID: 1, Name: "widget", Cents: 250}, {ID: 2, Name: "gadget", Cents: 1000}}
	cases := []struct {
		name, dsn, token string
		status           int
		rows             []repo.Item
	}{
		{"ok", "ok", "demo-token-admin", 200, want},
		{"db failure", "fail", "demo-token-admin", 500, nil},
		{"no auth", "ok", "", 401, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openStub(c.dsn)
			t.Cleanup(func() { _ = db.Close() })
			admin := server.NewAdmin(handler.New(repo.New(db), db))
			rec := serve(t, admin, "GET", "/internal/export", c.token, "")
			if rec.Code != c.status {
				t.Fatalf("status %d, want %d (body %q)", rec.Code, c.status, rec.Body.String())
			}
			if c.rows == nil {
				return
			}
			var got []repo.Item
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("body %q: %v", rec.Body.String(), err)
			}
			if !reflect.DeepEqual(got, c.rows) {
				t.Fatalf("rows %+v, want %+v", got, c.rows)
			}
		})
	}
}
