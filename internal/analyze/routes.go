package analyze

// Route and middleware extraction per the router support matrix v1
// (docs/superpowers/specs/routers/README.md). Identity is decided on SSA
// values of module code; chain inheritance follows chi v5.5.0
// (With/Group/Route snapshot semantics, late Use diagnosed and not credited);
// net/http patterns are validated by replaying them on a scratch ServeMux per
// mux identity; chi patterns and methods by a scratch chi router.

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"

	"github.com/LavishGent/bedrock/internal/facts"
)

const (
	chiPath  = "github.com/go-chi/chi/v5"
	httpPath = "net/http"
)

// Category says how far a route diagnostic reaches (router matrix, section 2,
// "Which rules a diagnostic affects").
type Category int

const (
	CatIdentity  Category = iota // unknown registration identity: every require_middleware rule
	CatSite                      // known site, no route fact: rules whose scope has the unit
	CatChain                     // known route, uncertain chain: rules governing the route
	CatPath                      // known route, unresolved path: rules whose scope has the unit
	CatHandler                   // known route, unresolved handler: rules governing the route
	CatDuplicate                 // duplicate normalized key: warning only
)

// Route is an extracted route with its effective chain.
type Route struct {
	Fact      facts.Route
	Chain     []ChainEntry
	Uncertain []string // IDs of diagnostics that make the chain uncertain
}

// ChainEntry is one middleware in a route's chain, outermost first.
type ChainEntry struct {
	Sym        string
	Source     string
	Loc        *facts.Location
	Unresolved bool
	DiagID     string
}

// RouteDiag is a route-extraction diagnostic with its reach.
type RouteDiag struct {
	Diag   facts.Diagnostic
	Cat    Category
	Unit   string
	Routes []*Route // CatChain / CatHandler: the routes it concerns
}

// fixedMethods are chi's direct registration methods (all supported).
var fixedMethods = map[string]string{
	"Get": "GET", "Post": "POST", "Put": "PUT", "Delete": "DELETE", "Patch": "PATCH",
	"Head": "HEAD", "Options": "OPTIONS", "Connect": "CONNECT", "Trace": "TRACE", "Query": "QUERY",
}

// unsupportedChi are recognized chi registration APIs outside the matrix.
var unsupportedChi = map[string]bool{
	"Handle": true, "HandleFunc": true, "MethodFunc": true, "NotFound": true,
	"MethodNotAllowed": true, "Mount": true,
}

// readOnlyChi are chi methods that do not register anything.
var readOnlyChi = map[string]bool{
	"ServeHTTP": true, "Routes": true, "Middlewares": true, "Match": true, "Find": true,
	"NotFoundHandler": true, "MethodNotAllowedHandler": true,
}

type routerKind int

const (
	chiMux routerKind = iota
	chiInline
	httpMux
)

type router struct {
	kind       routerKind
	base       *router      // nearest chi mux (itself for a mux)
	parent     *router      // router it was derived from (With/Group/Route), nil for a root
	inherited  []ChainEntry // mux from Route: Chain(parent) at the Route call
	own        []ChainEntry // L(X)
	prefix     string
	hadRoute   bool // a route (or mount) registered on it
	hadDerive  bool // mux: With/Group/Route/Mount called on it
	scratch    *http.ServeMux
	routes     []*Route
	lateDiags  []string // late-Use diagnostics: apply to all its routes
	createdPos token.Pos
}

func (r *router) chain() []ChainEntry {
	var out []ChainEntry
	if r.kind == chiInline {
		out = append(out, r.base.inherited...)
		out = append(out, r.base.own...)
		out = append(out, r.own...)
	} else {
		out = append(out, r.inherited...)
		out = append(out, r.own...)
	}
	return append([]ChainEntry{}, out...)
}

// extractor holds program-wide extraction state.
type extractor struct {
	prog       *ssa.Program
	l          *locator
	pkgOf      map[*types.Package]*packages.Package
	nested     map[*ssa.Function]bool // literals passed directly to Group/Route
	defaultMux *router
	routes     []*Route
	diags      []RouteDiag
	closures   map[string][]facts.Symbol // unit -> closure symbols
	parents    map[string]*ssa.Function  // closure parent ID -> function (for package initializers)
	fnByID     map[string]*ssa.Function  // every module function with a body, by symbol ID
	module     map[string]bool           // module package paths (analyzed for routes)
	// routerUnderlying are the underlying types of http.ServeMux and chi.Mux:
	// a defined type with one of them is convertible to a router.
	routerUnderlying []types.Type
	wrappers   map[string]facts.Symbol   // $bound wrapper ID -> symbol (owner set later)
}

// extractRoutes builds SSA for the well-typed module packages and extracts
// routes, middleware chains and diagnostics.
func extractRoutes(all []*packages.Package, module map[string]bool, l *locator) *extractor {
	x := &extractor{l: l, pkgOf: map[*types.Package]*packages.Package{}, nested: map[*ssa.Function]bool{},
		closures: map[string][]facts.Symbol{}, wrappers: map[string]facts.Symbol{}, parents: map[string]*ssa.Function{}, fnByID: map[string]*ssa.Function{}, module: module}
	if len(all) == 0 {
		return x
	}
	x.prog = ssa.NewProgram(all[0].Fset, 0)
	// Create SSA packages in dependency order, only when the package and all
	// its imports are well typed.
	created := map[*packages.Package]bool{}
	var order []*packages.Package
	packages.Visit(all, nil, func(p *packages.Package) { order = append(order, p) })
	seen := map[*packages.Package]bool{}
	var ordered []*packages.Package
	for _, p := range order {
		if !seen[p] {
			seen[p] = true
			ordered = append(ordered, p)
		}
	}
	var modulePkgs []*ssa.Package
	skipped := map[*packages.Package]bool{}
	for _, p := range ordered {
		ok := p.Types != nil && !p.IllTyped && len(p.Errors) == 0
		for _, ip := range p.Imports {
			if !created[ip] {
				ok = false
			}
		}
		if !ok {
			if module[p.PkgPath] {
				skipped[p] = true
			}
			continue
		}
		created[p] = true
		x.pkgOf[p.Types] = p
		sp := x.prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		if module[p.PkgPath] {
			modulePkgs = append(modulePkgs, sp)
		}
	}
	// A governed package that cannot be built cannot have its routes
	// extracted; if it uses a router package, that is unknown identity.
	for p := range skipped {
		for path := range p.Imports {
			if path == chiPath || path == httpPath {
				x.diag(CatIdentity, p.PkgPath, nil, "unresolved_dynamic_route",
					"routes of "+p.PkgPath+" cannot be extracted: the package or one of its imports does not type-check", nil)
				break
			}
		}
	}
	sort.Slice(modulePkgs, func(i, j int) bool { return modulePkgs[i].Pkg.Path() < modulePkgs[j].Pkg.Path() })
	var fns []*ssa.Function
	for _, sp := range modulePkgs {
		sp.Build()
		fns = append(fns, packageFuncs(x.prog, sp)...)
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].String() < fns[j].String() })
	for _, fn := range fns {
		x.fnByID[fn.String()] = fn
		x.scanNested(fn)
		if fn.Parent() != nil {
			unit := fn.Pkg.Pkg.Path()
			x.parents[fn.Parent().String()] = fn.Parent()
			x.closures[unit] = append(x.closures[unit], facts.Symbol{
				ID: fn.String(), UnitID: unit, Kind: "closure", Name: fn.Name(), HasBody: true, InUniverse: true,
				ParentID: facts.Str(fn.Parent().String()), Loc: x.pos(fn, fn.Pos()),
			})
		}
	}
	for _, fn := range fns {
		if !x.nested[fn] {
			x.process(fn, nil)
		}
	}
	x.routerUnderlying = routerUnderlyingTypes(all)
	x.audit(fns)
	return x
}

// packageFuncs lists every function of a module package with a body:
// package-level functions, declared methods and all nested literals.
func packageFuncs(prog *ssa.Program, sp *ssa.Package) []*ssa.Function {
	var out []*ssa.Function
	var add func(fn *ssa.Function)
	add = func(fn *ssa.Function) {
		if fn == nil || fn.Blocks == nil {
			return
		}
		out = append(out, fn)
		for _, a := range fn.AnonFuncs {
			add(a)
		}
	}
	for _, m := range sp.Members {
		switch m := m.(type) {
		case *ssa.Function:
			add(m)
		case *ssa.Type:
			named, ok := m.Type().(*types.Named)
			if !ok {
				continue
			}
			for i := 0; i < named.NumMethods(); i++ {
				add(prog.FuncValue(named.Method(i)))
			}
		}
	}
	return out
}

func (x *extractor) pos(fn *ssa.Function, p token.Pos) *facts.Location {
	if fn.Pkg == nil {
		return nil
	}
	pp := x.pkgOf[fn.Pkg.Pkg]
	if pp == nil {
		return nil
	}
	return x.l.pos(pp, p)
}

func (x *extractor) diag(cat Category, unit string, fn *ssa.Function, kind, msg string, loc *facts.Location, rs ...*Route) RouteDiag {
	var sym *string
	if fn != nil {
		sym = facts.Str(fn.String())
	}
	sev := "error"
	if cat == CatDuplicate {
		sev = "warning"
	}
	d := RouteDiag{Diag: facts.NewDiagnostic(kind, sev, facts.Str(unit), sym, msg, loc), Cat: cat, Unit: unit, Routes: rs}
	x.diags = append(x.diags, d)
	return d
}

// op is a recognized router call.
type op struct {
	pkg    string // chiPath or httpPath
	name   string // method or function name
	recv   ssa.Value
	args   []ssa.Value // without the receiver
	isFunc bool        // package-level function (http.Handle, chi.RegisterMethod)
}

// named strips aliases (type R = chi.Router) and one pointer level and
// returns the named type, if any.
func named(t types.Type) (*types.Named, bool) {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	return n, ok
}

func isChiRouterType(t types.Type) bool {
	n, ok := named(t)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != chiPath {
		return false
	}
	return n.Obj().Name() == "Mux" || n.Obj().Name() == "Router"
}

func classify(c *ssa.CallCommon) *op {
	if c.IsInvoke() {
		if isChiRouterType(c.Value.Type()) {
			return &op{pkg: chiPath, name: c.Method.Name(), recv: c.Value, args: c.Args}
		}
		return nil
	}
	callee := c.StaticCallee()
	if callee == nil || callee.Pkg == nil && callee.Object() == nil {
		return nil
	}
	obj, _ := callee.Object().(*types.Func)
	if obj == nil || obj.Pkg() == nil {
		return nil
	}
	path := obj.Pkg().Path()
	sig := obj.Type().(*types.Signature)
	switch {
	case path == chiPath && sig.Recv() != nil && isChiRouterType(sig.Recv().Type()) && len(c.Args) > 0:
		return &op{pkg: chiPath, name: obj.Name(), recv: c.Args[0], args: c.Args[1:]}
	case path == chiPath && sig.Recv() == nil && obj.Name() == "RegisterMethod":
		return &op{pkg: chiPath, name: "RegisterMethod", isFunc: true, args: c.Args}
	case path == httpPath && sig.Recv() != nil && len(c.Args) > 0 && isServeMux(sig.Recv().Type()) &&
		(obj.Name() == "Handle" || obj.Name() == "HandleFunc"):
		return &op{pkg: httpPath, name: obj.Name(), recv: c.Args[0], args: c.Args[1:]}
	case path == httpPath && sig.Recv() == nil && (obj.Name() == "Handle" || obj.Name() == "HandleFunc"):
		return &op{pkg: httpPath, name: obj.Name(), isFunc: true, args: c.Args}
	}
	return nil
}

func isServeMux(t types.Type) bool {
	n, ok := named(t)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == httpPath && n.Obj().Name() == "ServeMux"
}

func peel(v ssa.Value) ssa.Value {
	for {
		switch t := v.(type) {
		case *ssa.ChangeType:
			v = t.X
		case *ssa.MakeInterface:
			v = t.X
		case *ssa.ChangeInterface:
			v = t.X
		default:
			return v
		}
	}
}

func isDefaultMux(v ssa.Value) bool {
	u, ok := v.(*ssa.UnOp)
	if !ok || u.Op != token.MUL {
		return false
	}
	g, ok := u.X.(*ssa.Global)
	return ok && g.Pkg != nil && g.Pkg.Pkg.Path() == httpPath && g.Name() == "DefaultServeMux"
}

func constString(v ssa.Value) (string, bool) {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(c.Value), true
}

// scanNested marks function literals passed directly as fn to Group/Route.
func (x *extractor) scanNested(fn *ssa.Function) {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			ci, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			o := classify(ci.Common())
			if o == nil || o.pkg != chiPath || (o.name != "Group" && o.name != "Route") || len(o.args) == 0 {
				continue
			}
			if lit := literal(fn, o.args[len(o.args)-1]); lit != nil {
				x.nested[lit] = true
			}
		}
	}
}

// literal returns the function literal written in place in parent, if v is one.
func literal(parent *ssa.Function, v ssa.Value) *ssa.Function {
	switch t := peel(v).(type) {
	case *ssa.Function:
		if t.Parent() == parent {
			return t
		}
	case *ssa.MakeClosure:
		if f, ok := t.Fn.(*ssa.Function); ok && f.Parent() == parent {
			return f
		}
	}
	return nil
}

// unconditional reports the blocks executed exactly once on every normal
// path: not in a cycle, and dominating every return block.
func unconditional(fn *ssa.Function) map[*ssa.BasicBlock]bool {
	// Only returns reachable from the entry count: the recover block of a
	// function with defer is entered only on panic.
	reach := map[*ssa.BasicBlock]bool{}
	if len(fn.Blocks) > 0 {
		stack := []*ssa.BasicBlock{fn.Blocks[0]}
		for len(stack) > 0 {
			b := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if reach[b] {
				continue
			}
			reach[b] = true
			stack = append(stack, b.Succs...)
		}
	}
	var rets []*ssa.BasicBlock
	for _, b := range fn.Blocks {
		if !reach[b] {
			continue
		}
		if len(b.Instrs) > 0 {
			if _, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return); ok {
				rets = append(rets, b)
			}
		}
	}
	if len(rets) == 0 {
		for _, b := range fn.Blocks {
			if reach[b] && len(b.Succs) == 0 {
				rets = append(rets, b)
			}
		}
	}
	out := map[*ssa.BasicBlock]bool{}
	for _, b := range fn.Blocks {
		if inCycle(b) {
			continue
		}
		all := true
		for _, r := range rets {
			if !b.Dominates(r) {
				all = false
			}
		}
		out[b] = all
	}
	return out
}

func inCycle(b *ssa.BasicBlock) bool {
	seen := map[*ssa.BasicBlock]bool{}
	stack := append([]*ssa.BasicBlock{}, b.Succs...)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c == b {
			return true
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		stack = append(stack, c.Succs...)
	}
	return false
}

// process walks fn in block order. param, if non-nil, is the router value of
// fn's first parameter (a Group/Route literal).
func (x *extractor) process(fn *ssa.Function, param *router) {
	unit := fn.Pkg.Pkg.Path()
	env := map[ssa.Value]*router{}
	if param != nil && len(fn.Params) > 0 {
		env[fn.Params[0]] = param
	}
	uncond := unconditional(fn)
	lookup := func(v ssa.Value) *router {
		v = peel(v)
		if isDefaultMux(v) {
			return x.defaultMuxRouter()
		}
		return env[v]
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			x.apiValues(fn, unit, ins)
			ci, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			c := ci.Common()
			loc := x.pos(fn, ci.Pos())
			if o := classify(c); o == nil && registrationShaped(c) {
				// A call through a function value or a foreign interface with
				// the shape of a registration (pattern, handler) cannot be
				// resolved to a supported router: fail closed, module-wide.
				x.diag(CatIdentity, unit, fn, "unsupported_router_form",
					"call with a registration-shaped signature (pattern, handler) through a function value or interface that cannot be resolved to a supported router", loc)
				continue
			}
			if o := classify(c); o == nil && x.unanalyzedRouterCall(c) {
				// Code outside the module is not analyzed for routes; a router
				// handed to it may be registered on there.
				x.diag(CatIdentity, unit, fn, "unsupported_router_form",
					"a router value is passed to "+c.StaticCallee().String()+", which is outside the analyzed module; registrations there cannot be identified", loc)
				continue
			}
			cond := !uncond[b]
			if _, isGo := ins.(*ssa.Go); isGo {
				cond = true
			}
			if _, isDefer := ins.(*ssa.Defer); isDefer {
				cond = true
			}
			// Router constructors.
			if callee := c.StaticCallee(); callee != nil && !c.IsInvoke() {
				if obj, _ := callee.Object().(*types.Func); obj != nil && obj.Pkg() != nil {
					name, path := obj.Name(), obj.Pkg().Path()
					call, isCall := ins.(*ssa.Call)
					if isCall && ((path == chiPath && (name == "NewRouter" || name == "NewMux")) || (path == httpPath && name == "NewServeMux")) {
						r := &router{kind: chiMux, createdPos: ci.Pos()}
						if path == httpPath {
							r.kind, r.scratch = httpMux, http.NewServeMux()
						}
						r.base = r
						env[call] = r
						continue
					}
				}
			}
			o := classify(c)
			if o == nil {
				continue
			}
			if o.isFunc {
				if o.name == "RegisterMethod" {
					x.diag(CatIdentity, unit, fn, "unsupported_router_form", "chi.RegisterMethod changes the set of valid methods globally (unsupported)", loc)
					continue
				}
				// http.Handle / http.HandleFunc use DefaultServeMux.
				x.register(fn, unit, x.defaultMuxRouter(), o, cond, loc)
				continue
			}
			r := lookup(o.recv)
			if r == nil {
				if o.pkg == chiPath && readOnlyChi[o.name] {
					continue
				}
				x.diag(CatIdentity, unit, fn, "unsupported_router_form",
					fmt.Sprintf("%s registration on a receiver that is not a supported router value (parameter, field, call result or merged value)", o.name), loc)
				continue
			}
			if o.pkg == httpPath {
				x.register(fn, unit, r, o, cond, loc)
				continue
			}
			switch {
			case readOnlyChi[o.name]:
			case unsupportedChi[o.name]:
				x.diag(CatIdentity, unit, fn, "unsupported_router_form", "chi "+o.name+" is not supported by the router matrix v1", loc)
				if o.name == "Mount" {
					r.hadRoute, r.base.hadDerive = true, true
				}
			case fixedMethods[o.name] != "" || o.name == "Method":
				x.register(fn, unit, r, o, cond, loc)
			case o.name == "Use":
				x.use(fn, unit, r, o.args[0], ci, cond, loc)
			case o.name == "With":
				child := x.with(fn, unit, r, o.args[0], ci, cond, loc)
				if call, ok := ins.(*ssa.Call); ok && child != nil {
					env[call] = child
				}
			case o.name == "Group":
				child := x.with(fn, unit, r, nil, ci, false, loc)
				if call, ok := ins.(*ssa.Call); ok {
					env[call] = child
				}
				x.body(fn, unit, child, o.args[len(o.args)-1], cond, loc, "Group")
			case o.name == "Route":
				p, ok := constString(o.args[0])
				switch {
				case !ok:
					x.diag(CatSite, unit, fn, "unresolved_dynamic_route", "Route prefix is not a constant string", loc)
				case !chiPatternOK(p) || badSegments(p):
					x.diag(CatSite, unit, fn, "unsupported_router_form", fmt.Sprintf("Route prefix %q is rejected by chi or outside the bounded subset", p), loc)
				default:
					sub := &router{kind: chiMux, inherited: r.chain(), prefix: joinPrefix(r.prefix, p), createdPos: ci.Pos(), parent: r}
					sub.base = sub
					if call, ok := ins.(*ssa.Call); ok {
						env[call] = sub
					}
					x.body(fn, unit, sub, o.args[len(o.args)-1], cond, loc, "Route")
				}
				r.hadRoute = true
				r.base.hadDerive = true
			default:
				x.diag(CatIdentity, unit, fn, "unsupported_router_form", "chi "+o.name+" is not recognized by the router matrix v1", loc)
			}
		}
	}
}

func (x *extractor) defaultMuxRouter() *router {
	if x.defaultMux == nil {
		x.defaultMux = &router{kind: httpMux, scratch: http.NewServeMux()}
		x.defaultMux.base = x.defaultMux
	}
	return x.defaultMux
}

// body processes a Group/Route literal with its sub-router.
func (x *extractor) body(fn *ssa.Function, unit string, child *router, arg ssa.Value, cond bool, loc *facts.Location, what string) {
	lit := literal(fn, arg)
	switch {
	case lit == nil:
		x.diag(CatIdentity, unit, fn, "unsupported_router_form", what+" with a function that is not a literal written in place; its registrations cannot be identified", loc)
	case cond:
		x.diag(CatSite, unit, fn, "unsupported_router_form", what+" under a condition or in a loop", loc)
	default:
		x.process(lit, child)
	}
}

// with creates the inline router I := r.With(mws...) (or Group's With()).
func (x *extractor) with(fn *ssa.Function, unit string, r *router, mws ssa.Value, call ssa.CallInstruction, cond bool, loc *facts.Location) *router {
	if r.kind == httpMux {
		return nil
	}
	child := &router{kind: chiInline, base: r.base, prefix: r.prefix, parent: r}
	if r.kind == chiInline {
		child.own = append(child.own, r.own...) // snapshot at the With call
	} else {
		r.hadDerive = true
	}
	if mws != nil {
		child.own = append(child.own, x.middlewares(fn, unit, mws, call, "with", cond, loc)...)
	}
	return child
}

// use appends r.Use(mws...) unless the Use is late or conditional.
func (x *extractor) use(fn *ssa.Function, unit string, r *router, mws ssa.Value, call ssa.CallInstruction, cond bool, loc *facts.Location) {
	late := r.hadRoute || (r.kind == chiMux && r.hadDerive)
	if late {
		d := x.diag(CatChain, unit, fn, "unsupported_middleware_form",
			"late Use: chi panics when middleware is added after routes, With, Group, Route or Mount on the same router; the middleware is credited to no route", loc)
		r.lateDiags = append(r.lateDiags, d.Diag.ID)
		for _, rt := range r.routes {
			rt.Uncertain = append(rt.Uncertain, d.Diag.ID)
		}
		x.diags[len(x.diags)-1].Routes = append(x.diags[len(x.diags)-1].Routes, r.routes...)
		return
	}
	r.own = append(r.own, x.middlewares(fn, unit, mws, call, "use", cond, loc)...)
}

// middlewares resolves a variadic middleware argument.
func (x *extractor) middlewares(fn *ssa.Function, unit string, v ssa.Value, call ssa.CallInstruction, source string, cond bool, loc *facts.Location) []ChainEntry {
	unresolved := func(why string) []ChainEntry {
		d := x.diag(CatChain, unit, fn, "unsupported_middleware_form", why, loc)
		return []ChainEntry{{Source: source, Loc: loc, Unresolved: true, DiagID: d.Diag.ID}}
	}
	if cond {
		return unresolved("conditional middleware: Use/With under a condition or in a loop")
	}
	if c, ok := v.(*ssa.Const); ok && c.IsNil() {
		return nil
	}
	elems, why := varargs(v, call)
	if why != "" {
		return unresolved(why)
	}
	var out []ChainEntry
	for i := int64(0); i < int64(len(elems)); i++ {
		e, ok := elems[i]
		if !ok {
			return unresolved("middleware list has a gap")
		}
		sym, ok := directFunc(e)
		if !ok {
			out = append(out, unresolved("middleware is not a direct reference to a function or method value")...)
			continue
		}
		out = append(out, ChainEntry{Sym: sym, Source: source, Loc: loc})
	}
	return out
}

// apiValues diagnoses a registration API used as a value rather than called
// directly: var register = http.HandleFunc, f := http.Handle, method
// expressions such as (*chi.Mux).Get, or storing/passing such a function.
// Its later indirect calls cannot be attributed to a router identity.
func (x *extractor) apiValues(fn *ssa.Function, unit string, ins ssa.Instruction) {
	var callee ssa.Value
	if ci, ok := ins.(ssa.CallInstruction); ok && !ci.Common().IsInvoke() {
		callee = ci.Common().Value
	}
	for _, op := range ins.Operands(nil) {
		if op == nil || *op == nil || *op == callee {
			continue
		}
		f, ok := (*op).(*ssa.Function)
		if !ok || !isRegistrationAPI(f) {
			continue
		}
		x.diag(CatIdentity, unit, fn, "unsupported_router_form",
			"registration API "+f.String()+" is used as a function value (alias, variable, field or argument); its calls cannot be attributed to a router", x.pos(fn, ins.Pos()))
	}
}

// isRegistrationAPI reports a chi or net/http function or method (or its
// thunk/wrapper) that registers routes or middleware.
func isRegistrationAPI(f *ssa.Function) bool {
	obj, _ := f.Object().(*types.Func)
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	sig := obj.Type().(*types.Signature)
	switch obj.Pkg().Path() {
	case chiPath:
		if sig.Recv() == nil {
			return obj.Name() == "RegisterMethod"
		}
		name := obj.Name()
		return isChiRouterType(sig.Recv().Type()) && (fixedMethods[name] != "" || unsupportedChi[name] ||
			name == "Method" || name == "Use" || name == "With" || name == "Group" || name == "Route")
	case httpPath:
		if obj.Name() != "Handle" && obj.Name() != "HandleFunc" {
			return false
		}
		return sig.Recv() == nil || isServeMux(sig.Recv().Type())
	}
	return false
}

// registrationShaped reports a dynamic call (function value) or an invoke on
// a non-chi interface whose signature is (string, handler, ...): a pattern
// and an http.Handler, http.HandlerFunc or func(ResponseWriter, *Request).
func registrationShaped(c *ssa.CallCommon) bool {
	if c.StaticCallee() != nil {
		return false
	}
	if c.IsInvoke() && isChiRouterType(c.Value.Type()) {
		return false
	}
	sig := c.Signature()
	if sig == nil {
		return false
	}
	// A dynamic call that receives a router value can register on it,
	// whatever the rest of its signature (reg(r, "/p", h) with reg bound to
	// (*chi.Mux).Get by an external caller).
	for _, a := range c.Args {
		if isRouterValueType(a.Type()) {
			return true
		}
	}
	params := sig.Params()
	first := 0
	if params.Len() >= 3 && isRouterValueType(params.At(0).Type()) {
		first = 1 // receiver-first: func(router, pattern, handler)
	}
	if params.Len() < first+2 {
		return false
	}
	b, ok := types.Unalias(params.At(first).Type()).Underlying().(*types.Basic)
	if !ok || b.Kind() != types.String {
		return false
	}
	h := params.At(first + 1).Type()
	if isHTTPHandler(h) || isHandlerFunc(h) {
		return true
	}
	fs, ok := types.Unalias(h).Underlying().(*types.Signature)
	return ok && fs.Params().Len() == 2 && fs.Results().Len() == 0 && isResponseWriter(fs.Params().At(0).Type())
}

// unanalyzedRouterCall reports a static call into a package that is neither
// module code (analyzed) nor chi/net/http (modeled) with a router-typed
// argument.
func (x *extractor) unanalyzedRouterCall(c *ssa.CallCommon) bool {
	callee := c.StaticCallee()
	if callee == nil {
		return false
	}
	obj, _ := callee.Object().(*types.Func)
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	path := obj.Pkg().Path()
	if x.module[path] || path == chiPath || path == httpPath {
		return false
	}
	for _, a := range c.Args {
		if isRouterValueType(a.Type()) {
			return true
		}
	}
	return false
}

// isRouterValueType reports a recognized router type (*http.ServeMux, a chi
// Mux or Router, aliases resolved).
func isRouterValueType(t types.Type) bool { return isServeMux(t) || isChiRouterType(t) }

func isResponseWriter(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == httpPath && n.Obj().Name() == "ResponseWriter"
}

// varargs accepts only the slice the compiler builds for in-place variadic
// arguments (r.Use(A, B)): an Alloc marked "varargs" whose elements are each
// stored exactly once, by stores in the call's block before the call, and
// which is used only by those stores and the slice passed to this call.
// A user-created slice (ms := []...{A}; r.Use(ms...)), a spread or computed
// slice, or any later overwrite is rejected.
func varargs(v ssa.Value, call ssa.CallInstruction) (map[int64]ssa.Value, string) {
	sl, ok := v.(*ssa.Slice)
	if !ok {
		return nil, "middleware list is not written in place (spread or computed slice)"
	}
	alloc, ok := sl.X.(*ssa.Alloc)
	// Varargs arrays are always heap-allocated by the builder; aliasing is
	// excluded below by requiring that the array and slice have no other use.
	if !ok || alloc.Comment != "varargs" || sl.Low != nil || sl.High != nil || sl.Max != nil {
		return nil, "middleware list is a user-created slice, not in-place arguments (its elements can be overwritten)"
	}
	slRefs := *sl.Referrers()
	if len(slRefs) != 1 || slRefs[0] != call.(ssa.Instruction) {
		return nil, "middleware slice is used other than by this call"
	}
	callBlock, callIdx := call.Block(), indexIn(call.(ssa.Instruction))
	elems := map[int64]ssa.Value{}
	for _, ref := range *alloc.Referrers() {
		if ref == ssa.Instruction(sl) {
			continue
		}
		ia, ok := ref.(*ssa.IndexAddr)
		if !ok {
			return nil, "middleware slice storage is used other than by in-place stores"
		}
		idx, ok := ia.Index.(*ssa.Const)
		if !ok {
			return nil, "middleware list has a computed index"
		}
		refs := *ia.Referrers()
		if len(refs) != 1 {
			return nil, "middleware slice element is written or read more than once"
		}
		st, ok := refs[0].(*ssa.Store)
		if !ok || st.Addr != ia || st.Block() != callBlock || indexIn(st) > callIdx {
			return nil, "middleware slice element is not stored in place before the call"
		}
		if _, dup := elems[idx.Int64()]; dup {
			return nil, "middleware slice element is stored more than once"
		}
		elems[idx.Int64()] = st.Val
	}
	return elems, ""
}

func indexIn(ins ssa.Instruction) int {
	for i, x := range ins.Block().Instrs {
		if x == ins {
			return i
		}
	}
	return -1
}

// directFunc resolves a direct reference to a top-level function or a method
// value (bound method wrapper) and returns the canonical symbol ID.
func directFunc(v ssa.Value) (string, bool) {
	switch t := peel(v).(type) {
	case *ssa.Function:
		if t.Parent() == nil && t.Synthetic == "" {
			return t.String(), true
		}
	case *ssa.MakeClosure:
		if f, ok := t.Fn.(*ssa.Function); ok && strings.HasSuffix(f.String(), "$bound") {
			return strings.TrimSuffix(f.String(), "$bound"), true
		}
	}
	return "", false
}

// handler resolves a direct handler: (handler_sym, handler_value_sym).
func handler(fn *ssa.Function, v ssa.Value) (*string, *string, bool) {
	switch t := peel(v).(type) {
	case *ssa.Function:
		if t.Synthetic == "" && (t.Parent() == nil || t.Parent() != nil) {
			return facts.Str(t.String()), nil, true
		}
	case *ssa.MakeClosure:
		f, ok := t.Fn.(*ssa.Function)
		if !ok {
			return nil, nil, false
		}
		if strings.HasSuffix(f.String(), "$bound") {
			return facts.Str(strings.TrimSuffix(f.String(), "$bound")), facts.Str(f.String()), true
		}
		if f.Parent() != nil {
			return facts.Str(f.String()), nil, true
		}
	}
	return nil, nil, false
}

// isWrapperCall reports a call that wraps a handler: it takes an
// http.Handler or http.HandlerFunc argument, or it comes from chi.
func isWrapperCall(call *ssa.Call) bool {
	if callee := call.Call.StaticCallee(); callee != nil {
		if obj, _ := callee.Object().(*types.Func); obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == chiPath {
			return true
		}
	}
	for _, a := range call.Call.Args {
		if isHTTPHandler(a.Type()) || isHandlerFunc(a.Type()) {
			return true
		}
	}
	return false
}

func isHandlerFunc(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == httpPath && n.Obj().Name() == "HandlerFunc"
}

// wrappedByChi reports a bound method value whose receiver is produced by a
// chi wrapper call (Chain, Middlewares.Handler/HandlerFunc).
func wrappedByChi(v ssa.Value) bool {
	mc, ok := peel(v).(*ssa.MakeClosure)
	if !ok || len(mc.Bindings) == 0 {
		return false
	}
	call, ok := peel(mc.Bindings[0]).(*ssa.Call)
	if !ok {
		return false
	}
	callee := call.Call.StaticCallee()
	if callee == nil {
		return false
	}
	obj, _ := callee.Object().(*types.Func)
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == chiPath
}

func isMiddlewareSig(sig *types.Signature) bool {
	if sig == nil {
		return false
	}
	return sig.Params().Len() == 1 && sig.Results().Len() == 1 && !sig.Variadic() &&
		isHTTPHandler(sig.Params().At(0).Type()) && isHTTPHandler(sig.Results().At(0).Type())
}

func isHTTPHandler(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == httpPath && n.Obj().Name() == "Handler"
}

// register emits one route registration.
func (x *extractor) register(fn *ssa.Function, unit string, r *router, o *op, cond bool, loc *facts.Location) {
	if cond {
		x.diag(CatSite, unit, fn, "unsupported_router_form", o.name+" registration under a condition or in a loop", loc)
		return
	}
	if loc == nil {
		x.diag(CatSite, unit, fn, "unsupported_router_form", o.name+" registration has no resolvable source location", nil)
		return
	}
	rt := &Route{Fact: facts.Route{UnitID: unit, RegisteredIn: fn.String(), Loc: loc}}
	var patV, hV ssa.Value
	method := ""
	switch {
	case o.pkg == httpPath:
		rt.Fact.Router = "net_http"
		patV, hV = o.args[0], o.args[1]
	case o.name == "Method":
		rt.Fact.Router = "chi"
		m, ok := constString(o.args[0])
		if !ok {
			x.diag(CatSite, unit, fn, "unresolved_dynamic_route", "Method with a non-constant method", loc)
			return
		}
		method = strings.ToUpper(m)
		if !chiMethodOK(m) {
			x.diag(CatSite, unit, fn, "unsupported_router_form", fmt.Sprintf("chi rejects method %q (it would panic)", m), loc)
			return
		}
		patV, hV = o.args[1], o.args[2]
	default:
		rt.Fact.Router = "chi"
		method = fixedMethods[o.name]
		patV, hV = o.args[0], o.args[1]
	}
	raw, constPat := constString(patV)
	if constPat {
		rt.Fact.PatternRaw = facts.Str(raw)
		if o.pkg == httpPath {
			m, p, why := netHTTPPattern(r.scratch, raw)
			if why != "" {
				x.diag(CatSite, unit, fn, "unsupported_router_form", fmt.Sprintf("net/http pattern %q: %s", raw, why), loc)
				return
			}
			if m != "" {
				rt.Fact.Method = facts.Str(m)
			}
			rt.Fact.Path = facts.Str(p)
		} else {
			if !chiPatternOK(raw) {
				x.diag(CatSite, unit, fn, "unsupported_router_form", fmt.Sprintf("chi rejects pattern %q (it would panic)", raw), loc)
				return
			}
			full := normalizeChi(joinPrefix(r.prefix, raw))
			if badSegments(full) {
				x.diag(CatSite, unit, fn, "unsupported_router_form", fmt.Sprintf("path %q has empty, . or .. segments (outside the bounded subset)", full), loc)
				return
			}
			rt.Fact.Method = facts.Str(method)
			rt.Fact.Path = facts.Str(full)
		}
	} else if o.pkg != httpPath {
		rt.Fact.Method = facts.Str(method)
	}
	rt.Chain = r.chain()
	// The handler, with the net/http direct wrapper form W(h).
	hv := peel(hV)
	if call, ok := hv.(*ssa.Call); ok && !isWrapperCall(call) {
		// A plain call result (makeHandler()) is not a direct handler.
		x.diag(CatHandler, unit, fn, "unresolved_dynamic_route", o.name+" handler is a call result, not a direct handler", loc)
		hv = nil
		defer func() { x.diags[len(x.diags)-1].Routes = append(x.diags[len(x.diags)-1].Routes, rt) }()
	} else if ok {
		w := call.Call.StaticCallee()
		if o.pkg == httpPath && o.name == "Handle" && w != nil && w.Parent() == nil && w.Synthetic == "" &&
			isMiddlewareSig(w.Signature) && len(call.Call.Args) == 1 {
			if _, nested := peel(call.Call.Args[0]).(*ssa.Call); nested {
				d := x.diag(CatChain, unit, fn, "unsupported_middleware_form", "nested handler wrappers W1(W2(h)) are not the direct wrapper form", loc)
				rt.Chain = append(rt.Chain, ChainEntry{Source: "direct_wrapper", Loc: loc, Unresolved: true, DiagID: d.Diag.ID})
			} else {
				rt.Chain = append(rt.Chain, ChainEntry{Sym: w.String(), Source: "direct_wrapper", Loc: x.pos(fn, call.Pos())})
				hv = call.Call.Args[0]
			}
		} else {
			d := x.diag(CatChain, unit, fn, "unsupported_middleware_form", "the handler is wrapped by a call that is not the direct wrapper form W(h)", loc)
			rt.Chain = append(rt.Chain, ChainEntry{Source: "direct_wrapper", Loc: loc, Unresolved: true, DiagID: d.Diag.ID})
			hv = nil
		}
	}
	if hv != nil && wrappedByChi(hv) {
		// e.g. chi.Chain(mw).HandlerFunc(h).ServeHTTP: a method value on a
		// chi wrapper is a wrapped handler, not a direct one.
		d := x.diag(CatChain, unit, fn, "unsupported_middleware_form", "the handler is a method of a chi Chain/Middlewares wrapper, not a direct handler", loc)
		rt.Chain = append(rt.Chain, ChainEntry{Source: "direct_wrapper", Loc: loc, Unresolved: true, DiagID: d.Diag.ID})
		hv = nil
	}
	if hv != nil {
		rt.Fact.HandlerSym, rt.Fact.HandlerValueSym, _ = handler(fn, hv)
	}
	for _, e := range rt.Chain {
		if e.Unresolved {
			rt.Uncertain = append(rt.Uncertain, e.DiagID)
		}
	}
	// A late Use on any ancestor makes chi panic for the whole router tree.
	for a := r; a != nil; a = a.parent {
		rt.Uncertain = append(rt.Uncertain, a.lateDiags...)
		a.routes = append(a.routes, rt) // routes of a and all its descendants
	}
	r.hadRoute = true
	x.routes = append(x.routes, rt)
	// Link chain diagnostics to the route they make uncertain.
	for i := range x.diags {
		for _, id := range rt.Uncertain {
			if x.diags[i].Diag.ID == id {
				x.diags[i].Routes = append(x.diags[i].Routes, rt)
			}
		}
	}
	if !constPat {
		x.diag(CatPath, unit, fn, "unresolved_dynamic_route", o.name+" pattern is not a constant string", loc, rt)
	} else if hv != nil && rt.Fact.HandlerSym == nil {
		x.diag(CatHandler, unit, fn, "unresolved_dynamic_route", o.name+" handler is not a direct handler", loc, rt)
	}
}

func isHTTPServerHandlerField(fa *ssa.FieldAddr) bool {
	st, ok := fa.X.Type().Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := st.Elem().(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != httpPath || n.Obj().Name() != "Server" {
		return false
	}
	s := n.Underlying().(*types.Struct)
	return s.Field(fa.Field).Name() == "Handler"
}

// netHTTPPattern validates a constant pattern by replaying it on the mux's
// scratch ServeMux (so duplicates and conflicts on the same mux are caught),
// then applies the bounded subset. It returns method ("" for any) and path.
func netHTTPPattern(scratch *http.ServeMux, raw string) (method, path, why string) {
	if msg := tryRegister(func() { scratch.HandleFunc(raw, func(http.ResponseWriter, *http.Request) {}) }); msg != "" {
		return "", "", "net/http rejects it (it would panic): " + msg
	}
	rest := raw
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		method, rest = raw[:i], strings.TrimLeft(raw[i+1:], " \t")
		for _, c := range method {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
				return "", "", "method " + method + " is outside the bounded subset (letters only)"
			}
		}
	}
	if !strings.HasPrefix(rest, "/") {
		return "", "", "host patterns are outside the bounded subset"
	}
	if badSegments(rest) {
		return "", "", "empty, . or .. path segments are outside the bounded subset"
	}
	return method, rest, ""
}

func tryRegister(f func()) (msg string) {
	defer func() {
		if p := recover(); p != nil {
			msg = fmt.Sprint(p)
			if i := strings.Index(msg, " (registered at"); i >= 0 {
				msg = msg[:i] // drop analyzer-internal file paths
			}
		}
	}()
	f()
	return ""
}

func chiPatternOK(p string) bool {
	return tryRegister(func() { chi.NewRouter().Get(p, func(http.ResponseWriter, *http.Request) {}) }) == ""
}

func chiMethodOK(m string) bool {
	return tryRegister(func() { chi.NewRouter().Method(m, "/", http.NotFoundHandler()) }) == ""
}

// joinPrefix joins a Route prefix and a pattern: drop one trailing "/" from
// the prefix, then concatenate (router matrix, section 5).
func joinPrefix(prefix, p string) string {
	return strings.TrimSuffix(prefix, "/") + p
}

// normalizeChi rewrites {name:regexp} parameters to {name}.
func normalizeChi(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			b.WriteByte(p[i])
			continue
		}
		depth, j := 1, i+1
		for ; j < len(p) && depth > 0; j++ {
			switch p[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		inner := p[i+1 : j-1]
		if k := strings.IndexByte(inner, ':'); k >= 0 {
			inner = inner[:k]
		}
		b.WriteString("{" + inner + "}")
		i = j - 1
	}
	return b.String()
}

func badSegments(p string) bool {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segs {
		if s == "." || s == ".." || (s == "" && i != len(segs)-1) {
			return true
		}
	}
	return false
}

// attachRoutes runs extraction and puts its facts into the unit shards:
// routes with stable IDs and duplicate indexes, resolved middleware, the
// closure and wrapper symbols they reference, external-symbol entries and
// the route diagnostics.
func (res *Result) attachRoutes(all []*packages.Package, l *locator) {
	module := map[string]bool{}
	for id, u := range res.Units {
		if u.Origin == "module" {
			module[id] = true
		}
	}
	x := extractRoutes(all, module, l)
	for unit, syms := range x.closures {
		if u := res.Units[unit]; u != nil {
			u.Shard.Symbols = append(u.Shard.Symbols, syms...)
		}
	}
	owner := map[string]string{}
	for id, u := range res.Units {
		for _, s := range u.Shard.Symbols {
			owner[s.ID] = id
		}
	}
	// Every function a fact refers to must be a symbol of its owning unit:
	// closure parents, registered_in and diagnostic symbols. The package
	// initializer (pkg.init) and declared init functions (pkg.init#n) are not
	// declared by go/types, so they are added here from SSA.
	needed := map[string]bool{}
	for pid := range x.parents {
		needed[pid] = true
	}
	for _, rt := range x.routes {
		needed[rt.Fact.RegisteredIn] = true
	}
	for _, d := range x.diags {
		if d.Diag.SymbolID != nil {
			needed[*d.Diag.SymbolID] = true
		}
	}
	var parentIDs []string
	for pid := range needed {
		parentIDs = append(parentIDs, pid)
	}
	sort.Strings(parentIDs)
	for _, pid := range parentIDs {
		if _, ok := owner[pid]; ok {
			continue
		}
		pf := x.fnByID[pid]
		if pf == nil || pf.Pkg == nil {
			continue
		}
		unit := pf.Pkg.Pkg.Path()
		u := res.Units[unit]
		if u == nil {
			continue
		}
		u.Shard.Symbols = append(u.Shard.Symbols, facts.Symbol{
			ID: pid, UnitID: unit, Kind: "func", Name: pf.Name(), HasBody: true, InUniverse: true,
			EntryHints: facts.EntryHints{IsInit: strings.HasPrefix(pf.Name(), "init")}, Loc: x.pos(pf, pf.Pos()),
		})
		owner[pid] = unit
	}
	// $bound wrappers used as handler values are owned by the method's unit.
	for _, rt := range x.routes {
		if rt.Fact.HandlerValueSym == nil {
			continue
		}
		w := *rt.Fact.HandlerValueSym
		m := strings.TrimSuffix(w, "$bound")
		ou, ok := owner[m]
		if !ok {
			rt.Fact.HandlerSym, rt.Fact.HandlerValueSym = nil, nil
			continue
		}
		if _, done := owner[w]; !done {
			res.Units[ou].Shard.Symbols = append(res.Units[ou].Shard.Symbols, facts.Symbol{
				ID: w, UnitID: ou, Kind: "wrapper", Name: w[strings.LastIndex(w, ".")+1:], Exported: token.IsExported(m[strings.LastIndex(m, ".")+1:]),
				HasBody: true, InUniverse: true, OriginID: facts.Str(m), WrapperKind: facts.Str("bound"),
				EntryHints: facts.EntryHints{AddressTaken: true},
			})
			owner[w] = ou
		}
	}
	// Duplicate indexes per (unit, method, path) in location order.
	type key struct{ unit, method, path string }
	groups := map[key][]*Route{}
	for _, rt := range x.routes {
		k := key{rt.Fact.UnitID, deref(rt.Fact.Method, "*"), deref(rt.Fact.Path, "")}
		groups[k] = append(groups[k], rt)
	}
	for k, rs := range groups {
		sort.SliceStable(rs, func(i, j int) bool { return locKey(rs[i].Fact.Loc) < locKey(rs[j].Fact.Loc) })
		for i, rt := range rs {
			rt.Fact.DuplicateIndex = i
			rt.Fact.ID = fmt.Sprintf("route:%s:%s:%s#%d", k.unit, k.method, k.path, i)
			if i > 0 {
				x.diag(CatDuplicate, k.unit, nil, "duplicate_route",
					fmt.Sprintf("%s %s is registered more than once in this unit; each registration is checked separately", k.method, k.path), rt.Fact.Loc)
			}
		}
	}
	for _, rt := range x.routes {
		u := res.Units[rt.Fact.UnitID]
		if u == nil {
			continue
		}
		u.Shard.Routes = append(u.Shard.Routes, rt.Fact)
		for i, e := range rt.Chain {
			if e.Unresolved {
				continue
			}
			if _, ok := owner[e.Sym]; !ok {
				// Not a symbol of any loaded unit (should not happen for a
				// direct reference); keep the chain conservative.
				d := x.diag(CatChain, rt.Fact.UnitID, nil, "unsupported_middleware_form", "middleware "+e.Sym+" is not a known symbol", e.Loc, rt)
				rt.Chain[i].Unresolved, rt.Chain[i].DiagID = true, d.Diag.ID
				rt.Uncertain = append(rt.Uncertain, d.Diag.ID)
				continue
			}
			loc := e.Loc
			if loc == nil {
				loc = rt.Fact.Loc
			}
			u.Shard.RouteMiddleware = append(u.Shard.RouteMiddleware, facts.RouteMiddleware{
				RouteID: rt.Fact.ID, MiddlewareSym: e.Sym, Order: i, Source: e.Source, Loc: loc})
		}
	}
	for _, d := range x.diags {
		if u := res.Units[d.Unit]; u != nil {
			u.Shard.Diagnostics = append(u.Shard.Diagnostics, d.Diag)
		}
	}
	// Normalize every shard: sorted, deduplicated, external symbols.
	for id, u := range res.Units {
		sh := u.Shard
		sort.Slice(sh.Symbols, func(i, j int) bool { return sh.Symbols[i].ID < sh.Symbols[j].ID })
		sort.Slice(sh.Routes, func(i, j int) bool { return sh.Routes[i].ID < sh.Routes[j].ID })
		sort.Slice(sh.RouteMiddleware, func(i, j int) bool {
			a, b := sh.RouteMiddleware[i], sh.RouteMiddleware[j]
			if a.RouteID != b.RouteID {
				return a.RouteID < b.RouteID
			}
			return a.Order < b.Order
		})
		seen := map[string]bool{}
		diags := sh.Diagnostics[:0]
		for _, d := range sh.Diagnostics {
			if !seen[d.ID] {
				seen[d.ID] = true
				diags = append(diags, d)
			}
		}
		sh.Diagnostics = diags
		sortDiags(sh.Diagnostics)
		ext := map[string]string{}
		ref := func(s *string) {
			if s != nil && owner[*s] != "" && owner[*s] != id {
				ext[*s] = owner[*s]
			}
		}
		for _, s := range sh.Symbols {
			ref(s.ParentID)
			ref(s.OriginID)
		}
		for _, r := range sh.Routes {
			ref(r.HandlerSym)
			ref(r.HandlerValueSym)
			ref(&r.RegisteredIn)
		}
		for _, m := range sh.RouteMiddleware {
			ref(&m.MiddlewareSym)
		}
		for _, d := range sh.Diagnostics {
			ref(d.SymbolID)
		}
		sh.ExternalSymbols = []facts.ExtSymbol{}
		for sid, ou := range ext {
			sh.ExternalSymbols = append(sh.ExternalSymbols, facts.ExtSymbol{ID: sid, UnitID: ou})
		}
		sort.Slice(sh.ExternalSymbols, func(i, j int) bool { return sh.ExternalSymbols[i].ID < sh.ExternalSymbols[j].ID })
	}
	res.Routes = x.routes
	res.RouteDiags = x.diags
	sort.SliceStable(res.Routes, func(i, j int) bool { return res.Routes[i].Fact.ID < res.Routes[j].Fact.ID })
	// Middleware shape of every declared function and method, for policy
	// validation of middleware concepts.
	res.MiddlewareShaped = map[string]bool{}
	for _, p := range all {
		if p.Types == nil {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			switch o := scope.Lookup(name).(type) {
			case *types.Func:
				res.MiddlewareShaped[o.FullName()] = isMiddlewareSig(o.Type().(*types.Signature))
			case *types.TypeName:
				if n, ok := o.Type().(*types.Named); ok {
					for i := 0; i < n.NumMethods(); i++ {
						m := n.Method(i)
						res.MiddlewareShaped[m.FullName()] = isMiddlewareSig(m.Type().(*types.Signature))
					}
				}
			}
		}
	}
}

func deref(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

func locKey(l *facts.Location) string {
	if l == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%012d\x00%012d", l.File, l.Line, l.Column)
}

func newScratch() *http.ServeMux { return http.NewServeMux() }
