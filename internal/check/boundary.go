package check

// restrict_concept_to_boundary (policy contract 6.3, plan decision 4 and
// section 7). The evaluator reads only shards and the manifest: it merges
// them into one call graph, recomputes Entry, and checks every governed call
// site with reachable_from.

import (
	"fmt"
	"sort"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/glob"
	"github.com/LavishGent/bedrock/internal/policy"
)

// unknownCallKinds are the diagnostics plan section 7 calls unknown calls.
var unknownCallKinds = map[string]bool{
	"unresolved_interface_call": true, "unresolved_dynamic_call": true,
	"bodyless_function": true, "reflection_call": true, "unsafe_pointer": true,
}

// Assumption statements (policy contract section 7, owner decision A).
const (
	stmtNoPossibleTarget = "A non-static call with no possible target in the analyzed universe (VTA over the closed whole program, cgo disabled) is not an unknown call."
	stmtTrustedStdlib    = "Unknown calls owned by the pinned " + analyze.PinnedGoVersion + " standard library are not counted (owner decision A, plan section 7; policy contract section 7)."
)

type edge struct {
	to   string
	site *facts.CallSite
}

// graph is the call graph merged from all shards.
type graph struct {
	sym   map[string]*facts.Symbol
	unit  map[string]string // symbol ID -> owner unit
	succ  map[string][]edge // caller -> edges in (ordinal, target) order
	entry []string          // recomputed Entry, sorted
	sites []*facts.CallSite // every call site, by (caller, ordinal)
}

// mergeGraph merges the shards and runs the merge checks of the schema
// README (Shard ownership and referential integrity). Failures are global
// errors.
func mergeGraph(shards []*facts.Shard, m *facts.Manifest) (*graph, []facts.Diagnostic) {
	g := &graph{sym: map[string]*facts.Symbol{}, unit: map[string]string{}, succ: map[string][]edge{}}
	var errs []facts.Diagnostic
	fail := func(kind, msg string) {
		errs = append(errs, facts.NewDiagnostic(kind, "error", nil, nil, msg, nil))
	}
	for _, sh := range shards {
		for i := range sh.Symbols {
			s := &sh.Symbols[i]
			if prev, dup := g.unit[s.ID]; dup {
				fail("symbol_id_collision", fmt.Sprintf("symbol %s is defined by both %s and %s", s.ID, prev, sh.Unit.ID))
				continue
			}
			g.sym[s.ID], g.unit[s.ID] = s, sh.Unit.ID
		}
	}
	for _, sh := range shards {
		for _, e := range sh.ExternalSymbols {
			if g.unit[e.ID] != e.UnitID {
				fail("unresolved_external_symbol", fmt.Sprintf("shard %s refers to %s in unit %s, which does not define it", sh.Unit.ID, e.ID, e.UnitID))
			}
		}
		for i := range sh.CallSites {
			c := &sh.CallSites[i]
			g.sites = append(g.sites, c)
			for _, t := range c.Targets {
				g.succ[c.Caller] = append(g.succ[c.Caller], edge{to: t, site: c})
			}
		}
	}
	g.entry, _, _ = analyze.EntrySet(shards)
	if got := facts.EntryDigestOf(g.entry); m == nil || got != m.Entry {
		fail("entry_mismatch", fmt.Sprintf("Entry recovered from shards (%d symbols, %s) does not match the manifest", got.Count, got.Digest))
	}
	return g, errs
}

// reach is a multi-source BFS over g from sources, never entering a symbol
// for which blocked returns true. parent records the first edge into each
// reached symbol, so a witness path can be rebuilt deterministically.
type reach struct {
	from   map[string]string // symbol -> BFS source (Entry) it was reached from
	parent map[string]edge   // symbol -> edge into it (site.Caller is the predecessor)
}

func (g *graph) bfs(sources []string, blocked func(string) bool) *reach {
	r := &reach{from: map[string]string{}, parent: map[string]edge{}}
	var queue []string
	for _, s := range sources {
		if blocked(s) {
			continue
		}
		if _, ok := r.from[s]; !ok {
			r.from[s] = s
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, e := range g.succ[n] {
			if blocked(e.to) {
				continue
			}
			if _, ok := r.from[e.to]; !ok {
				r.from[e.to] = r.from[n]
				r.parent[e.to] = e
				queue = append(queue, e.to)
			}
		}
	}
	return r
}

// path returns the call steps from the BFS source to n.
func (r *reach) path(n string) []facts.EvidenceStep {
	var steps []facts.EvidenceStep
	for {
		e, ok := r.parent[n]
		if !ok {
			break
		}
		steps = append(steps, facts.EvidenceStep{Kind: "call", From: e.site.Caller, To: n, Loc: e.site.Loc})
		n = e.site.Caller
	}
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
	return steps
}

// restrictResult is one evaluated restrict_concept_to_boundary rule.
type restrictResult struct {
	violations []facts.Violation
	empties    []facts.Diagnostic // empty_match, rule-specific
	affects    map[string]string  // unit diagnostic ID -> reason
}

// restrictConcept evaluates one rule. g is nil when call facts are
// incomplete; then every load failure affects the rule and nothing passes.
func restrictConcept(pol *policy.Policy, ru policy.Rule, res *analyze.Result, g *graph, shards []*facts.Shard) restrictResult {
	out := restrictResult{affects: map[string]string{}}
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
	inB := match(ru.Boundary, sortedIDs(res.Units))
	empty := func(what string) {
		out.empties = append(out.empties, facts.NewDiagnostic("empty_match", "error", nil, nil,
			fmt.Sprintf("rule %q: %s; the rule cannot be evaluated.", ru.ID, what), nil))
	}
	if len(scope) == 0 {
		empty(fmt.Sprintf("scope boundary %q matches no module package", ru.Scope))
	}
	if len(inB) == 0 {
		empty(fmt.Sprintf("boundary %q matches no loaded package", ru.Boundary))
	}
	if g == nil {
		// No call facts: every load failure leaves the graph incomplete.
		for _, sh := range shards {
			for _, d := range sh.Diagnostics {
				if loadErrorKinds[d.Kind] {
					out.affects[d.ID] = reasonFor(scope, sh.Unit.ID, "reachable_from_governed_entry")
				}
			}
		}
		return out
	}
	// Concept set restricted to universe functions, then its wrapper closure
	// (policy contract 5): wrappers and instances whose origin is bound.
	bound := map[string]bool{}
	for _, id := range pol.Bindings(ru.Concept) {
		if s := g.sym[id]; s != nil && s.InUniverse && entryKindsEval[s.Kind] {
			bound[id] = true
		}
	}
	if len(bound) == 0 {
		empty(fmt.Sprintf("concept %q binds no function of the analysis universe", ru.Concept))
	}
	if len(out.empties) > 0 {
		return out
	}
	closure := map[string]string{} // bound or wrapper-of-bound -> the bound symbol
	for id := range bound {
		closure[id] = id
	}
	for changed := true; changed; {
		changed = false
		for id, s := range g.sym {
			if _, ok := closure[id]; ok || s.OriginID == nil || (s.Kind != "wrapper" && s.Kind != "instance") {
				continue
			}
			if b, ok := closure[*s.OriginID]; ok {
				closure[id] = b
				changed = true
			}
		}
	}
	inBSym := func(id string) bool { return inB[g.unit[id]] }

	all := g.bfs(g.entry, func(string) bool { return false })
	noB := g.bfs(g.entry, inBSym)

	for _, c := range g.sites {
		if !scope[g.unit[c.Caller]] {
			continue
		}
		if forwardsToBound(g, closure, c) {
			continue // reported at the call into the wrapper
		}
		target := ""
		for _, t := range c.Targets {
			if _, ok := closure[t]; ok {
				target = t
				break
			}
		}
		if target == "" || inBSym(c.Caller) {
			continue
		}
		var chain []facts.EvidenceStep
		var why string
		if e, ok := noB.from[c.Caller]; ok {
			chain = append([]facts.EvidenceStep{{Kind: "entry", From: "Entry", To: e, Loc: g.symLoc(e)}}, noB.path(c.Caller)...)
			why = fmt.Sprintf("Entry %s reaches it without passing through boundary %q", e, ru.Boundary)
		} else if _, ok := all.from[c.Caller]; ok {
			continue // every Entry path passes through B
		} else {
			why = "it is not reachable from any Entry, so the boundary cannot guard it"
		}
		chain = append(chain,
			facts.EvidenceStep{Kind: "call", From: c.Caller, To: target, Loc: c.Loc},
			facts.EvidenceStep{Kind: "concept_binding", From: target, To: closure[target] + " (" + ru.Concept + ")", Loc: nil})
		v := facts.Violation{
			RuleID: ru.ID, RuleKind: ru.Kind, Location: c.Loc,
			Message: fmt.Sprintf("%s (%s calls %s, bound by concept %s, outside boundary %q; %s)",
				ru.Message, c.Caller, target, ru.Concept, ru.Boundary, why),
			EvidenceChain: chain,
			Remediation:   facts.Remediation{Message: ru.Remediation.Message, Suggest: nonNil(ru.Remediation.Suggest)},
		}
		v.ID = facts.ViolID(v)
		out.violations = append(out.violations, v)
	}

	// Unknown-call relevance (plan section 7, owner decision A). Exempted
	// diagnostics are attributed by the caller; here a non-exempt unknown
	// call is relevant if it sits in a governed unit or is reachable from a
	// governed Entry.
	var governed []string
	for _, e := range g.entry {
		if scope[g.unit[e]] {
			governed = append(governed, e)
		}
	}
	gov := g.bfs(governed, func(string) bool { return false })
	for _, sh := range shards {
		for _, d := range sh.Diagnostics {
			if !unknownCallKinds[d.Kind] || exemption(d, sh.Unit.Origin) != "" {
				continue
			}
			if scope[sh.Unit.ID] {
				out.affects[d.ID] = "unit_in_scope"
			} else if d.SymbolID != nil {
				if _, ok := gov.from[*d.SymbolID]; ok {
					out.affects[d.ID] = "reachable_from_governed_entry"
				}
			}
		}
	}
	return out
}

// forwardsToBound reports whether c is the forwarding call of a synthetic
// wrapper whose origin is bound (policy contract 5: such a call is reported
// at the call into the wrapper). Ordinary bound functions and instantiated
// bodies are real code: their calls are always checked.
func forwardsToBound(g *graph, closure map[string]string, c *facts.CallSite) bool {
	s := g.sym[c.Caller]
	if s == nil || s.Kind != "wrapper" || s.OriginID == nil {
		return false
	}
	if _, ok := closure[c.Caller]; !ok {
		return false
	}
	for _, t := range c.Targets {
		if t == *s.OriginID {
			return true
		}
	}
	return false
}

func reasonFor(scope map[string]bool, unit, otherwise string) string {
	if scope[unit] {
		return "unit_in_scope"
	}
	return otherwise
}

var entryKindsEval = map[string]bool{"func": true, "method": true, "closure": true, "instance": true, "wrapper": true}

func (g *graph) symLoc(id string) *facts.Location {
	if s := g.sym[id]; s != nil {
		return s.Loc
	}
	return nil
}

// exemption returns the decision A criterion that exempts an unknown-call
// diagnostic, or "" (policy contract section 7): any unknown call owned by
// a standard-library unit is trusted_stdlib; a zero-target unresolved site in
// module or dependency code is no_possible_target. The analyzer emits
// unresolved_* diagnostics only for zero-target sites.
func exemption(d facts.Diagnostic, origin string) string {
	if !unknownCallKinds[d.Kind] {
		return ""
	}
	switch {
	case origin == "stdlib":
		return "trusted_stdlib"
	case (d.Kind == "unresolved_interface_call" || d.Kind == "unresolved_dynamic_call") && d.SiteID != nil &&
		(origin == "module" || origin == "dependency"):
		return "no_possible_target"
	}
	return ""
}

func assumptions(diags []facts.ReportDiagnostic) []facts.Assumption {
	n := map[string]int{}
	for _, d := range diags {
		if d.ExemptedBy != nil {
			n[*d.ExemptedBy]++
		}
	}
	out := []facts.Assumption{
		{ID: "no_possible_target", Statement: stmtNoPossibleTarget, ExemptedCount: n["no_possible_target"]},
		{ID: "trusted_stdlib", Statement: stmtTrustedStdlib, ExemptedCount: n["trusted_stdlib"]},
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
