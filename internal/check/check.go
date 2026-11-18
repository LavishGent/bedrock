// Package check runs one `bedrock check`: validate the policy, load the
// module, evaluate the rules this build supports, and assemble the report
// (report.schema.json, schema 0.3) and the unit shards.
//
// Step 3 evaluates forbid_import and require_middleware. Every configured
// rule is listed in rules[]; a rule of a kind this build cannot evaluate
// (restrict_concept_to_boundary) gets a global rule_not_evaluated diagnostic
// that makes it INDETERMINATE (schema 0.4), so the run exits 2 unless a real
// violation gives 1. It never reports PASS for an unchecked rule.
package check

import (
	"context"
	"fmt"
	"sort"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/glob"
	"github.com/LavishGent/bedrock/internal/policy"
)

// ToolVersion is the bedrock version recorded in reports.
const ToolVersion = "0.0.0-poc"

// Options configure one check.
type Options struct {
	Dir         string // module root to analyze
	PolicyPath  string // policy file to read
	PolicyLabel string // policy path as recorded in the report
}

// Output is the result of one check.
type Output struct {
	Report       *facts.Report
	Shards       []*facts.Shard // sorted by unit ID
	NotEvaluated []policy.Rule  // valid rules of kinds not evaluated in this build
}

// Evaluated reports whether this build evaluates rules of kind.
func Evaluated(kind string) bool {
	return kind == policy.ForbidImport || kind == policy.RequireMiddleware
}

// loadErrorKinds are the diagnostics of a unit that failed to load.
var loadErrorKinds = map[string]bool{"package_load_error": true, "parse_or_type_error": true}

func newReport(label string, sum *string) *facts.Report {
	return &facts.Report{
		SchemaVersion: facts.SchemaVersion, Kind: "report",
		Tool:         facts.Tool{Name: "bedrock", Version: ToolVersion},
		BuildProfile: analyze.Profile(),
		Policy:       facts.PolicyRef{Path: label, SHA256: sum},
		Assumptions:  []facts.Assumption{},
		Packages:     facts.Packages{Included: []string{}, Excluded: []facts.ExcludedPackage{}, ExcludedFiles: []facts.ExcludedFile{}},
		Rules:        []facts.RuleResult{},
		Violations:   []facts.Violation{},
		Diagnostics:  []facts.ReportDiagnostic{},
	}
}

func global(d facts.Diagnostic) facts.ReportDiagnostic {
	return facts.ReportDiagnostic{Diagnostic: d, Relevance: "global", AffectsRules: []facts.AffectedRule{}}
}

// PolicyOnly returns the report for a run that stops before analysis (policy
// failure) or aborts (crash, timeout, memory limit): one run-level diagnostic,
// the given rules all INDETERMINATE, exit 2.
func PolicyOnly(label string, sum *string, d facts.Diagnostic, rules []policy.Rule) *facts.Report {
	r := newReport(label, sum)
	r.Diagnostics = append(r.Diagnostics, global(d))
	for _, ru := range rules {
		r.Rules = append(r.Rules, facts.RuleResult{ID: ru.ID, Kind: ru.Kind, Status: "INDETERMINATE",
			ViolationIDs: []string{}, DiagnosticIDs: []string{}})
	}
	finish(r)
	return r
}

// LoadPolicy reads and validates the policy for a check (bounded, regular
// files only).
func LoadPolicy(o Options) policy.Result {
	modPath, _ := analyze.ModulePath(o.Dir) // a missing go.mod is reported as layout later
	return policy.Load(o.PolicyPath, modPath)
}

// Run performs one check.
func Run(ctx context.Context, o Options) *Output { return RunWithPolicy(ctx, o, LoadPolicy(o)) }

// RunWithPolicy performs one check with an already loaded policy.
func RunWithPolicy(ctx context.Context, o Options, pr policy.Result) *Output {
	if pr.Diag != nil {
		return &Output{Report: PolicyOnly(o.PolicyLabel, pr.SHA256, *pr.Diag, nil)}
	}
	pol := pr.Policy
	out := &Output{}
	for _, ru := range pol.Rules {
		if !Evaluated(ru.Kind) {
			out.NotEvaluated = append(out.NotEvaluated, ru)
		}
	}

	res := analyze.Load(ctx, o.Dir)
	// Policy contract 5: a middleware concept must bind only functions of
	// type func(http.Handler) http.Handler. This needs types, so it is
	// checked after loading; a violation is still a policy failure.
	if msg := middlewareShapeErrors(pol, res); msg != "" {
		d := facts.NewDiagnostic("policy_invalid", "error", nil, nil, msg, nil)
		return &Output{Report: PolicyOnly(o.PolicyLabel, pr.SHA256, d, nil)}
	}
	r := newReport(o.PolicyLabel, pr.SHA256)
	for _, id := range sortedIDs(res.Units) {
		out.Shards = append(out.Shards, res.Units[id].Shard)
	}
	r.Inputs.ShardCount = len(out.Shards)
	r.Packages.Included = append(r.Packages.Included, res.Module...)
	r.Packages.Excluded = append(r.Packages.Excluded, res.Excluded...)
	r.Packages.ExcludedFiles = append(r.Packages.ExcludedFiles, res.ExcludedFiles...)
	for _, id := range res.Module {
		r.Packages.ExcludedFiles = append(r.Packages.ExcludedFiles, res.Units[id].Shard.Unit.ExcludedFiles...)
	}
	sort.Slice(r.Packages.Excluded, func(i, j int) bool { return r.Packages.Excluded[i].ID < r.Packages.Excluded[j].ID })
	sort.Slice(r.Packages.ExcludedFiles, func(i, j int) bool {
		return r.Packages.ExcludedFiles[i].Path < r.Packages.ExcludedFiles[j].Path
	})
	r.Summary.UnitsAnalyzed = len(res.Module)

	affects := map[string][]facts.AffectedRule{} // diagnostic ID -> affected rules
	var diags []facts.ReportDiagnostic
	for _, d := range res.Global {
		rd := facts.ReportDiagnostic{Diagnostic: d, Relevance: "warning", AffectsRules: []facts.AffectedRule{}}
		if d.Severity == "error" {
			rd.Relevance = "global"
		}
		diags = append(diags, rd)
	}
	for _, id := range sortedIDs(res.Units) {
		u := res.Units[id]
		for _, d := range u.Shard.Diagnostics {
			diags = append(diags, facts.ReportDiagnostic{Diagnostic: d, AffectsRules: []facts.AffectedRule{},
				UnitOrigin: facts.Str(u.Origin)})
		}
	}

	for _, ru := range pol.Rules {
		rr := facts.RuleResult{ID: ru.ID, Kind: ru.Kind, ViolationIDs: []string{}, DiagnosticIDs: []string{}}
		if !Evaluated(ru.Kind) {
			d := facts.NewDiagnostic("rule_not_evaluated", "error", nil, nil,
				fmt.Sprintf("rule %q (%s) is not evaluated by this build (bedrock %s evaluates forbid_import and require_middleware only); it cannot pass until it is.", ru.ID, ru.Kind, ToolVersion), nil)
			diags = append(diags, facts.ReportDiagnostic{Diagnostic: d, AffectsRules: []facts.AffectedRule{}})
			affects[d.ID] = append(affects[d.ID], facts.AffectedRule{RuleID: ru.ID, Reason: "not_evaluated"})
		} else if !res.Fatal {
			var vs []facts.Violation
			var extra, warnings []facts.Diagnostic
			var scope map[string]bool
			if ru.Kind == policy.ForbidImport {
				vs, extra, scope = forbidImport(pol, ru, res)
			} else {
				var routeAffects map[string]string
				vs, extra, warnings, scope, routeAffects = requireMiddleware(pol, ru, res)
				for id, reason := range routeAffects {
					affects[id] = append(affects[id], facts.AffectedRule{RuleID: ru.ID, Reason: reason})
				}
			}
			for _, d := range warnings { // unmatched except_routes: listed only
				diags = append(diags, facts.ReportDiagnostic{Diagnostic: d, Relevance: "warning", AffectsRules: []facts.AffectedRule{}})
			}
			r.Violations = append(r.Violations, vs...)
			for _, v := range vs {
				rr.ViolationIDs = append(rr.ViolationIDs, v.ID)
			}
			for _, d := range extra { // empty_match: rule-specific, policy_config
				rd := facts.ReportDiagnostic{Diagnostic: d, AffectsRules: []facts.AffectedRule{}}
				diags = append(diags, rd)
				affects[d.ID] = append(affects[d.ID], facts.AffectedRule{RuleID: ru.ID, Reason: "policy_config"})
			}
			// A governed unit that failed to load or type-check (plan section 7).
			for _, d := range diags {
				if d.Scope == "unit" && loadErrorKinds[d.Kind] && scope[*d.UnitID] {
					affects[d.ID] = append(affects[d.ID], facts.AffectedRule{RuleID: ru.ID, Reason: "unit_in_scope"})
				}
			}
		}
		r.Rules = append(r.Rules, rr)
	}
	for i := range diags {
		d := &diags[i]
		if a := affects[d.ID]; len(a) > 0 {
			sort.Slice(a, func(i, j int) bool { return a[i].RuleID < a[j].RuleID })
			d.AffectsRules = a
			d.Relevance = "indeterminate"
		} else if d.Relevance == "" {
			d.Relevance = "warning"
		}
	}
	r.Diagnostics = dedupDiags(diags)
	finish(r)
	out.Report = r
	return out
}

// forbidImport evaluates one forbid_import rule (policy contract 6.1). It
// returns the violations, any empty_match diagnostics, and the governed units.
func forbidImport(pol *policy.Policy, ru policy.Rule, res *analyze.Result) ([]facts.Violation, []facts.Diagnostic, map[string]bool) {
	all := sortedIDs(res.Units)
	match := func(boundary string, units []string) map[string]bool {
		m := map[string]bool{}
		for _, u := range units {
			for _, pat := range pol.Boundaries[boundary] {
				if glob.Match(pat, u) {
					m[u] = true
				}
			}
		}
		return m
	}
	scope := match(ru.Scope, res.Module)
	target := match(ru.Target, all)
	except := map[string]bool{}
	var empties []facts.Diagnostic
	empty := func(role, boundary, where string) {
		empties = append(empties, facts.NewDiagnostic("empty_match", "error", nil, nil,
			fmt.Sprintf("rule %q: %s boundary %q matches no %s; the rule cannot be evaluated.", ru.ID, role, boundary, where), nil))
	}
	if len(scope) == 0 {
		empty("scope", ru.Scope, "module package")
	}
	if len(target) == 0 {
		empty("target", ru.Target, "loaded package")
	}
	for _, b := range ru.ExceptFrom {
		m := match(b, all)
		if len(m) == 0 {
			empty("except_from", b, "loaded package")
		}
		for u := range m {
			except[u] = true
		}
	}
	if len(empties) > 0 {
		return nil, empties, scope
	}
	var vs []facts.Violation
	for _, u := range res.Module {
		if !scope[u] || except[u] || target[u] {
			continue
		}
		for _, im := range res.Units[u].Imports {
			if !target[im.ToUnit] {
				continue
			}
			v := facts.Violation{
				RuleID: ru.ID, RuleKind: ru.Kind, Location: im.Loc,
				Message:       fmt.Sprintf("%s (%s imports %s)", ru.Message, u, im.ToUnit),
				EvidenceChain: []facts.EvidenceStep{{Kind: "import", From: u, To: im.ToUnit, Loc: im.Loc}},
				Remediation:   facts.Remediation{Message: ru.Remediation.Message, Suggest: nonNil(ru.Remediation.Suggest)},
			}
			v.ID = facts.ViolID(v)
			vs = append(vs, v)
		}
	}
	return vs, nil, scope
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func dedupDiags(in []facts.ReportDiagnostic) []facts.ReportDiagnostic {
	out := []facts.ReportDiagnostic{}
	seen := map[string]bool{}
	for _, d := range in {
		if !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, d)
		}
	}
	return out
}

var rank = map[string]int{"global": 0, "indeterminate": 1, "warning": 2}

// finish computes rule and overall verdicts, sorts everything and fills the
// summary (verdict table, plan decision 7).
func finish(r *facts.Report) {
	sort.Slice(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		if rank[a.Relevance] != rank[b.Relevance] {
			return rank[a.Relevance] < rank[b.Relevance]
		}
		return a.ID < b.ID
	})
	// (rule_id, file, line, column, id); a null location sorts first.
	key := func(v facts.Violation) string {
		if v.Location == nil {
			return v.RuleID + "\x00\x00" + v.ID
		}
		return fmt.Sprintf("%s\x00\x01%s\x00%012d\x00%012d\x00%s", v.RuleID, v.Location.File, v.Location.Line, v.Location.Column, v.ID)
	}
	sort.Slice(r.Violations, func(i, j int) bool { return key(r.Violations[i]) < key(r.Violations[j]) })
	global, indet, warn := 0, 0, 0
	byRule := map[string][]string{}
	for _, d := range r.Diagnostics {
		switch d.Relevance {
		case "global":
			global++
		case "indeterminate":
			indet++
		default:
			warn++
		}
		for _, a := range d.AffectsRules {
			byRule[a.RuleID] = append(byRule[a.RuleID], d.ID)
		}
	}
	anyV := false
	for i := range r.Rules {
		ru := &r.Rules[i]
		sort.Strings(ru.ViolationIDs)
		ru.DiagnosticIDs = append([]string{}, byRule[ru.ID]...)
		sort.Strings(ru.DiagnosticIDs)
		switch {
		case len(ru.ViolationIDs) > 0:
			ru.Status, anyV = "VIOLATION", true
		case len(ru.DiagnosticIDs) > 0 || global > 0:
			ru.Status = "INDETERMINATE"
		default:
			ru.Status = "PASS"
		}
	}
	sort.Slice(r.Rules, func(i, j int) bool { return r.Rules[i].ID < r.Rules[j].ID })
	switch {
	case anyV:
		r.Status, r.ExitCode = "VIOLATION", 1
	case global > 0 || indet > 0:
		r.Status, r.ExitCode = "INDETERMINATE", 2
	default:
		r.Status, r.ExitCode = "PASS", 0
	}
	r.Summary.Violations = len(r.Violations)
	r.Summary.IndeterminateDiagnostics = indet
	r.Summary.GlobalDiagnostics = global
	r.Summary.WarningDiagnostics = warn
	r.Summary.Rules = len(r.Rules)
}

func sortedIDs(m map[string]*analyze.UnitInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
