package check

import (
	"testing"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/policy"
)

func unit(id, origin string, imports ...string) *analyze.UnitInfo {
	u := &analyze.UnitInfo{Origin: origin, Shard: &facts.Shard{Unit: facts.Unit{ID: id}}}
	for i, to := range imports {
		u.Imports = append(u.Imports, facts.Import{FromUnit: id, ToUnit: to, ToPath: to,
			Loc: &facts.Location{Root: "repo", File: id + ".go", Line: 3 + i, Column: 2}})
	}
	return u
}

func fixture() *analyze.Result {
	r := &analyze.Result{Units: map[string]*analyze.UnitInfo{
		"m/auth":           unit("m/auth", "module", "m/auth/private"),
		"m/auth/private":   unit("m/auth/private", "module"),
		"m/auth/private/x": unit("m/auth/private/x", "module", "m/auth/private"),
		"m/billing":        unit("m/billing", "module", "m/auth/private", "net/http"),
		"m/handler":        unit("m/handler", "module", "m/billing"),
		"net/http":         unit("net/http", "stdlib"),
	}}
	for id, u := range r.Units {
		if u.Origin == "module" {
			r.Module = append(r.Module, id)
		}
	}
	return r
}

func pol(rule policy.Rule) (*policy.Policy, policy.Rule) {
	return &policy.Policy{Boundaries: map[string][]string{
		"app": {"m/**"}, "auth": {"m/auth"}, "private": {"m/auth/private/**"},
		"http": {"net/http"}, "nothing": {"x/**"},
	}}, rule
}

func TestForbidImport(t *testing.T) {
	p, r := pol(policy.Rule{ID: "r", Kind: policy.ForbidImport, Scope: "app", Target: "private", ExceptFrom: []string{"auth"}, Message: "m"})
	vs, empties, _ := forbidImport(p, r, fixture())
	if len(empties) != 0 {
		t.Fatalf("unexpected empty_match: %v", empties)
	}
	// auth is excepted; auth/private/x is inside the target (a boundary may
	// import itself); handler imports billing, not private (direct only).
	if len(vs) != 1 || vs[0].EvidenceChain[0].From != "m/billing" || vs[0].Location.Line != 3 {
		t.Fatalf("violations = %+v, want exactly billing -> auth/private", vs)
	}
}

func TestForbidImportThirdPartyTarget(t *testing.T) {
	p, r := pol(policy.Rule{ID: "r", Kind: policy.ForbidImport, Scope: "app", Target: "http", Message: "m"})
	vs, _, _ := forbidImport(p, r, fixture())
	if len(vs) != 1 || vs[0].EvidenceChain[0].To != "net/http" {
		t.Fatalf("violations = %+v, want billing -> net/http", vs)
	}
}

func TestForbidImportEmptyMatch(t *testing.T) {
	p, r := pol(policy.Rule{ID: "r", Kind: policy.ForbidImport, Scope: "app", Target: "nothing", Message: "m"})
	vs, empties, _ := forbidImport(p, r, fixture())
	if len(vs) != 0 || len(empties) != 1 || empties[0].Kind != "empty_match" {
		t.Fatalf("got %v violations, %v diagnostics; want one empty_match and no violations", vs, empties)
	}
	// Scope matching only non-module units is empty too: scope is module-only.
	p, r = pol(policy.Rule{ID: "r", Kind: policy.ForbidImport, Scope: "http", Target: "private", Message: "m"})
	if _, empties, _ := forbidImport(p, r, fixture()); len(empties) != 1 {
		t.Fatalf("scope over stdlib only must be empty_match, got %v", empties)
	}
}

func TestVerdictPrecedence(t *testing.T) {
	r := newReport("p", nil)
	r.Rules = []facts.RuleResult{{ID: "a", ViolationIDs: []string{"viol:1"}, DiagnosticIDs: []string{}},
		{ID: "b", ViolationIDs: []string{}, DiagnosticIDs: []string{}}}
	loc := &facts.Location{Root: "repo", File: "f.go", Line: 1, Column: 1}
	r.Violations = []facts.Violation{{ID: "viol:1", RuleID: "a", Location: loc}}
	d := facts.NewDiagnostic("analyzer_timeout", "error", nil, nil, "t", nil)
	r.Diagnostics = []facts.ReportDiagnostic{global(d)}
	finish(r)
	if r.Status != "VIOLATION" || r.ExitCode != 1 || r.Rules[1].Status != "INDETERMINATE" {
		t.Fatalf("status %s/%d, rule b %s; want VIOLATION/1 with b INDETERMINATE", r.Status, r.ExitCode, r.Rules[1].Status)
	}
}

// Known-route chain uncertainty is projected per rule scope and exceptions;
// unknown registration identity reaches every rule (router matrix section 2).
func TestRouteDiagProjection(t *testing.T) {
	path, get := "/x", "GET"
	loc := &facts.Location{Root: "repo", File: "a.go", Line: 1, Column: 1}
	rt := &analyze.Route{Fact: facts.Route{ID: "route:m/a:GET:/x#0", UnitID: "m/a", Method: &get, Path: &path, Loc: loc},
		Uncertain: []string{"diag:chain"}}
	res := &analyze.Result{Module: []string{"m/a", "m/b"}, Routes: []*analyze.Route{rt},
		MiddlewareShaped: map[string]bool{"m/a.Auth": true},
		RouteDiags: []analyze.RouteDiag{
			{Diag: facts.Diagnostic{ID: "diag:chain"}, Cat: analyze.CatChain, Unit: "m/a", Routes: []*analyze.Route{rt}},
			{Diag: facts.Diagnostic{ID: "diag:identity"}, Cat: analyze.CatIdentity, Unit: "m/b"},
		}}
	pol := &policy.Policy{
		Boundaries: map[string][]string{"a": {"m/a"}, "b": {"m/b"}},
		Concepts:   map[string]policy.Concept{"auth": {Symbols: []string{"m/a.Auth"}}},
	}
	cases := []struct {
		rule       policy.Rule
		chain, any bool
	}{
		{policy.Rule{ID: "scope-a", Kind: policy.RequireMiddleware, Scope: "a", Middleware: "auth"}, true, true},
		{policy.Rule{ID: "scope-b", Kind: policy.RequireMiddleware, Scope: "b", Middleware: "auth"}, false, true},
		{policy.Rule{ID: "a-excepted", Kind: policy.RequireMiddleware, Scope: "a", Middleware: "auth", ExceptRoutes: []string{"GET /x"}}, false, true},
	}
	for _, c := range cases {
		vs, _, _, _, affects := requireMiddleware(pol, c.rule, res)
		_, chain := affects["diag:chain"]
		_, ident := affects["diag:identity"]
		if chain != c.chain || ident != c.any {
			t.Errorf("%s: chain diag affects=%v identity affects=%v; want %v %v", c.rule.ID, chain, ident, c.chain, c.any)
		}
		if len(vs) != 0 {
			t.Errorf("%s: an uncertain chain must not give a violation, got %v", c.rule.ID, vs)
		}
	}
}

func TestMatchRoute(t *testing.T) {
	get, post := "GET", "POST"
	p := func(s string) *string { return &s }
	cases := []struct {
		pat    string
		method *string
		path   string
		want   bool
	}{
		{"GET /healthz", &get, "/healthz", true},
		{"GET /healthz", &post, "/healthz", false},
		{"GET /healthz", nil, "/healthz", false},
		{"/healthz", nil, "/healthz", true},
		{"/public/**", &get, "/public", true},
		{"/public/**", &get, "/public/a/b", true},
		{"GET /api/v1/items/*", &get, "/api/v1/items/{id}", true},
		{"GET /api/", &get, "/api", false},
		{"GET /api", &get, "/api/", false},
		{"get /x", &get, "/x", false},
	}
	for _, c := range cases {
		if got := matchRoute(c.pat, c.method, p(c.path)); got != c.want {
			t.Errorf("matchRoute(%q, %v, %q) = %v, want %v", c.pat, c.method, c.path, got, c.want)
		}
	}
}
