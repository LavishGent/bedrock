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
