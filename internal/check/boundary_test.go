package check

import (
	"strings"
	"testing"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/policy"
)

const sqlExec = "(*database/sql.DB).Exec"

// graphShards builds one shard per unit from symbols "unit.F" and edges
// "caller->callee"; the SQL method lives in database/sql.
func graphShards(edges ...string) ([]*facts.Shard, map[string]*analyze.UnitInfo) {
	units := map[string]*analyze.UnitInfo{}
	shard := func(id, origin string) *facts.Shard {
		if u := units[id]; u != nil {
			return u.Shard
		}
		sh := &facts.Shard{Unit: facts.Unit{ID: id, Origin: origin}}
		units[id] = &analyze.UnitInfo{Origin: origin, Shard: sh}
		return sh
	}
	unitOf := func(sym string) string {
		if sym == sqlExec {
			return "database/sql"
		}
		return sym[:strings.LastIndex(sym, ".")]
	}
	declared := map[string]bool{}
	declare := func(sym string) {
		if declared[sym] {
			return
		}
		declared[sym] = true
		origin := "module"
		if sym == sqlExec {
			origin = "stdlib"
		}
		sh := shard(unitOf(sym), origin)
		sh.Symbols = append(sh.Symbols, facts.Symbol{ID: sym, UnitID: unitOf(sym), Kind: "func", HasBody: true, InUniverse: true})
	}
	ordinal := map[string]int{}
	for _, e := range edges {
		from, to, _ := strings.Cut(e, "->")
		declare(from)
		declare(to)
		sh := shard(unitOf(from), "module")
		sh.CallSites = append(sh.CallSites, facts.CallSite{ID: from + "#", Caller: from, Ordinal: ordinal[from],
			Mode: "static", CalleeHint: facts.Str(to), Targets: []string{to}, Resolution: "static",
			Loc: &facts.Location{Root: "repo", File: from + ".go", Line: 1 + ordinal[from], Column: 1}})
		ordinal[from]++
	}
	var shards []*facts.Shard
	for _, id := range sortedIDs(units) {
		shards = append(shards, units[id].Shard)
	}
	return shards, units
}

func evalGraph(t *testing.T, edges ...string) []facts.Violation {
	t.Helper()
	shards, units := graphShards(edges...)
	entry, _, _ := analyze.EntrySet(shards)
	m := &facts.Manifest{Entry: facts.EntryDigestOf(entry)}
	g, errs := mergeGraph(shards, m)
	if len(errs) > 0 {
		t.Fatalf("merge errors: %+v", errs)
	}
	res := &analyze.Result{Units: units}
	for id, u := range units {
		if u.Origin == "module" {
			res.Module = append(res.Module, id)
		}
	}
	p := &policy.Policy{
		Boundaries: map[string][]string{"all": {"m/**"}, "repo": {"m/b"}},
		Concepts:   map[string]policy.Concept{"database.query": {Packs: []string{"go-stdlib-sql"}}},
	}
	ru := policy.Rule{ID: "r", Kind: policy.RestrictConceptToBoundary, Scope: "all", Concept: "database.query", Boundary: "repo", Message: "m"}
	out := restrictConcept(p, ru, res, g, shards)
	if len(out.empties) > 0 {
		t.Fatalf("empty match: %+v", out.empties)
	}
	return out.violations
}

// Plan decision 4 detail, Codex's counterexample: B={b}, b->c, u->c,
// c->sql. Entry={b,u}, so c fails. Adding b->u makes Entry={b} and c passes.
func TestReachableFromCounterexample(t *testing.T) {
	vs := evalGraph(t, "m/b.B->m/c.C", "m/u.U->m/c.C", "m/c.C->"+sqlExec)
	if len(vs) != 1 || vs[0].Location.File != "m/c.C.go" {
		t.Fatalf("violations %+v, want one at c's call", vs)
	}
	if e := vs[0].EvidenceChain[0]; e.Kind != "entry" || e.To != "m/u.U" {
		t.Fatalf("evidence starts %+v, want Entry m/u.U", e)
	}
	if vs := evalGraph(t, "m/b.B->m/c.C", "m/u.U->m/c.C", "m/c.C->"+sqlExec, "m/b.B->m/u.U"); len(vs) != 0 {
		t.Fatalf("with b->u: violations %+v, want none", vs)
	}
}

// A caller in B passes; a caller unreachable from every Entry fails.
func TestReachableFromMembershipAndUnreachable(t *testing.T) {
	if vs := evalGraph(t, "m/b.B->"+sqlExec); len(vs) != 0 {
		t.Fatalf("caller in B: %+v", vs)
	}
	vs := evalGraph(t, "m/b.B->m/b.H", "m/x.X->m/y.Y", "m/y.Y->m/x.X", "m/x.X->"+sqlExec)
	if len(vs) != 1 || !strings.Contains(vs[0].Message, "not reachable from any Entry") {
		t.Fatalf("unreachable cycle: %+v, want one violation", vs)
	}
}

func TestMergeChecks(t *testing.T) {
	shards, _ := graphShards("m/a.A->" + sqlExec)
	entry, _, _ := analyze.EntrySet(shards)
	kinds := func(errs []facts.Diagnostic) string {
		var k []string
		for _, d := range errs {
			k = append(k, d.Kind)
		}
		return strings.Join(k, ",")
	}
	if _, errs := mergeGraph(shards, &facts.Manifest{Entry: facts.EntryDigestOf(append(entry, "m/z.Z"))}); kinds(errs) != "entry_mismatch" {
		t.Fatalf("entry mismatch not detected: %v", kinds(errs))
	}
	good := &facts.Manifest{Entry: facts.EntryDigestOf(entry)}
	shards[0].ExternalSymbols = []facts.ExtSymbol{{ID: "m/q.Q", UnitID: "m/q"}}
	if _, errs := mergeGraph(shards, good); kinds(errs) != "unresolved_external_symbol" {
		t.Fatalf("unresolved external not detected: %v", kinds(errs))
	}
	shards[0].ExternalSymbols = nil
	shards[1].Symbols = append(shards[1].Symbols, shards[0].Symbols[0])
	if _, errs := mergeGraph(shards, good); !strings.HasPrefix(kinds(errs), "symbol_id_collision") {
		t.Fatalf("collision not detected: %v", kinds(errs))
	}
}
