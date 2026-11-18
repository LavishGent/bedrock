package analyze

// The router audit: origin allowlist + use allowlist (router matrix, section
// 2, "Router value audit"). Closed world: a router is trusted only if the
// analyzer can see where it was made and everything done with it.
//
// Facts per router-bearing SSA value (computed to a greatest fixed point):
//
//   covered: its origin is a recognized constructor call (chi.NewRouter,
//            chi.NewMux, http.NewServeMux) in an analyzed module function, a
//            With/Group/Route result on a covered router, or the result of an
//            audited module function whose every return (at that index) is
//            covered, or a parameter of a closed module function (unexported,
//            package-level, never used as a value) whose every call site
//            passes a covered value, or the parameter of a Group/Route literal
//            on a covered router.
//   proven:  additionally, its concrete type is exactly *http.ServeMux or
//            *chi.Mux; proof survives only MakeInterface and ChangeInterface
//            (and audited factories/closed helpers whose every flow is
//            proven); a phi, an interface parameter of an open function or a
//            defined/embedding type never has it.
//
// Every other router-bearing value is an UNCOVERED SOURCE: a zero value or
// new() allocation, any global, a parameter of an open function, a call
// result from dependency or unanalyzed code, a field load, an index or map
// read, a type assertion, a closure free variable. Each one is module-wide
// unsupported_router_form at its origin, regardless of later use.
//
// Uses (of covered and uncovered values alike) must be on the allowlist; see
// allowed. Every other use is module-wide unsupported_router_form.

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// routerUnderlyingTypes returns the underlying types of http.ServeMux and
// chi.Mux from the loaded packages.
func routerUnderlyingTypes(all []*packages.Package) []types.Type {
	var out []types.Type
	packages.Visit(all, nil, func(p *packages.Package) {
		if p.Types == nil {
			return
		}
		var name string
		switch p.PkgPath {
		case httpPath:
			name = "ServeMux"
		case chiPath:
			name = "Mux"
		default:
			return
		}
		if o := p.Types.Scope().Lookup(name); o != nil {
			out = append(out, o.Type().Underlying())
		}
	})
	return out
}

// routerish reports a type that may hold a router: a recognized router type,
// or a defined type (or pointer to one) convertible to a router.
func (x *extractor) routerish(t types.Type) bool {
	if isRouterValueType(t) {
		return true
	}
	n, ok := named(t)
	if !ok {
		return false
	}
	for _, u := range x.routerUnderlying {
		if types.Identical(n.Underlying(), u) {
			return true
		}
	}
	return false
}

// concreteRouter reports a static type that is exactly *http.ServeMux or
// *chi.Mux (a pointer to the recognized named type, not a defined type).
func concreteRouter(t types.Type) bool {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	path, name := n.Obj().Pkg().Path(), n.Obj().Name()
	return (path == httpPath && name == "ServeMux") || (path == chiPath && name == "Mux")
}

// isConstructor reports a call to a recognized router constructor.
func isConstructor(c *ssa.CallCommon) bool {
	callee := c.StaticCallee()
	if callee == nil || c.IsInvoke() {
		return false
	}
	obj, _ := callee.Object().(*types.Func)
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	name, path := obj.Name(), obj.Pkg().Path()
	return (path == chiPath && (name == "NewRouter" || name == "NewMux")) || (path == httpPath && name == "NewServeMux")
}

type facts2 struct{ covered, proven bool }

// auditState is the interprocedural part of the fixed point.
type auditState struct {
	audited map[*ssa.Function]bool
	closed  map[*ssa.Function]bool           // unexported package-level func never used as a value
	params  map[*ssa.Parameter]bool          // non-router params that receive a tracked value
	paramF  map[*ssa.Parameter]facts2        // closed-function params: AND over call sites
	nestedF map[*ssa.Function]facts2         // Group/Route literal params: AND over receivers
	retF    map[*ssa.Function]map[int]facts2 // audited functions: AND over returns
	changed bool
}

func and(a, b facts2) facts2 { return facts2{a.covered && b.covered, a.proven && b.proven} }

var top = facts2{true, true}

// fnAudit is the per-function analysis.
type fnAudit struct {
	x       *extractor
	fn      *ssa.Function
	st      *auditState
	tracked map[ssa.Value]bool
	memo    map[ssa.Value]facts2
	busy    map[ssa.Value]bool
}

func (x *extractor) analyze(fn *ssa.Function, st *auditState) *fnAudit {
	a := &fnAudit{x: x, fn: fn, st: st, tracked: map[ssa.Value]bool{}, memo: map[ssa.Value]facts2{}, busy: map[ssa.Value]bool{}}
	var work []ssa.Value
	add := func(v ssa.Value) {
		if v != nil && !a.tracked[v] {
			a.tracked[v] = true
			work = append(work, v)
		}
	}
	for _, p := range fn.Params {
		if x.routerish(p.Type()) || st.params[p] {
			add(p)
		}
	}
	for _, fv := range fn.FreeVars {
		if x.routerish(fv.Type()) {
			add(fv)
		}
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			// Operands: globals (var G http.ServeMux) are values, not results.
			for _, op := range ins.Operands(nil) {
				if op == nil || *op == nil {
					continue
				}
				if g, ok := (*op).(*ssa.Global); ok && x.routerish(g.Type()) {
					add(g)
				}
			}
			v, ok := ins.(ssa.Value)
			if !ok {
				continue
			}
			if x.routerish(v.Type()) {
				add(v)
				continue
			}
			call, ok := ins.(*ssa.Call)
			if !ok {
				continue
			}
			f := call.Call.StaticCallee()
			if f == nil || st.retF[f] == nil {
				continue
			}
			if call.Call.Signature().Results().Len() == 1 {
				if _, ok := st.retF[f][0]; ok {
					add(call)
				}
				continue
			}
			for _, ref := range *call.Referrers() {
				if e, ok := ref.(*ssa.Extract); ok {
					if _, ok := st.retF[f][e.Index]; ok {
						add(e)
					}
				}
			}
		}
	}
	for len(work) > 0 {
		v := work[len(work)-1]
		work = work[:len(work)-1]
		if v.Referrers() == nil {
			continue
		}
		for _, ref := range *v.Referrers() {
			switch r := ref.(type) {
			case *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.ChangeType, *ssa.Convert, *ssa.TypeAssert, *ssa.Phi:
				add(r.(ssa.Value))
			}
		}
	}
	return a
}

// facts computes covered/proven for a tracked (or constant) value.
func (a *fnAudit) facts(v ssa.Value) facts2 {
	if f, ok := a.memo[v]; ok {
		return f
	}
	if a.busy[v] {
		return top // greatest fixed point inside phi cycles
	}
	a.busy[v] = true
	f := a.compute(v)
	delete(a.busy, v)
	a.memo[v] = f
	return f
}

func (a *fnAudit) compute(v ssa.Value) facts2 {
	st := a.st
	switch t := v.(type) {
	case *ssa.Const:
		return facts2{true, false} // nil: carries no router
	case *ssa.Parameter:
		if a.x.nested[a.fn] && len(a.fn.Params) > 0 && t == a.fn.Params[0] {
			return st.nestedF[a.fn]
		}
		if st.closed[a.fn] {
			if f, ok := st.paramF[t]; ok {
				return f
			}
			return top // never called: vacuous
		}
		return facts2{}
	case *ssa.Call:
		c := &t.Call
		if isConstructor(c) {
			return facts2{true, concreteRouter(t.Type())}
		}
		if o := classify(c); o != nil && !o.isFunc && (o.name == "With" || o.name == "Group" || o.name == "Route") {
			rf := a.facts(o.recv)
			return facts2{rf.covered, rf.covered}
		}
		if f := c.StaticCallee(); f != nil && st.audited[f] && !c.IsInvoke() {
			if r, ok := st.retF[f][0]; ok {
				return r
			}
			return top // not yet computed: greatest fixed point, refined later
		}
		return facts2{}
	case *ssa.Extract:
		if call, ok := t.Tuple.(*ssa.Call); ok {
			if f := call.Call.StaticCallee(); f != nil && st.audited[f] && !call.Call.IsInvoke() {
				if r, ok := st.retF[f][t.Index]; ok {
					return r
				}
				return top
			}
		}
		return facts2{}
	case *ssa.MakeInterface:
		return a.facts(t.X)
	case *ssa.ChangeInterface:
		return a.facts(t.X)
	case *ssa.ChangeType:
		f := a.facts(t.X)
		return facts2{f.covered && isRouterValueType(t.Type()), f.proven && concreteRouter(t.Type())}
	case *ssa.Phi:
		f := facts2{true, false} // a phi drops the proof
		for _, e := range t.Edges {
			f.covered = f.covered && a.facts(e).covered
		}
		return f
	}
	return facts2{} // allocation, global, load, field, index, lookup, type assertion, free var, convert
}

// isSource reports a value that is an origin rather than a derivation.
func isSource(v ssa.Value) bool {
	switch v.(type) {
	case *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.ChangeType, *ssa.Phi, *ssa.Const:
		return false
	}
	return true
}

func sourceKind(v ssa.Value) string {
	switch v.(type) {
	case *ssa.Alloc:
		return "a zero-value or new() allocation"
	case *ssa.Global:
		return "a global"
	case *ssa.Parameter:
		return "a parameter of a function callable from outside the audited call graph"
	case *ssa.FreeVar:
		return "a closure free variable"
	case *ssa.Call, *ssa.Extract:
		return "a call result from code that is not an audited module factory of covered routers"
	case *ssa.UnOp:
		return "a load through a pointer"
	case *ssa.Field, *ssa.FieldAddr:
		return "a field"
	case *ssa.TypeAssert:
		return "a type assertion"
	case *ssa.Index, *ssa.IndexAddr, *ssa.Lookup:
		return "an index or map read"
	case *ssa.Convert:
		return "a conversion"
	}
	return "an unrecognized origin"
}

// audit runs the fixed point, then reports uncovered origins and every use
// outside the allowlist.
func (x *extractor) audit(fns []*ssa.Function) {
	st := &auditState{audited: map[*ssa.Function]bool{}, closed: map[*ssa.Function]bool{},
		params: map[*ssa.Parameter]bool{}, paramF: map[*ssa.Parameter]facts2{},
		nestedF: map[*ssa.Function]facts2{}, retF: map[*ssa.Function]map[int]facts2{}}
	for _, fn := range fns {
		st.audited[fn] = true
	}
	// Closed functions: unexported, package-level, not methods, never used as
	// a value anywhere in module code.
	valueUse := map[*ssa.Function]bool{}
	for _, fn := range fns {
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				var callee ssa.Value
				if ci, ok := ins.(ssa.CallInstruction); ok && !ci.Common().IsInvoke() {
					callee = ci.Common().Value
				}
				for _, op := range ins.Operands(nil) {
					if op != nil && *op != nil && *op != callee {
						if f, ok := (*op).(*ssa.Function); ok {
							valueUse[f] = true
						}
					}
				}
			}
		}
	}
	for _, fn := range fns {
		if fn.Parent() == nil && fn.Signature.Recv() == nil && fn.Synthetic == "" && !valueUse[fn] &&
			!token.IsExported(fn.Name()) && fn.Name() != "init" && fn.Name() != "main" {
			st.closed[fn] = true
		}
	}
	for iter := 0; iter < 100; iter++ {
		st.changed = false
		for _, fn := range fns {
			x.step(fn, st)
		}
		if !st.changed {
			break
		}
		if iter == 99 { // did not converge: fail closed
			for f := range st.retF {
				for i := range st.retF[f] {
					st.retF[f][i] = facts2{}
				}
			}
		}
	}
	for _, fn := range fns {
		a := x.analyze(fn, st)
		unit := fn.Pkg.Pkg.Path()
		report := func(pos token.Pos, why string) {
			if !pos.IsValid() {
				pos = fn.Pos()
			}
			x.diag(CatIdentity, unit, fn, "unsupported_router_form", why, x.pos(fn, pos))
		}
		// Origins.
		for v := range a.tracked {
			if !isSource(v) || a.facts(v).covered {
				continue
			}
			if !x.routerish(v.Type()) {
				if _, isParam := v.(*ssa.Parameter); !isParam {
					continue // a non-router value is tracked only through a tracked flow
				}
			}
			pos := v.Pos()
			if g, ok := v.(*ssa.Global); ok {
				// Report at each use site of the global in this function.
				for _, b := range fn.Blocks {
					for _, ins := range b.Instrs {
						for _, op := range ins.Operands(nil) {
							if op != nil && *op == ssa.Value(g) {
								report(ins.Pos(), "router of unknown origin ("+sourceKind(v)+" "+g.String()+"); routes registered on it cannot be identified")
							}
						}
					}
				}
				continue
			}
			report(pos, "router of unknown origin ("+sourceKind(v)+"); routes registered on it cannot be identified")
		}
		// Uses.
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				for _, op := range ins.Operands(nil) {
					if op == nil || *op == nil || !a.tracked[*op] {
						continue
					}
					if why := a.allowed(*op, ins); why != "" {
						report(ins.Pos(), "a router value is used outside the router audit allowlist ("+why+"); routes registered or served through it cannot be identified")
					}
				}
			}
		}
	}
}

// step recomputes one function's contribution to the interprocedural facts.
func (x *extractor) step(fn *ssa.Function, st *auditState) {
	a := x.analyze(fn, st)
	set := func(cur facts2, ok bool, nf facts2) (facts2, bool) {
		if !ok {
			return nf, true
		}
		m := and(cur, nf)
		return m, m != cur
	}
	for v := range a.tracked {
		if v.Referrers() == nil {
			continue
		}
		for _, ref := range *v.Referrers() {
			switch r := ref.(type) {
			case *ssa.Return:
				for i, res := range r.Results {
					if res != v {
						continue
					}
					if st.retF[fn] == nil {
						st.retF[fn] = map[int]facts2{}
					}
					cur, ok := st.retF[fn][i]
					if !ok {
						cur = top
					}
					if n, ch := set(cur, ok, and(cur, a.facts(v))); ch {
						st.retF[fn][i] = n
						st.changed = true
					}
				}
			case ssa.CallInstruction:
				c := r.Common()
				if o := classify(c); o != nil && !o.isFunc && o.recv == v && (o.name == "Group" || o.name == "Route") {
					if lit := literal(fn, o.args[len(o.args)-1]); lit != nil {
						cur, ok := st.nestedF[lit]
						if !ok {
							cur = top
						}
						if n, ch := set(cur, ok, and(cur, a.facts(v))); ch {
							st.nestedF[lit] = n
							st.changed = true
						}
					}
				}
				callee := c.StaticCallee()
				if c.IsInvoke() || callee == nil || !st.audited[callee] {
					continue
				}
				for i, arg := range c.Args {
					if arg != v || i >= len(callee.Params) {
						continue
					}
					p := callee.Params[i]
					if !x.routerish(p.Type()) && !st.params[p] {
						st.params[p] = true
						st.changed = true
					}
					if st.closed[callee] {
						cur, ok := st.paramF[p]
						if !ok {
							cur = top
						}
						if n, ch := set(cur, ok, and(cur, a.facts(v))); ch {
							st.paramF[p] = n
							st.changed = true
						}
					}
				}
			}
		}
	}
}

// servingArg is the handler argument position of each trusted serving API.
var servingArg = map[string]map[string]int{
	httpPath:            {"ListenAndServe": 1, "ListenAndServeTLS": 3, "Serve": 1, "ServeTLS": 1},
	"net/http/httptest": {"NewServer": 0, "NewUnstartedServer": 0, "NewTLSServer": 0},
}

// serverMethods are the only uses of a local http.Server holding a router.
var serverMethods = map[string]bool{"ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true, "Shutdown": true, "Close": true}

// allowed returns "" if the use of tracked value v by ins is on the
// allowlist, or why it is not.
func (a *fnAudit) allowed(v ssa.Value, ins ssa.Instruction) string {
	x, st := a.x, a.st
	proven := a.facts(v).proven
	switch r := ins.(type) {
	case *ssa.DebugRef, *ssa.Return:
		return ""
	case *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.Phi:
		return "" // the result is tracked and audited itself
	case *ssa.ChangeType:
		if isRouterValueType(r.Type()) {
			return ""
		}
		return "converted to " + r.Type().String() + ", a type other than a recognized router"
	case *ssa.BinOp:
		if r.Op == token.EQL || r.Op == token.NEQ {
			return ""
		}
		return "used in an expression"
	case *ssa.Store:
		if fa, ok := r.Addr.(*ssa.FieldAddr); ok && r.Val == v && isHTTPServerHandlerField(fa) {
			if !proven {
				return "stored as an http.Server Handler without a proven concrete router type"
			}
			if why := localServer(fa.X); why != "" {
				return "stored as the Handler of an http.Server that " + why
			}
			return ""
		}
		return "stored into a variable, field, slice, map or global"
	case ssa.CallInstruction:
		c := r.Common()
		// Dispatch first: ServeHTTP (and read-only methods) need the proof,
		// whatever API it is called through.
		name := ""
		if c.IsInvoke() {
			name = c.Method.Name()
		} else if f := c.StaticCallee(); f != nil {
			if obj, _ := f.Object().(*types.Func); obj != nil {
				name = obj.Name()
			}
		}
		recv := (c.IsInvoke() && c.Value == v) || (!c.IsInvoke() && len(c.Args) > 0 && c.Args[0] == v && c.Signature().Recv() != nil)
		if recv && (name == "ServeHTTP" || readOnlyChi[name] || name == "Handler") {
			if o := classify(c); o != nil || name == "ServeHTTP" || isServeMuxMethod(c) {
				if proven {
					return ""
				}
				return name + " on a router whose concrete type is not proven to be *http.ServeMux or *chi.Mux"
			}
		}
		if o := classify(c); o != nil && !o.isFunc && o.recv == v && !readOnlyChi[o.name] {
			for _, arg := range o.args {
				if arg == v {
					return "passed as an argument of a router operation"
				}
			}
			return "" // registration, Use, With, Group, Route (identity checked by extraction)
		}
		if c.IsInvoke() {
			if c.Value == v {
				return "method " + c.Method.Name() + " called through an interface whose concrete type is not proven"
			}
			return "passed to an interface method call"
		}
		callee := c.StaticCallee()
		if callee == nil {
			return "passed to a call through a function value"
		}
		if obj, _ := callee.Object().(*types.Func); obj != nil && obj.Pkg() != nil {
			if pos, ok := servingArg[obj.Pkg().Path()][obj.Name()]; ok && obj.Type().(*types.Signature).Recv() == nil {
				if pos >= len(c.Args) || c.Args[pos] != v {
					return "passed to " + obj.Name() + " in a position other than its handler"
				}
				if !proven {
					return "served by " + obj.Name() + " without a proven concrete router type"
				}
				return ""
			}
		}
		if st.audited[callee] {
			for i, arg := range c.Args {
				if arg == v && i < len(callee.Params) && (st.params[callee.Params[i]] || x.routerish(callee.Params[i].Type())) {
					return ""
				}
			}
		}
		return "passed to " + callee.String() + ", which is not an audited module function receiving it"
	}
	return "used by " + instrName(ins)
}

func isServeMuxMethod(c *ssa.CallCommon) bool {
	f := c.StaticCallee()
	return f != nil && f.Signature.Recv() != nil && isServeMux(f.Signature.Recv().Type())
}

// localServer reports why an http.Server holding a router is not the narrow
// allowed pattern: a local allocation, its fields only written, and used only
// as the receiver of ListenAndServe/ListenAndServeTLS/Serve/ServeTLS/
// Shutdown/Close. "" means allowed.
func localServer(v ssa.Value) string {
	alloc, ok := v.(*ssa.Alloc)
	if !ok {
		return "is not a local allocation"
	}
	for _, ref := range *alloc.Referrers() {
		switch r := ref.(type) {
		case *ssa.DebugRef:
		case *ssa.FieldAddr:
			for _, fr := range *r.Referrers() {
				if st, ok := fr.(*ssa.Store); !ok || st.Addr != r {
					return "has a field read or otherwise used (only field writes are allowed)"
				}
			}
		case ssa.CallInstruction:
			c := r.Common()
			f := c.StaticCallee()
			if c.IsInvoke() || f == nil || len(c.Args) == 0 || c.Args[0] != alloc || f.Signature.Recv() == nil {
				return "escapes (passed, returned or stored)"
			}
			obj, _ := f.Object().(*types.Func)
			if obj == nil || !serverMethods[obj.Name()] {
				return "is used other than by ListenAndServe/ListenAndServeTLS/Serve/ServeTLS/Shutdown/Close"
			}
			for _, arg := range c.Args[1:] {
				if arg == alloc {
					return "escapes"
				}
			}
		default:
			return "escapes (passed, returned or stored)"
		}
	}
	return ""
}

func instrName(ins ssa.Instruction) string {
	switch ins.(type) {
	case *ssa.MakeClosure:
		return "a closure capturing it"
	case *ssa.Send:
		return "a channel send"
	case *ssa.MapUpdate:
		return "a map update"
	case *ssa.Convert:
		return "a conversion (unsafe.Pointer or numeric)"
	case *ssa.TypeAssert:
		return "a type assertion"
	case *ssa.FieldAddr, *ssa.Field:
		return "a field access on the router"
	case *ssa.UnOp:
		return "a dereference or other unary operation"
	case *ssa.Slice, *ssa.IndexAddr, *ssa.Index, *ssa.Lookup:
		return "an index or slice operation"
	case *ssa.Panic:
		return "a panic value"
	}
	return "an unmodeled instruction"
}
