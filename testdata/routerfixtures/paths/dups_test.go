package paths

import (
	"net/http/httptest"
	"testing"
)

func TestDupCases(t *testing.T) {
	for _, c := range DupCases {
		h, p := c.Build()
		if (p != nil) != c.Panics {
			t.Errorf("%s: real router panic=%v, fixture says panics=%v", c.Name, p, c.Panics)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.Probe.Method, c.Probe.Path, nil))
		if rec.Code != c.Probe.Status || rec.Body.String() != c.Body {
			t.Errorf("%s: %s %s -> %d %q, fixture says %d %q", c.Name, c.Probe.Method, c.Probe.Path,
				rec.Code, rec.Body.String(), c.Probe.Status, c.Body)
		}
	}
}
