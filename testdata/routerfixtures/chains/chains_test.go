package chains

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAcceptedChains(t *testing.T) {
	for _, f := range Accepted {
		h := f.Build()
		for route, want := range f.Chains {
			method, path, _ := strings.Cut(route, " ")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code != 200 {
				t.Errorf("%s: %s -> %d", f.Name, route, rec.Code)
				continue
			}
			got := rec.Header().Values("X-Chain")
			if got == nil {
				got = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %s chain %v, spec says %v", f.Name, route, got, want)
			}
		}
	}
}

func TestRejectedLateUsePanics(t *testing.T) {
	for _, f := range Rejected {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: chi accepted a late Use; the spec's rejection is wrong", f.Name)
				}
			}()
			f.Build()
		}()
	}
}
