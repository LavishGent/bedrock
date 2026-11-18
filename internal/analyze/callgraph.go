package analyze

// Call facts (plan step 4, decision 4): SSA for the whole pinned analysis
// universe with generics instantiated, CHA seeding VTA, emitted as call
// sites, universe symbols and unknown-call diagnostics in the owning shards
// (schema README: Symbol IDs, Call-site IDs, Analysis universe, Recovering
// Entry). The analyzer states facts; relevance is decided by the evaluator.

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/LavishGent/bedrock/internal/facts"
)

// CallStats summarizes call-fact extraction for the analysis manifest.
type CallStats struct {
	Complete      bool     // call facts cover the whole universe
	Status        string   // manifest status: complete, load_error or crash
	FunctionCount int      // in-universe function symbols
	BodylessCount int      // of which bodyless
	Entry         []string // the analyzer's Entry derivation, sorted
}

// reflectCalls are the reflection entry points that call or create code the
// analysis cannot see (policy contract section 7, reflection_call).
var reflectCalls = map[string]bool{
	"(reflect.Value).Call": true, "(reflect.Value).CallSlice": true,
	"(reflect.Value).Method": true, "(reflect.Value).MethodByName": true,
	"reflect.MakeFunc": true,
}

// entryKinds are the symbol kinds that can be Entry members.
var entryKinds = map[string]bool{"func": true, "method": true, "closure": true, "instance": true, "wrapper": true}

// callFacts builds the call graph and attaches its facts to the shards. It
// must run after every other symbol source (types, routes) and before
// normalizeShards.
func (res *Result) callFacts(all []*packages.Package, l *locator) {
	res.Calls.Status = "load_error"
	if res.Fatal {
		return
	}
	for _, p := range all {
		if p.Types == nil || p.IllTyped || len(p.Errors) > 0 {
			return
		}
	}
	for _, u := range res.Units {
		for _, d := range u.Shard.Diagnostics {
			if d.Kind == "package_load_error" || d.Kind == "parse_or_type_error" {
				return
			}
		}
	}
	// Only analyzed packages: a directory the loader selected no files from
	// is an exclusion, not a unit, and contributes no functions.
	pkgByPath := map[string]*packages.Package{}
	var roots []*packages.Package
	for _, p := range all {
		if _, ok := res.Units[p.PkgPath]; ok {
			pkgByPath[p.PkgPath] = p
			roots = append(roots, p)
		}
	}

	prog, _ := ssautil.AllPackages(roots, ssa.InstantiateGenerics)
	prog.Build()
	chaCG := cha.CallGraph(prog)
	uni := ssautil.AllFunctions(prog)
	vtaCG := vta.CallGraph(uni, chaCG)

	siteT := map[ssa.CallInstruction][]*ssa.Function{}
	for _, n := range vtaCG.Nodes {
		for _, e := range n.Out {
			siteT[e.Site] = append(siteT[e.Site], e.Callee.Func)
		}
	}
	targetsOf := func(ci ssa.CallInstruction) []*ssa.Function {
		if callee := ci.Common().StaticCallee(); callee != nil {
			return []*ssa.Function{callee}
		}
		return siteT[ci]
	}

	// The universe is closed under call targets; a function reached only
	// through an edge but not listed by AllFunctions would have no VTA
	// sites of its own, so it makes the facts incomplete.
	for fn := range uni {
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if ci, ok := ins.(ssa.CallInstruction); ok {
					for _, t := range targetsOf(ci) {
						if !uni[t] {
							res.Global = append(res.Global, globalDiag("analyzer_crash", "error",
								"call target "+t.String()+" is outside the analysis universe (ssautil.AllFunctions); call facts are incomplete"))
							res.Calls.Status = "crash"
							return
						}
					}
				}
			}
		}
	}

	c := &callBuilder{res: res, l: l, pkgByPath: pkgByPath, uni: uni, ids: newIDMaker(uni), taken: map[*ssa.Function]bool{}}
	c.build(targetsOf)
	if c.failed {
		res.Calls.Status = "crash"
		return
	}
	res.Calls.Complete = true
	res.Calls.Status = "complete"
	res.Calls.Entry, res.Calls.FunctionCount, res.Calls.BodylessCount = EntrySet(res.shards())
}

func (res *Result) shards() []*facts.Shard {
	var out []*facts.Shard
	for _, id := range sortedUnitIDs(res.Units) {
		out = append(out, res.Units[id].Shard)
	}
	return out
}

// EntrySet derives policy Entry from shards alone (schema README,
// Recovering Entry): in-universe function symbols with no incoming call edge
// (self-edges count), plus every route handler. It also returns the universe
// function count and the bodyless count.
func EntrySet(shards []*facts.Shard) (entry []string, functions, bodyless int) {
	incoming := map[string]bool{}
	for _, sh := range shards {
		for _, c := range sh.CallSites {
			for _, t := range c.Targets {
				incoming[t] = true
			}
		}
	}
	set := map[string]bool{}
	for _, sh := range shards {
		for _, s := range sh.Symbols {
			if !s.InUniverse || !entryKinds[s.Kind] {
				continue
			}
			functions++
			if !s.HasBody {
				bodyless++
			}
			if !incoming[s.ID] {
				set[s.ID] = true
			}
		}
		for _, r := range sh.Routes {
			if r.HandlerSym != nil {
				set[*r.HandlerSym] = true
			}
		}
	}
	for id := range set {
		entry = append(entry, id)
	}
	sort.Strings(entry)
	return entry, functions, bodyless
}

type callBuilder struct {
	res       *Result
	l         *locator
	pkgByPath map[string]*packages.Package
	uni       map[*ssa.Function]bool
	ids       *idMaker
	taken     map[*ssa.Function]bool
	failed    bool
}

// site is one call instruction of a symbol, merged across C2 copies.
type site struct {
	mode    string
	hint    *string
	targets map[string]bool
	pos     token.Pos
	reflect string // reflective callee, if any
}

func (c *callBuilder) collision(id, why string) {
	c.res.Global = append(c.res.Global, globalDiag("symbol_id_collision", "error",
		"two distinct functions share the symbol ID "+id+" ("+why+"); call facts are incomplete"))
	c.failed = true
}

func (c *callBuilder) build(targetsOf func(ssa.CallInstruction) []*ssa.Function) {
	// Address-taken: a function used as a value anywhere in the universe.
	for fn := range c.uni {
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				var callee ssa.Value
				if ci, ok := ins.(ssa.CallInstruction); ok && !ci.Common().IsInvoke() {
					callee = ci.Common().Value
				}
				for _, op := range ins.Operands(nil) {
					if f, ok := (*op).(*ssa.Function); ok && *op != callee {
						c.taken[f] = true
					}
				}
			}
		}
	}

	// Canonical IDs (C1) and groups of equal IDs (C2/C3).
	groups := map[string][]*ssa.Function{}
	for fn := range c.uni {
		id := c.ids.id(fn)
		groups[id] = append(groups[id], fn)
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	isWrapper := func(fn *ssa.Function) bool {
		return fn.Synthetic != "" && fn.Origin() == nil && fn.Parent() == nil && fn.Synthetic != "package initializer"
	}
	for _, id := range ids {
		g := groups[id]
		if len(g) == 1 {
			continue
		}
		for _, f := range g[1:] {
			if !isWrapper(f) || !isWrapper(g[0]) || f.Synthetic != g[0].Synthetic {
				c.collision(id, "not equivalent wrappers")
				return
			}
		}
		// C2: one symbol for all copies, so its facts are the union.
		for _, f := range g {
			if c.taken[f] {
				for _, h := range g {
					c.taken[h] = true
				}
				break
			}
		}
	}

	// Existing symbols (types, routes): universe membership and body come
	// from SSA. Updated in place before any append to the shard slices.
	owner := map[string]string{} // symbol ID -> unit
	for uid, u := range c.res.Units {
		for i := range u.Shard.Symbols {
			s := &u.Shard.Symbols[i]
			owner[s.ID] = uid
			if !entryKinds[s.Kind] {
				continue
			}
			g, ok := groups[s.ID]
			s.InUniverse = ok
			if ok {
				s.HasBody = g[0].Blocks != nil
			}
		}
	}

	var pending []*ssa.Function // referenced functions outside the universe
	addSym := func(fn *ssa.Function, id string, in bool) {
		if _, ok := owner[id]; ok {
			return
		}
		unit := c.owner(fn)
		u := c.res.Units[unit]
		if u == nil {
			c.res.Global = append(c.res.Global, globalDiag("analyzer_crash", "error",
				"function "+id+" has no owner unit (owner "+fmt.Sprintf("%q", unit)+"); call facts are incomplete"))
			c.failed = true
			return
		}
		u.Shard.Symbols = append(u.Shard.Symbols, c.symbol(fn, id, unit, in))
		owner[id] = unit
		for _, ref := range []*ssa.Function{fn.Parent(), fn.Origin()} {
			if ref != nil && !c.uni[ref] {
				pending = append(pending, ref)
			}
		}
	}
	for _, id := range ids {
		addSym(groups[id][0], id, true)
		if c.failed {
			return
		}
	}
	for len(pending) > 0 {
		fn := pending[0]
		pending = pending[1:]
		addSym(fn, c.ids.id(fn), false)
		if c.failed {
			return
		}
	}

	// Wrapper origins: the method the wrapper calls. An interface method
	// (bound or thunk of an interface method, promotion through an
	// embedded interface) becomes an abstract, non-universe method symbol.
	wrapperOrigin := map[string]string{}
	predecl := map[string]string{} // predeclared interface method -> owner unit
	for _, id := range ids {
		fn := groups[id][0]
		if !isWrapper(fn) {
			continue
		}
		w, predeclared := c.wrapped(fn, owner)
		if w == "" {
			c.collision(id, "wrapper with no wrapped method")
			return
		}
		wrapperOrigin[id] = w
		if predeclared {
			if cur, ok := predecl[w]; !ok || owner[id] < cur {
				predecl[w] = owner[id]
			}
		}
	}
	// A method of a predeclared interface (error.Error) has no package; it
	// is owned by the smallest unit ID whose wrapper refers to it.
	for _, w := range sortedKeys(predecl) {
		if _, ok := owner[w]; ok {
			continue
		}
		u := c.res.Units[predecl[w]]
		u.Shard.Symbols = append(u.Shard.Symbols, facts.Symbol{ID: w, UnitID: predecl[w], Kind: "method",
			Name: w[strings.LastIndex(w, ".")+1:], Exported: true})
		owner[w] = predecl[w]
	}
	symAt := map[string]*facts.Symbol{}
	for _, u := range c.res.Units {
		for i := range u.Shard.Symbols {
			s := &u.Shard.Symbols[i]
			if w, ok := wrapperOrigin[s.ID]; ok && s.Kind == "wrapper" && s.OriginID == nil {
				s.OriginID = facts.Str(w)
			}
			symAt[s.ID] = s
		}
	}

	// Call sites and unknown-call diagnostics, per canonical symbol.
	for _, id := range ids {
		g := groups[id]
		var merged []*site
		for gi, fn := range g {
			sites := c.sites(fn, targetsOf)
			if gi == 0 {
				merged = sites
				continue
			}
			if len(sites) != len(merged) {
				c.collision(id, "wrapper copies with different call sites")
				return
			}
			for i, s := range sites {
				if s.mode != merged[i].mode || (s.mode == "static" && *s.hint != *merged[i].hint) {
					c.collision(id, "wrapper copies with different call sites")
					return
				}
				for t := range s.targets {
					merged[i].targets[t] = true
				}
			}
		}
		fn := g[0]
		unit := owner[id]
		sh := c.res.Units[unit].Shard
		pkg := c.pkgByPath[unit]
		noLoc := isWrapper(fn) || fn.Synthetic == "package initializer"
		for i, s := range merged {
			sid := fmt.Sprintf("%s#%d", id, i)
			var loc *facts.Location
			if !noLoc && pkg != nil {
				loc = c.l.pos(pkg, s.pos)
			}
			ts := make([]string, 0, len(s.targets))
			for t := range s.targets {
				ts = append(ts, t)
			}
			sort.Strings(ts)
			res := "vta"
			if s.mode == "static" {
				res = "static"
			}
			sh.CallSites = append(sh.CallSites, facts.CallSite{ID: sid, Caller: id, Ordinal: i, Mode: s.mode,
				CalleeHint: s.hint, Targets: ts, Resolution: res, Loc: loc})
			diag := func(kind, msg string) {
				d := facts.Diagnostic{Kind: kind, Severity: "warning", Scope: "unit", UnitID: facts.Str(unit),
					SymbolID: facts.Str(id), SiteID: facts.Str(sid), Message: msg, Loc: loc}
				d.ID = facts.DiagID(d)
				sh.Diagnostics = append(sh.Diagnostics, d)
			}
			if s.reflect != "" {
				diag("reflection_call", "call to "+s.reflect+": code reached by reflection is not modeled")
			}
			for _, t := range ts {
				if ts := symAt[t]; ts != nil && !ts.HasBody {
					diag("bodyless_function", "call to bodyless function "+t+" (assembly, linkname or intrinsic): its effects are not modeled")
				}
			}
			if s.mode != "static" && len(ts) == 0 {
				if s.mode == "invoke" {
					diag("unresolved_interface_call", "interface call "+*s.hint+" has no possible target in the analyzed universe")
				} else {
					diag("unresolved_dynamic_call", "dynamic call has no possible target in the analyzed universe")
				}
			}
		}
		for _, u := range c.reflectValueUses(fn, targetsOf) {
			var loc *facts.Location
			if !noLoc && pkg != nil {
				loc = c.l.pos(pkg, u.pos)
			}
			d := facts.NewDiagnostic("reflection_call", "warning", facts.Str(unit), facts.Str(id),
				"reflection entry point "+u.name+" used as a value: the code it reaches is not modeled", loc)
			sh.Diagnostics = append(sh.Diagnostics, d)
		}
		if n := unsafeConversions(fn); n > 0 {
			var loc *facts.Location
			if !noLoc && pkg != nil {
				loc = c.l.pos(pkg, c.fnPos(fn))
			}
			d := facts.NewDiagnostic("unsafe_pointer", "warning", facts.Str(unit), facts.Str(id),
				fmt.Sprintf("%d unsafe.Pointer conversion(s): memory reached through them is not modeled", n), loc)
			sh.Diagnostics = append(sh.Diagnostics, d)
		}
	}
}

// sites lists fn's non-builtin call instructions in block, then instruction
// order (schema README, Call-site IDs).
func (c *callBuilder) sites(fn *ssa.Function, targetsOf func(ssa.CallInstruction) []*ssa.Function) []*site {
	var out []*site
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			ci, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			cc := ci.Common()
			if _, ok := cc.Value.(*ssa.Builtin); ok {
				continue
			}
			s := &site{targets: map[string]bool{}, pos: ci.Pos()}
			switch {
			case cc.StaticCallee() != nil:
				s.mode = "static"
				s.hint = facts.Str(c.ids.id(cc.StaticCallee()))
			case cc.IsInvoke():
				s.mode = "invoke"
				s.hint = facts.Str(cc.Method.FullName())
			default:
				s.mode = "dynamic"
			}
			for _, t := range targetsOf(ci) {
				s.targets[c.ids.id(t)] = true
				// Reflection is classified from every resolved target, through
				// synthetic wrappers: a bound method value, a method
				// expression or an interface call of (reflect.Value).Call is
				// still a reflective call at this site.
				if r := c.reflectEntry(t, targetsOf, 0); r != "" && (s.reflect == "" || r < s.reflect) {
					s.reflect = r
				}
			}
			out = append(out, s)
		}
	}
	return out
}

// reflectValueUses lists the positions where fn uses a reflection entry
// point (or a wrapper of one) as a value rather than calling it, for example
// passing reflect.ValueOf(f).Call to another function. The use itself is
// reported, so a callback invoked from trusted code is not lost.
func (c *callBuilder) reflectValueUses(fn *ssa.Function, targetsOf func(ssa.CallInstruction) []*ssa.Function) []reflectUse {
	var out []reflectUse
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			var callee ssa.Value
			if ci, ok := ins.(ssa.CallInstruction); ok && !ci.Common().IsInvoke() {
				callee = ci.Common().Value
			}
			for _, op := range ins.Operands(nil) {
				f, ok := (*op).(*ssa.Function)
				if !ok || *op == callee {
					continue
				}
				if r := c.reflectEntry(f, targetsOf, 0); r != "" {
					out = append(out, reflectUse{pos: ins.Pos(), name: r})
				}
			}
		}
	}
	return out
}

type reflectUse struct {
	pos  token.Pos
	name string
}

// reflectEntry returns the reflection entry point fn is, or forwards to
// through synthetic wrappers ($bound, $thunk, pointer and promotion
// wrappers, instantiation wrappers), or "".
func (c *callBuilder) reflectEntry(fn *ssa.Function, targetsOf func(ssa.CallInstruction) []*ssa.Function, depth int) string {
	if reflectCalls[fn.String()] {
		return fn.String()
	}
	if o := fn.Origin(); o != nil && reflectCalls[o.String()] {
		return o.String()
	}
	if fn.Synthetic == "" || fn.Parent() != nil || depth > 4 || fn.Synthetic == "package initializer" {
		return ""
	}
	best := ""
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			ci, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			for _, t := range targetsOf(ci) {
				if r := c.reflectEntry(t, targetsOf, depth+1); r != "" && (best == "" || r < best) {
					best = r
				}
			}
		}
	}
	return best
}

func unsafeConversions(fn *ssa.Function) int {
	n := 0
	isUnsafe := func(t types.Type) bool {
		b, ok := t.Underlying().(*types.Basic)
		return ok && b.Kind() == types.UnsafePointer
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if cv, ok := ins.(*ssa.Convert); ok && (isUnsafe(cv.Type()) || isUnsafe(cv.X.Type())) {
				n++
			}
		}
	}
	return n
}

// owner returns the owner unit of fn (schema README, Owner unit of a symbol).
func (c *callBuilder) owner(fn *ssa.Function) string {
	if fn.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	if o := fn.Origin(); o != nil {
		return c.owner(o)
	}
	if p := fn.Parent(); p != nil {
		return c.owner(p)
	}
	if r := fn.Signature.Recv(); r != nil {
		if n := namedOf(r.Type()); n != nil && n.Obj().Pkg() != nil {
			return n.Obj().Pkg().Path()
		}
	}
	if f, ok := fn.Object().(*types.Func); ok {
		if r := f.Signature().Recv(); r != nil {
			if n := namedOf(r.Type()); n != nil && n.Obj().Pkg() != nil {
				return n.Obj().Pkg().Path()
			}
		}
		if f.Pkg() != nil {
			return f.Pkg().Path()
		}
	}
	return ""
}

func namedOf(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, _ := t.(*types.Named)
	return n
}

func (c *callBuilder) fnPos(fn *ssa.Function) token.Pos {
	if !fn.Pos().IsValid() && fn.Origin() != nil {
		return fn.Origin().Pos()
	}
	return fn.Pos()
}

// symbol builds the fact for an SSA function that no other pass declared.
func (c *callBuilder) symbol(fn *ssa.Function, id, unit string, in bool) facts.Symbol {
	s := facts.Symbol{ID: id, UnitID: unit, Name: symbolName(id, unit, fn), HasBody: fn.Blocks != nil, InUniverse: in}
	base := fn.Name()
	if i := strings.IndexAny(base, "$[#"); i >= 0 {
		base = base[:i]
	}
	s.Exported = token.IsExported(base)
	switch {
	case fn.Parent() != nil:
		s.Kind = "closure"
		s.ParentID = facts.Str(c.ids.id(fn.Parent()))
		s.Exported = false
	case fn.Origin() != nil:
		s.Kind = "instance"
		s.OriginID = facts.Str(c.ids.id(fn.Origin()))
	case strings.HasPrefix(fn.Synthetic, "bound method wrapper"):
		s.Kind, s.WrapperKind = "wrapper", facts.Str("bound")
	case strings.HasPrefix(fn.Synthetic, "thunk"):
		s.Kind, s.WrapperKind = "wrapper", facts.Str("thunk")
	case strings.HasPrefix(fn.Synthetic, "wrapper for"):
		s.Kind, s.WrapperKind = "wrapper", facts.Str("promotion")
		if f, ok := fn.Object().(*types.Func); ok && fn.Signature.Recv() != nil {
			if r := f.Signature().Recv(); r != nil {
				if a, b := namedOf(fn.Signature.Recv().Type()), namedOf(r.Type()); a != nil && b != nil && a.Obj() == b.Obj() {
					s.WrapperKind = facts.Str("pointer")
				}
			}
		}
	case fn.Signature.Recv() != nil:
		s.Kind = "method"
	default:
		s.Kind = "func"
	}
	isInit := fn.Synthetic == "package initializer" || strings.HasPrefix(fn.Name(), "init#")
	s.EntryHints = facts.EntryHints{
		IsMain:       s.Kind == "func" && fn.Pkg != nil && fn.Pkg.Pkg.Name() == "main" && fn.Name() == "main",
		IsInit:       isInit,
		AddressTaken: c.taken[fn],
	}
	if s.Kind != "wrapper" && fn.Synthetic != "package initializer" {
		if p := c.pkgByPath[unit]; p != nil {
			s.Loc = c.l.pos(p, c.fnPos(fn))
		}
	}
	return s
}

// symbolName is the canonical ID without its package or receiver, so it is
// spelled the same way as the ID.
func symbolName(id, unit string, fn *ssa.Function) string {
	if strings.HasPrefix(id, unit+".") {
		return id[len(unit)+1:]
	}
	if i := strings.Index(id, ")."); strings.HasPrefix(id, "(") && i > 0 {
		return id[i+2:]
	}
	return fn.Name()
}

// wrapped returns the ID of the method a wrapper calls: the static callee
// of its call, or the abstract interface method it invokes (added as a
// non-universe method symbol of the interface's unit).
func (c *callBuilder) wrapped(fn *ssa.Function, owner map[string]string) (string, bool) {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			ci, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			cc := ci.Common()
			if callee := cc.StaticCallee(); callee != nil {
				return c.ids.id(callee), false
			}
			if cc.IsInvoke() {
				m := cc.Method
				id := m.FullName()
				if m.Pkg() == nil {
					return id, true
				}
				unit := m.Pkg().Path()
				u := c.res.Units[unit]
				if u == nil {
					return "", false
				}
				if _, ok := owner[id]; ok {
					return id, false
				}
				s := facts.Symbol{ID: id, UnitID: unit, Kind: "method", Name: m.Name(), Exported: m.Exported()}
				if p := c.pkgByPath[unit]; p != nil {
					s.Loc = c.l.pos(p, m.Pos())
				}
				u.Shard.Symbols = append(u.Shard.Symbols, s)
				owner[id] = unit
				return id, false
			}
		}
	}
	return "", false
}

// idMaker computes canonical symbol IDs: go/ssa's name, with C1 applied to
// instances whose type arguments contain type parameters.
type idMaker struct {
	tpOwner map[*types.TypeParam]string
	cache   map[*ssa.Function]string
}

func newIDMaker(uni map[*ssa.Function]bool) *idMaker {
	m := &idMaker{tpOwner: map[*types.TypeParam]string{}, cache: map[*ssa.Function]string{}}
	// The owner of a type parameter is the generic function or method that
	// declares it. Closures and wrappers share their declarer's type
	// parameters, so declared functions win, then the smallest name.
	var cands []*ssa.Function
	for fn := range uni {
		if fn.Origin() == nil && fn.Parent() == nil && fn.TypeParams().Len() > 0 {
			cands = append(cands, fn)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if (a.Synthetic == "") != (b.Synthetic == "") {
			return a.Synthetic == ""
		}
		return a.String() < b.String()
	})
	for _, fn := range cands {
		tps := fn.TypeParams()
		for i := 0; i < tps.Len(); i++ {
			if _, ok := m.tpOwner[tps.At(i)]; !ok {
				m.tpOwner[tps.At(i)] = fn.String()
			}
		}
	}
	return m
}

func (m *idMaker) id(fn *ssa.Function) string {
	if id, ok := m.cache[fn]; ok {
		return id
	}
	id := m.compute(fn)
	m.cache[fn] = id
	return id
}

// compute follows (*ssa.Function).RelString, but writes every type with
// typeString when the name depends on instantiated or literal types. go/ssa
// spells such a type the way it first met it (os.DirEntry or
// io/fs.DirEntry for the same alias), and packages are built in parallel, so
// its own string is not stable across runs.
func (m *idMaker) compute(fn *ssa.Function) string {
	if p := fn.Parent(); p != nil {
		for i, anon := range p.AnonFuncs {
			if anon == fn {
				return fmt.Sprintf("%s$%d", m.id(p), i+1)
			}
		}
		return fn.String()
	}
	name := fn.Name()
	var recv types.Type
	switch {
	case fn.Signature.Recv() != nil:
		recv = fn.Signature.Recv().Type()
	case strings.HasPrefix(fn.Synthetic, "thunk") && fn.Signature.Params().Len() > 0:
		recv = fn.Signature.Params().At(0).Type()
	case len(fn.FreeVars) == 1 && strings.HasSuffix(name, "$bound"):
		recv = fn.FreeVars[0].Type()
	}
	if recv != nil {
		if fn.Origin() == nil && plainReceiver(recv) {
			return fn.String()
		}
		if k := strings.Index(name, "["); k >= 0 {
			name = name[:k]
		}
		return "(" + m.typeString(recv) + ")." + name
	}
	o := fn.Origin()
	if o == nil {
		return fn.String()
	}
	args := make([]string, len(fn.TypeArgs()))
	for i, t := range fn.TypeArgs() {
		args[i] = m.typeString(t)
	}
	return m.id(o) + "[" + strings.Join(args, ", ") + "]"
}

// plainReceiver reports whether t is a declared named type, or a pointer to
// one, without type arguments: its spelling does not depend on build order.
func plainReceiver(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	return ok && n.TypeArgs().Len() == 0
}

// typeString writes t with every alias resolved and package paths in full;
// a type parameter is written Name@<owner ID> (C1).
func (m *idMaker) typeString(t types.Type) string {
	switch t := t.(type) {
	case *types.Alias:
		return m.typeString(types.Unalias(t))
	case *types.TypeParam:
		o := m.tpOwner[t]
		if o == "" {
			o = "?"
		}
		return t.Obj().Name() + "@" + o
	case *types.Pointer:
		return "*" + m.typeString(t.Elem())
	case *types.Slice:
		return "[]" + m.typeString(t.Elem())
	case *types.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), m.typeString(t.Elem()))
	case *types.Map:
		return "map[" + m.typeString(t.Key()) + "]" + m.typeString(t.Elem())
	case *types.Chan:
		p := "chan "
		switch t.Dir() {
		case types.SendOnly:
			p = "chan<- "
		case types.RecvOnly:
			p = "<-chan "
		}
		return p + m.typeString(t.Elem())
	case *types.Named:
		obj := t.Obj()
		name := obj.Name()
		if obj.Pkg() != nil {
			name = obj.Pkg().Path() + "." + name
		}
		if t.TypeArgs().Len() == 0 {
			return name
		}
		var args []string
		for i := 0; i < t.TypeArgs().Len(); i++ {
			args = append(args, m.typeString(t.TypeArgs().At(i)))
		}
		return name + "[" + strings.Join(args, ", ") + "]"
	case *types.Signature:
		return "func" + m.sigString(t)
	case *types.Struct:
		var fs []string
		for i := 0; i < t.NumFields(); i++ {
			f := t.Field(i)
			s := m.typeString(f.Type())
			if !f.Embedded() {
				s = f.Name() + " " + s
			}
			if tag := t.Tag(i); tag != "" {
				s += " " + fmt.Sprintf("%q", tag)
			}
			fs = append(fs, s)
		}
		return "struct{" + strings.Join(fs, "; ") + "}"
	case *types.Interface:
		var ms []string
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if e := t.EmbeddedType(i); !types.IsInterface(e) {
				ms = append(ms, m.typeString(e))
			}
		}
		for i := 0; i < t.NumMethods(); i++ {
			f := t.Method(i)
			ms = append(ms, f.Name()+m.sigString(f.Type().(*types.Signature)))
		}
		return "interface{" + strings.Join(ms, "; ") + "}"
	case *types.Union:
		var ts []string
		for i := 0; i < t.Len(); i++ {
			s := m.typeString(t.Term(i).Type())
			if t.Term(i).Tilde() {
				s = "~" + s
			}
			ts = append(ts, s)
		}
		return strings.Join(ts, "|")
	}
	return types.TypeString(t, (*types.Package).Path)
}

func (m *idMaker) sigString(sig *types.Signature) string {
	var ps, rs []string
	for i := 0; i < sig.Params().Len(); i++ {
		pt := sig.Params().At(i).Type()
		if sig.Variadic() && i == sig.Params().Len()-1 {
			if sl, ok := pt.(*types.Slice); ok {
				ps = append(ps, "..."+m.typeString(sl.Elem()))
				continue
			}
		}
		ps = append(ps, m.typeString(pt))
	}
	for i := 0; i < sig.Results().Len(); i++ {
		rs = append(rs, m.typeString(sig.Results().At(i).Type()))
	}
	s := "(" + strings.Join(ps, ", ") + ")"
	switch len(rs) {
	case 0:
	case 1:
		s += " " + rs[0]
	default:
		s += " (" + strings.Join(rs, ", ") + ")"
	}
	return s
}

func anyTypeParam(ts []types.Type) bool {
	for _, t := range ts {
		if hasTypeParam(t) {
			return true
		}
	}
	return false
}

func hasTypeParam(t types.Type) bool {
	switch t := t.(type) {
	case *types.TypeParam:
		return true
	case *types.Pointer:
		return hasTypeParam(t.Elem())
	case *types.Slice:
		return hasTypeParam(t.Elem())
	case *types.Array:
		return hasTypeParam(t.Elem())
	case *types.Map:
		return hasTypeParam(t.Key()) || hasTypeParam(t.Elem())
	case *types.Chan:
		return hasTypeParam(t.Elem())
	case *types.Alias:
		return hasTypeParam(types.Unalias(t))
	case *types.Named:
		for i := 0; i < t.TypeArgs().Len(); i++ {
			if hasTypeParam(t.TypeArgs().At(i)) {
				return true
			}
		}
	case *types.Signature:
		for i := 0; i < t.Params().Len(); i++ {
			if hasTypeParam(t.Params().At(i).Type()) {
				return true
			}
		}
		for i := 0; i < t.Results().Len(); i++ {
			if hasTypeParam(t.Results().At(i).Type()) {
				return true
			}
		}
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if hasTypeParam(t.Field(i).Type()) {
				return true
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			if hasTypeParam(t.Method(i).Type()) {
				return true
			}
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Manifest builds the analysis manifest for this run (analysis.schema.json).
// Shard hashes are taken over the shards as they are now, so it must be
// called after loading has finished.
func (res *Result) Manifest(version string, timeoutSeconds, memoryMiB int) *facts.Manifest {
	m := &facts.Manifest{
		SchemaVersion: facts.SchemaVersion, Kind: "analysis_manifest",
		Analyzer:     facts.Analyzer{Name: "bedrock-analyzer-go", Version: version},
		Status:       res.Calls.Status,
		BuildProfile: Profile(), BuildProfileDigest: ProfileDigest(),
		Module:   facts.ManifestModule{Path: res.ModulePath, Dir: "."},
		Patterns: []string{"./..."},
		Limits:   facts.Limits{TimeoutSeconds: timeoutSeconds, MemoryMiB: memoryMiB},
		Universe: facts.Universe{Definition: "all_functions_transitive_closure_v1", BodylessPolicy: "leaf_with_diagnostic_v1",
			FunctionCount: res.Calls.FunctionCount, BodylessCount: res.Calls.BodylessCount},
		Units:             []facts.ManifestUnit{},
		ExcludedPackages:  append([]facts.ExcludedPackage{}, res.Excluded...),
		Entry:             facts.EntryDigestOf(res.Calls.Entry),
		GlobalDiagnostics: []facts.Diagnostic{},
	}
	if m.Status == "" {
		m.Status = "load_error"
	}
	seen := map[string]bool{}
	for _, d := range res.Global {
		if d.Severity == "error" && m.Status == "complete" {
			m.Status = "load_error"
		}
		if !seen[d.ID] {
			seen[d.ID] = true
			m.GlobalDiagnostics = append(m.GlobalDiagnostics, d)
		}
	}
	sortDiags(m.GlobalDiagnostics)
	sort.Slice(m.ExcludedPackages, func(i, j int) bool { return m.ExcludedPackages[i].ID < m.ExcludedPackages[j].ID })
	for _, id := range sortedUnitIDs(res.Units) {
		u := res.Units[id]
		m.Units = append(m.Units, facts.ManifestUnit{ID: id, Origin: u.Origin, ContentHash: u.Shard.Unit.ContentHash,
			Shard: facts.SHA256(facts.Canon(u.Shard))})
	}
	return m
}
