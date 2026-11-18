package paths

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCases(t *testing.T) {
	for _, c := range Cases {
		var h http.Handler
		panicked := func() (p bool) {
			defer func() { p = recover() != nil }()
			h = c.Build()
			return false
		}()
		if panicked != c.Panics {
			t.Errorf("%s: real router panicked=%v, fixture says %v", c.Name, panicked, c.Panics)
			continue
		}
		for _, p := range c.Probes {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(p.Method, p.Path, nil))
			if rec.Code != p.Status {
				t.Errorf("%s: %s %s -> %d, fixture says %d", c.Name, p.Method, p.Path, rec.Code, p.Status)
			}
		}
	}
}
