package main

// Semantic checks for the rules in docs/superpowers/specs/schema/README.md
// that JSON Schema cannot express: ordering, referential integrity, ID and
// hash recomputation, and verdict consistency.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type obj = map[string]any

type checker struct{ errs []string }

func (c *checker) errf(format string, a ...any) { c.errs = append(c.errs, fmt.Sprintf(format, a...)) }

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case float64:
		return int(n)
	}
	return -1
}

// canon is RFC 8785-style canonical JSON for the value shapes used here
// (strings, integers, null, arrays, objects): sorted keys, no whitespace,
// no HTML escaping.
func canon(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func short(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16]
}

func locField(l any, k string) any {
	m, ok := l.(obj)
	if !ok {
		return nil
	}
	return m[k]
}

func diagID(d obj) string {
	l := d["loc"]
	t := []any{d["kind"], d["scope"], d["unit_id"], d["symbol_id"], d["site_id"],
		locField(l, "root"), locField(l, "module"), locField(l, "file"), locField(l, "line"), locField(l, "column"), d["message"]}
	return "diag:" + short(canon(t))
}

func violID(v obj) string {
	l := v["location"]
	var ev []any
	for _, e := range arr(v["evidence_chain"]) {
		e := e.(obj)
		ev = append(ev, []any{e["kind"], e["from"], e["to"]})
	}
	t := []any{v["rule_id"], locField(l, "file"), locField(l, "line"), locField(l, "column"), ev}
	return "viol:" + short(canon(t))
}

// sortedBy checks that items are strictly increasing under key.
func (c *checker) sortedBy(what string, items []any, key func(obj) string) {
	for i := 1; i < len(items); i++ {
		a, b := key(items[i-1].(obj)), key(items[i].(obj))
		if a >= b {
			c.errf("%s not strictly sorted at %d: %q >= %q", what, i, a, b)
		}
	}
}

// k builds a sortable composite key; ints are zero-padded, nulls sort first.
func k(parts ...any) string {
	var s []string
	for _, p := range parts {
		switch v := p.(type) {
		case nil:
			s = append(s, "\x00")
		case string:
			s = append(s, "\x01"+v)
		default:
			s = append(s, fmt.Sprintf("\x01%012d", num(v)))
		}
	}
	return strings.Join(s, "\x1f")
}

func checkShard(d obj) []string {
	c := &checker{}
	u := d["unit"].(obj)
	uid := str(u["id"])
	if in, known := inStdlib(shardGoVersion, uid); known && (u["origin"] == "stdlib") != in {
		c.errf("unit %s: origin %v contradicts the %s standard-library inventory (in inventory: %v)", uid, u["origin"], shardGoVersion, in)
	}
	switch u["source_status"] {
	case "builtin":
		if !builtinUnits[uid] {
			c.errf("unit %s: source_status builtin is only valid for %v", uid, keys(builtinUnits))
		}
	case "missing":
		hasErr := false
		for _, x := range arr(d["diagnostics"]) {
			if x.(obj)["severity"] == "error" {
				hasErr = true
			}
		}
		if !hasErr {
			c.errf("unit %s: source_status missing needs an error diagnostic explaining the load failure", uid)
		}
	}

	files := arr(u["files"])
	c.sortedBy("unit.files", files, func(o obj) string { return str(o["path"]) })
	var buf strings.Builder
	for _, f := range files {
		f := f.(obj)
		buf.WriteString(str(f["path"]) + "\x00" + strings.TrimPrefix(str(f["sha256"]), "sha256:") + "\n")
	}
	if got := sha([]byte(buf.String())); got != str(u["content_hash"]) {
		c.errf("unit.content_hash %s, recomputed %s", u["content_hash"], got)
	}
	deps := arr(u["dep_hashes"])
	c.sortedBy("unit.dep_hashes", deps, func(o obj) string { return str(o["unit_id"]) })

	syms := arr(d["symbols"])
	c.sortedBy("symbols", syms, func(o obj) string { return str(o["id"]) })
	known := map[string]string{}
	for _, s := range syms {
		s := s.(obj)
		if str(s["unit_id"]) != uid {
			c.errf("symbol %s owned by %s, not shard unit %s", s["id"], s["unit_id"], uid)
		}
		known[str(s["id"])] = uid
	}
	ext := arr(d["external_symbols"])
	c.sortedBy("external_symbols", ext, func(o obj) string { return str(o["id"]) })
	for _, e := range ext {
		e := e.(obj)
		if str(e["unit_id"]) == uid {
			c.errf("external symbol %s claims the shard's own unit", e["id"])
		}
		if _, dup := known[str(e["id"])]; dup {
			c.errf("symbol %s is both owned and external", e["id"])
		}
		known[str(e["id"])] = str(e["unit_id"])
	}
	used := map[string]bool{}
	ref := func(where string, v any) {
		if v == nil {
			return
		}
		id := str(v)
		used[id] = true
		if _, ok := known[id]; !ok {
			c.errf("%s references unknown symbol %s", where, id)
		}
	}
	for _, s := range syms {
		s := s.(obj)
		ref("symbol.parent_id", s["parent_id"])
		ref("symbol.origin_id", s["origin_id"])
	}

	imps := arr(d["imports"])
	c.sortedBy("imports", imps, func(o obj) string {
		l := o["loc"]
		return k(o["to_unit"], locField(l, "file"), locField(l, "line"), locField(l, "column"))
	})
	impSet, depSet := map[string]bool{}, map[string]bool{}
	for _, i := range imps {
		i := i.(obj)
		if str(i["from_unit"]) != uid {
			c.errf("import from_unit %s != %s", i["from_unit"], uid)
		}
		impSet[str(i["to_unit"])] = true
	}
	for _, dh := range deps {
		depSet[str(dh.(obj)["unit_id"])] = true
	}
	if fmt.Sprint(keys(impSet)) != fmt.Sprint(keys(depSet)) {
		c.errf("imports %v and dep_hashes %v differ", keys(impSet), keys(depSet))
	}

	sites := arr(d["call_sites"])
	c.sortedBy("call_sites", sites, func(o obj) string { return k(o["caller"], o["ordinal"]) })
	next := map[string]int{}
	for _, s := range sites {
		s := s.(obj)
		caller := str(s["caller"])
		if known[caller] != uid {
			c.errf("call site %s: caller not owned by this unit", s["id"])
		}
		if want := fmt.Sprintf("%s#%d", caller, num(s["ordinal"])); str(s["id"]) != want {
			c.errf("call site id %s, want %s", s["id"], want)
		}
		if num(s["ordinal"]) != next[caller] {
			c.errf("call site %s: ordinals for %s are not contiguous from 0", s["id"], caller)
		}
		next[caller]++
		ts := arr(s["targets"])
		for i, t := range ts {
			ref("call site target", t)
			if i > 0 && str(ts[i-1]) >= str(t) {
				c.errf("call site %s targets not sorted", s["id"])
			}
		}
		if s["mode"] == "static" {
			ref("call site callee_hint", s["callee_hint"])
			if len(ts) == 1 && str(ts[0]) != str(s["callee_hint"]) {
				c.errf("static call site %s: target != callee_hint", s["id"])
			}
		}
	}

	routes := arr(d["routes"])
	c.sortedBy("routes", routes, func(o obj) string { return str(o["id"]) })
	routeIDs := map[string]bool{}
	type rk struct{ m, p string }
	byKey := map[rk][]obj{}
	for _, r := range routes {
		r := r.(obj)
		m := "*"
		if r["method"] != nil {
			m = str(r["method"])
		}
		want := fmt.Sprintf("route:%s:%s:%s#%d", uid, m, str(r["path"]), num(r["duplicate_index"]))
		if str(r["id"]) != want {
			c.errf("route id %s, want %s", r["id"], want)
		}
		if str(r["unit_id"]) != uid {
			c.errf("route %s unit_id %s != %s", r["id"], r["unit_id"], uid)
		}
		routeIDs[str(r["id"])] = true
		ref("route.handler_sym", r["handler_sym"])
		ref("route.handler_value_sym", r["handler_value_sym"])
		ref("route.registered_in", r["registered_in"])
		byKey[rk{m, str(r["path"])}] = append(byKey[rk{m, str(r["path"])}], r)
	}
	for key, rs := range byKey {
		sort.Slice(rs, func(i, j int) bool {
			li, lj := rs[i]["loc"], rs[j]["loc"]
			return k(locField(li, "file"), locField(li, "line"), locField(li, "column")) <
				k(locField(lj, "file"), locField(lj, "line"), locField(lj, "column"))
		})
		for i, r := range rs {
			if num(r["duplicate_index"]) != i {
				c.errf("route %v: duplicate_index %d, want %d by location order", key, num(r["duplicate_index"]), i)
			}
		}
	}
	mws := arr(d["route_middleware"])
	c.sortedBy("route_middleware", mws, func(o obj) string { return k(o["route_id"], o["order"]) })
	for _, m := range mws {
		m := m.(obj)
		if !routeIDs[str(m["route_id"])] {
			c.errf("route_middleware references unknown route %s", m["route_id"])
		}
		ref("route_middleware.middleware_sym", m["middleware_sym"])
	}

	diags := arr(d["diagnostics"])
	c.sortedBy("diagnostics", diags, func(o obj) string { return str(o["id"]) })
	for _, x := range diags {
		x := x.(obj)
		if want := diagID(x); str(x["id"]) != want {
			c.errf("diagnostic id %s, recomputed %s", x["id"], want)
		}
		if str(x["unit_id"]) != uid {
			c.errf("diagnostic %s unit_id %s != %s", x["id"], x["unit_id"], uid)
		}
		ref("diagnostic.symbol_id", x["symbol_id"])
	}
	for _, e := range ext {
		if !used[str(e.(obj)["id"])] {
			c.errf("external symbol %s is never referenced", e.(obj)["id"])
		}
	}
	return c.errs
}

func checkManifest(d obj) []string {
	c := &checker{}
	if got := sha(canon(d["build_profile"])); got != str(d["build_profile_digest"]) {
		c.errf("build_profile_digest %s, recomputed %s", d["build_profile_digest"], got)
	}
	c.sortedBy("units", arr(d["units"]), func(o obj) string { return str(o["id"]) })
	c.sortedBy("excluded_packages", arr(d["excluded_packages"]), func(o obj) string { return str(o["id"]) })
	gd := arr(d["global_diagnostics"])
	c.sortedBy("global_diagnostics", gd, func(o obj) string { return str(o["id"]) })
	for _, x := range gd {
		if want := diagID(x.(obj)); str(x.(obj)["id"]) != want {
			c.errf("diagnostic id %s, recomputed %s", x.(obj)["id"], want)
		}
	}
	if d["status"] == "complete" {
		for _, x := range gd {
			if x.(obj)["severity"] == "error" {
				c.errf("status complete with global error diagnostic %s", x.(obj)["id"])
			}
		}
	}
	return c.errs
}

// shardGoVersion is the toolchain shards are checked against. A shard does not
// carry its build profile, only its digest; the POC pins one toolchain.
const shardGoVersion = "go1.26.11"

// builtinUnits are the source-less packages the compiler provides (cgo is
// disabled by the pinned profile, so "C" never appears).
var builtinUnits = map[string]bool{"unsafe": true}

// runLevelKinds must have relevance global: they invalidate the whole run.
var runLevelKinds = map[string]bool{
	"policy_missing": true, "policy_invalid": true, "policy_no_rules": true,
	"analyzer_crash": true, "analyzer_timeout": true, "analyzer_memory_limit": true,
	"symbol_id_collision": true, "unresolved_external_symbol": true, "entry_mismatch": true,
}

// policyFailureKinds stop evaluation: the report must carry no rules.
var policyFailureKinds = map[string]bool{"policy_missing": true, "policy_invalid": true, "policy_no_rules": true}

var unknownCallKinds = map[string]bool{
	"unresolved_interface_call": true, "unresolved_dynamic_call": true,
	"bodyless_function": true, "reflection_call": true, "unsafe_pointer": true,
}

// stdlibInventory maps a Go version to its standard-library package set,
// loaded from stdlib-<version>.txt next to the schemas (go list std under the
// pinned profile). Standard-library provenance is never inferred from the
// shape of an import path: Go allows dotless module paths such as "app".
var stdlibInventory = map[string]map[string]bool{}

func inStdlib(goVersion, unit string) (in, known bool) {
	inv, ok := stdlibInventory[goVersion]
	if !ok {
		return false, false
	}
	return inv[unit], true
}

var relevanceRank = map[string]string{"global": "0", "indeterminate": "1", "warning": "2"}

func checkReport(d obj) []string {
	c := &checker{}
	rules := arr(d["rules"])
	c.sortedBy("rules", rules, func(o obj) string { return str(o["id"]) })
	viols := arr(d["violations"])
	c.sortedBy("violations", viols, func(o obj) string {
		l := o["location"]
		return k(o["rule_id"], locField(l, "file"), locField(l, "line"), locField(l, "column"), o["id"])
	})
	vByRule := map[string][]string{}
	for _, v := range viols {
		v := v.(obj)
		if want := violID(v); str(v["id"]) != want {
			c.errf("violation id %s, recomputed %s", v["id"], want)
		}
		vByRule[str(v["rule_id"])] = append(vByRule[str(v["rule_id"])], str(v["id"]))
	}
	diags := arr(d["diagnostics"])
	c.sortedBy("diagnostics", diags, func(o obj) string {
		return relevanceRank[str(o["relevance"])] + str(o["id"])
	})
	goVersion := str(d["build_profile"].(obj)["go_version"])
	dByRule := map[string][]string{}
	indet, global, warn := 0, 0, 0
	policyFailed := false
	exempted := map[string]int{}
	for _, x := range diags {
		x := x.(obj)
		id, kind, unit := str(x["id"]), str(x["kind"]), str(x["unit_id"])
		if want := diagID(x); id != want {
			c.errf("diagnostic id %s, recomputed %s", id, want)
		}
		ar := arr(x["affects_rules"])
		c.sortedBy("affects_rules of "+id, ar, func(o obj) string { return str(o["rule_id"]) })
		for _, a := range ar {
			dByRule[str(a.(obj)["rule_id"])] = append(dByRule[str(a.(obj)["rule_id"])], id)
		}
		switch x["relevance"] {
		case "global":
			global++
		case "indeterminate":
			indet++
		default:
			warn++
		}
		if runLevelKinds[kind] && x["relevance"] != "global" {
			c.errf("diagnostic %s: kind %s is run-level and must have relevance global", id, kind)
		}
		if policyFailureKinds[kind] {
			policyFailed = true
		}
		if kind == "rule_not_evaluated" {
			ar := arr(x["affects_rules"])
			if x["scope"] != "global" || x["relevance"] != "indeterminate" || len(ar) != 1 ||
				ar[0].(obj)["reason"] != "not_evaluated" {
				c.errf("diagnostic %s: rule_not_evaluated must be global, indeterminate, and affect exactly one rule with reason not_evaluated", id)
			}
		} else {
			for _, a := range arr(x["affects_rules"]) {
				if a.(obj)["reason"] == "not_evaluated" {
					c.errf("diagnostic %s: reason not_evaluated is only valid on rule_not_evaluated", id)
				}
			}
		}
		// S1: exemption attribution must be verifiable from the diagnostic's
		// provenance (unit_origin, from the loader) cross-checked against the
		// pinned standard-library inventory.
		origin, hasOrigin := x["unit_origin"].(string)
		if hasOrigin && unit != "" {
			in, known := inStdlib(goVersion, unit)
			switch {
			case !known && origin == "stdlib":
				c.errf("diagnostic %s: unit_origin stdlib but no standard-library inventory for %s", id, goVersion)
			case known && (origin == "stdlib") != in:
				c.errf("diagnostic %s: unit_origin %s for %q contradicts the %s standard-library inventory (in inventory: %v)", id, origin, unit, goVersion, in)
			}
		}
		switch by := str(x["exempted_by"]); by {
		case "":
		case "trusted_stdlib":
			exempted[by]++
			in, _ := inStdlib(goVersion, unit)
			if !unknownCallKinds[kind] || origin != "stdlib" || !in {
				c.errf("diagnostic %s: trusted_stdlib exemption needs an unknown-call kind with unit_origin stdlib and a unit in the %s standard-library inventory (got %s, unit_origin %q, unit %q)", id, goVersion, kind, origin, unit)
			}
		case "no_possible_target":
			exempted[by]++
			if (kind != "unresolved_interface_call" && kind != "unresolved_dynamic_call") || x["site_id"] == nil ||
				(origin != "module" && origin != "dependency") {
				c.errf("diagnostic %s: no_possible_target exemption needs a zero-target unresolved call site with unit_origin module or dependency (got %s, unit_origin %q)", id, kind, origin)
			}
		}
	}

	// B1: a failed policy means no rule was evaluated.
	if policyFailed && (len(rules) > 0 || len(viols) > 0) {
		c.errf("policy failure diagnostic present but the report has %d rules and %d violations; want none", len(rules), len(viols))
	}

	// S1: both assumptions are listed whenever the DB rule kind is evaluated.
	asm := arr(d["assumptions"])
	c.sortedBy("assumptions", asm, func(o obj) string { return str(o["id"]) })
	// A restrict_concept_to_boundary rule is evaluated when it is listed, no
	// rule_not_evaluated diagnostic names it, and the run did not fail
	// globally (abort, merge integrity): only then do assumptions apply.
	notEvaluated := map[string]bool{}
	for _, x := range diags {
		if x.(obj)["kind"] == "rule_not_evaluated" {
			for _, a := range arr(x.(obj)["affects_rules"]) {
				notEvaluated[str(a.(obj)["rule_id"])] = true
			}
		}
	}
	evaluatesDB := false
	for _, r := range rules {
		if r.(obj)["kind"] == "restrict_concept_to_boundary" && !notEvaluated[str(r.(obj)["id"])] && global == 0 {
			evaluatesDB = true
		}
	}
	gotAsm := map[string]int{}
	for _, a := range asm {
		gotAsm[str(a.(obj)["id"])] = num(a.(obj)["exempted_count"])
	}
	if evaluatesDB {
		for _, id := range []string{"no_possible_target", "trusted_stdlib"} {
			if _, ok := gotAsm[id]; !ok {
				c.errf("restrict_concept_to_boundary is evaluated but assumption %s is not listed", id)
			}
		}
	} else if len(asm) > 0 {
		c.errf("assumptions listed but no restrict_concept_to_boundary rule was evaluated")
	}
	for id, n := range gotAsm {
		if exempted[id] != n {
			c.errf("assumption %s exempted_count %d, but %d diagnostics carry exempted_by %s", id, n, exempted[id], id)
		}
	}
	for id, n := range exempted {
		if _, ok := gotAsm[id]; !ok {
			c.errf("%d diagnostics exempted by %s, which is not listed in assumptions", n, id)
		}
	}

	ruleIDs := map[string]bool{}
	anyV := false
	for _, r := range rules {
		r := r.(obj)
		id := str(r["id"])
		ruleIDs[id] = true
		vs, ds := vByRule[id], dByRule[id]
		sort.Strings(vs)
		sort.Strings(ds)
		if fmt.Sprint(strs(arr(r["violation_ids"]))) != fmt.Sprint(vs) {
			c.errf("rule %s violation_ids %v, violations say %v", id, r["violation_ids"], vs)
		}
		want := "PASS"
		switch {
		case len(vs) > 0:
			want, anyV = "VIOLATION", true
		case len(ds) > 0 || global > 0:
			want = "INDETERMINATE"
		}
		if fmt.Sprint(strs(arr(r["diagnostic_ids"]))) != fmt.Sprint(ds) {
			c.errf("rule %s diagnostic_ids %v, diagnostics say %v", id, r["diagnostic_ids"], ds)
		}
		if r["status"] != want {
			c.errf("rule %s status %v, want %s", id, r["status"], want)
		}
	}
	for id := range vByRule {
		if !ruleIDs[id] {
			c.errf("violation for unknown rule %s", id)
		}
	}
	for id := range dByRule {
		if !ruleIDs[id] {
			c.errf("diagnostic affects unknown rule %s", id)
		}
	}
	want := "PASS"
	switch {
	case anyV:
		want = "VIOLATION"
	case indet > 0 || global > 0:
		want = "INDETERMINATE"
	}
	if d["status"] != want {
		c.errf("status %v, want %s", d["status"], want)
	}
	s := d["summary"].(obj)
	if num(s["violations"]) != len(viols) || num(s["indeterminate_diagnostics"]) != indet ||
		num(s["global_diagnostics"]) != global || num(s["warning_diagnostics"]) != warn ||
		num(s["rules"]) != len(rules) {
		c.errf("summary counts do not match the report body")
	}
	inc := strs(arr(d["packages"].(obj)["included"]))
	if !sort.StringsAreSorted(inc) {
		c.errf("packages.included not sorted")
	}
	return c.errs
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func strs(a []any) []string {
	out := []string{}
	for _, x := range a {
		out = append(out, str(x))
	}
	return out
}

func semantic(doc any) []string {
	d, ok := doc.(obj)
	if !ok {
		return []string{"document is not an object"}
	}
	switch d["kind"] {
	case "unit_shard":
		return checkShard(d)
	case "analysis_manifest":
		return checkManifest(d)
	case "report":
		return checkReport(d)
	}
	return []string{fmt.Sprintf("unknown kind %v", d["kind"])}
}
