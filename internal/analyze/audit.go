package analyze

// The supported router form (router matrix, section 2, "Supported router
// form"; owner decision 2026-11-18). require_middleware trusts a router only
// when it is created and fully populated inside ONE function:
//
//   - created by a direct call to chi.NewRouter, chi.NewMux or
//     http.NewServeMux in that function (or it is the With/Group/Route result
//     of such a router, or the parameter of a Group/Route literal given to a
//     recognized op on such a router);
//   - used only as the receiver of recognized ops, returned from that
//     function, passed directly to an enumerated serving API (with a proven
//     concrete *http.ServeMux or *chi.Mux, at the handler position), or
//     stored as the Handler of a local http.Server that is only served.
//
// A caller of a module function that returns a router may only serve the
// result directly (serving API or local http.Server), and only if every
// return of that function is a router it created in the form. Every other
// router-bearing value or use is module-wide unsupported_router_form at that
// point: parameters, globals (including DefaultServeMux), allocations, field
// or index loads, type assertions, call results from anywhere else (including
// module helpers and factories that do not satisfy the form), conversions,
// storage, capture, passing to any other call, and functions exposed through
// go:linkname. There is no interprocedural coverage summary; the only
// whole-module fact is "may this function return a router" (a monotone
// boolean), used to recognize router-bearing call results whatever their
// static type.

import (
	"go/token"
	"go/types"
	"strings"

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

// isConstructor reports a direct call to a recognized router constructor.
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

// state is what the whole-module pass needs.
type state struct {
	audited  map[*ssa.Function]bool
	mayRet   map[*ssa.Function]map[int]bool // result indexes that may carry a router
	linkname map[*ssa.Function]bool         // exposed through //go:linkname
}

// kind of a router-bearing value in one function.
type kind int

const (
	kCovered    kind = iota // created in the form in this function
	kServedOnly             // result of a form-satisfying module function: may only be served
	kUncovered              // anything else
)

type valInfo struct {
	k      kind
	proven bool // concrete *http.ServeMux or *chi.Mux
}

type fnCheck struct {
	x       *extractor
	fn      *ssa.Function
	st      *state
	tracked map[ssa.Value]bool
	info    map[ssa.Value]valInfo
	busy    map[ssa.Value]bool
}

// track collects the router-bearing values of fn: router-typed results,
// parameters, free variables and global operands, call results of module
// functions that may return a router, and everything derived from them.
func (x *extractor) track(fn *ssa.Function, st *state) *fnCheck {
	c := &fnCheck{x: x, fn: fn, st: st, tracked: map[ssa.Value]bool{}, info: map[ssa.Value]valInfo{}, busy: map[ssa.Value]bool{}}
	var work []ssa.Value
	add := func(v ssa.Value) {
		if v != nil && !c.tracked[v] {
			c.tracked[v] = true
			work = append(work, v)
		}
	}
	for _, p := range fn.Params {
		if x.routerish(p.Type()) {
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
			for _, op := range ins.Operands(nil) {
				if op != nil && *op != nil {
					if g, ok := (*op).(*ssa.Global); ok && x.routerish(g.Type()) {
						add(g)
					}
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
			if f == nil || call.Call.IsInvoke() || st.mayRet[f] == nil {
				continue
			}
			if call.Call.Signature().Results().Len() == 1 {
				if st.mayRet[f][0] {
					add(call)
				}
				continue
			}
			for _, ref := range *call.Referrers() {
				if e, ok := ref.(*ssa.Extract); ok && st.mayRet[f][e.Index] {
					add(e)
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
	return c
}

// infoOf classifies a tracked value (or a constant).
func (c *fnCheck) infoOf(v ssa.Value) valInfo {
	if i, ok := c.info[v]; ok {
		return i
	}
	if c.busy[v] {
		return valInfo{k: kUncovered} // a cycle through phis: no constructor proves it
	}
	c.busy[v] = true
	i := c.classify(v)
	delete(c.busy, v)
	c.info[v] = i
	return i
}

func (c *fnCheck) classify(v ssa.Value) valInfo {
	x, st := c.x, c.st
	switch t := v.(type) {
	case *ssa.Const:
		return valInfo{k: kCovered} // nil carries no router
	case *ssa.Parameter:
		// The parameter of a Group/Route literal given to a recognized op on
		// a covered router in the same function chain.
		if x.nested[c.fn] && len(c.fn.Params) > 0 && t == c.fn.Params[0] && x.nestedCovered(c.fn, st) {
			return valInfo{k: kCovered}
		}
		return valInfo{k: kUncovered}
	case *ssa.Call:
		cc := &t.Call
		if isConstructor(cc) {
			return valInfo{k: kCovered, proven: concreteRouter(t.Type())}
		}
		if o := classify(cc); o != nil && !o.isFunc && (o.name == "With" || o.name == "Group" || o.name == "Route") {
			if c.tracked[o.recv] && c.infoOf(o.recv).k == kCovered {
				return valInfo{k: kCovered}
			}
			return valInfo{k: kUncovered}
		}
		if f := cc.StaticCallee(); f != nil && !cc.IsInvoke() && x.formFactory(f, 0, st) {
			return valInfo{k: kServedOnly, proven: true}
		}
		return valInfo{k: kUncovered}
	case *ssa.Extract:
		if call, ok := t.Tuple.(*ssa.Call); ok {
			if f := call.Call.StaticCallee(); f != nil && !call.Call.IsInvoke() && x.formFactory(f, t.Index, st) {
				return valInfo{k: kServedOnly, proven: true}
			}
		}
		return valInfo{k: kUncovered}
	case *ssa.MakeInterface:
		return c.infoOf(t.X)
	case *ssa.ChangeInterface:
		return c.infoOf(t.X)
	case *ssa.ChangeType:
		i := c.infoOf(t.X)
		if !isRouterValueType(t.Type()) {
			i.k = kUncovered
		}
		i.proven = i.proven && concreteRouter(t.Type())
		return i
	case *ssa.Phi:
		out := valInfo{k: kCovered} // a phi drops the proof
		for _, e := range t.Edges {
			ei := c.infoOf(e)
			if ei.k > out.k {
				out.k = ei.k
			}
		}
		return out
	}
	return valInfo{k: kUncovered}
}

// isOrigin reports a value that is a source rather than a derivation.
func isOrigin(v ssa.Value) bool {
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
		return "a parameter"
	case *ssa.FreeVar:
		return "a closure free variable"
	case *ssa.Call, *ssa.Extract:
		return "a call result that is not a router created in the supported form"
	case *ssa.UnOp:
		return "a load (global or pointer, e.g. DefaultServeMux)"
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

// nestedCovered reports whether every Group/Route op that receives lit has
// a covered receiver in its (parent) function.
func (x *extractor) nestedCovered(lit *ssa.Function, st *state) bool {
	parent := lit.Parent()
	if parent == nil {
		return false
	}
	pc := x.track(parent, st)
	found := false
	for _, b := range parent.Blocks {
		for _, ins := range b.Instrs {
			ci, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			o := classify(ci.Common())
			if o == nil || o.isFunc || (o.name != "Group" && o.name != "Route") || len(o.args) == 0 {
				continue
			}
			if literal(parent, o.args[len(o.args)-1]) != lit {
				continue
			}
			found = true
			if !pc.tracked[o.recv] || pc.infoOf(o.recv).k != kCovered {
				return false
			}
		}
	}
	return found
}

// formFactory reports whether every return of module function f at result
// index i is a router f created in the supported form (proven, through
// MakeInterface/ChangeInterface only), and f is not linkname-exposed.
func (x *extractor) formFactory(f *ssa.Function, i int, st *state) bool {
	if !st.audited[f] || st.linkname[f] || !st.mayRet[f][i] {
		return false
	}
	c := x.track(f, st)
	rets := 0
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			r, ok := ins.(*ssa.Return)
			if !ok || i >= len(r.Results) {
				continue
			}
			rets++
			v := r.Results[i]
			for {
				if mi, ok := v.(*ssa.MakeInterface); ok {
					v = mi.X
					continue
				}
				if ci, ok := v.(*ssa.ChangeInterface); ok {
					v = ci.X
					continue
				}
				break
			}
			call, ok := v.(*ssa.Call)
			if !ok || !isConstructor(&call.Call) || !c.tracked[call] {
				return false
			}
		}
	}
	return rets > 0
}

// linknameExposed finds module functions named by a //go:linkname directive.
func linknameExposed(all []*packages.Package, module map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, p := range all {
		if !module[p.PkgPath] {
			continue
		}
		for _, f := range p.Syntax {
			for _, cg := range f.Comments {
				for _, cm := range cg.List {
					rest, ok := strings.CutPrefix(cm.Text, "//go:linkname ")
					if !ok {
						continue
					}
					if fields := strings.Fields(rest); len(fields) > 0 {
						out[p.PkgPath+"."+fields[0]] = true
					}
				}
			}
		}
	}
	return out
}

// audit checks every module function against the supported form.
func (x *extractor) audit(fns []*ssa.Function, linkname map[string]bool) {
	st := &state{audited: map[*ssa.Function]bool{}, mayRet: map[*ssa.Function]map[int]bool{}, linkname: map[*ssa.Function]bool{}}
	for _, fn := range fns {
		st.audited[fn] = true
		if fn.Parent() == nil && linkname[fn.String()] {
			st.linkname[fn] = true
		}
	}
	// May-return-a-router: a monotone boolean, so that a router hidden behind
	// an interface result type is still recognized at the call site.
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			c := x.track(fn, st)
			for v := range c.tracked {
				if v.Referrers() == nil {
					continue
				}
				for _, ref := range *v.Referrers() {
					r, ok := ref.(*ssa.Return)
					if !ok {
						continue
					}
					for i, res := range r.Results {
						if res == v && !st.mayRet[fn][i] {
							if st.mayRet[fn] == nil {
								st.mayRet[fn] = map[int]bool{}
							}
							st.mayRet[fn][i] = true
							changed = true
						}
					}
				}
			}
		}
	}
	for _, fn := range fns {
		c := x.track(fn, st)
		unit := fn.Pkg.Pkg.Path()
		report := func(pos token.Pos, why string) {
			if !pos.IsValid() {
				pos = fn.Pos()
			}
			x.diag(CatIdentity, unit, fn, "unsupported_router_form", why, x.pos(fn, pos))
		}
		if st.linkname[fn] && (len(c.tracked) > 0 || st.mayRet[fn] != nil) {
			report(fn.Pos(), "a function handling routers is exposed through //go:linkname; callers outside the analyzed module cannot be seen")
		}
		for v := range c.tracked {
			if !isOrigin(v) || c.infoOf(v).k != kUncovered {
				continue
			}
			if g, ok := v.(*ssa.Global); ok {
				for _, b := range fn.Blocks {
					for _, ins := range b.Instrs {
						for _, op := range ins.Operands(nil) {
							if op != nil && *op == ssa.Value(g) {
								report(ins.Pos(), "router outside the supported form ("+sourceKind(v)+" "+g.String()+"); routes registered on it cannot be identified")
							}
						}
					}
				}
				continue
			}
			report(v.Pos(), "router outside the supported form ("+sourceKind(v)+"); routes registered on it cannot be identified")
		}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				for _, op := range ins.Operands(nil) {
					if op == nil || *op == nil || !c.tracked[*op] {
						continue
					}
					if why := c.allowed(*op, ins); why != "" {
						report(ins.Pos(), "a router value is used outside the supported form ("+why+"); routes registered or served through it cannot be identified")
					}
				}
			}
		}
	}
}

// servingArg is the handler argument position of each serving API.
var servingArg = map[string]map[string]int{
	httpPath:            {"ListenAndServe": 1, "ListenAndServeTLS": 3, "Serve": 1, "ServeTLS": 1},
	"net/http/httptest": {"NewServer": 0, "NewUnstartedServer": 0, "NewTLSServer": 0},
}

// serverMethods are the only uses of a local http.Server holding a router.
var serverMethods = map[string]bool{"ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true, "Shutdown": true, "Close": true}

// allowed returns "" if the use of tracked value v by ins is in the form.
func (c *fnCheck) allowed(v ssa.Value, ins ssa.Instruction) string {
	info := c.infoOf(v)
	served := func() string { // a serving use: needs a proven, covered or served-only value
		if info.k == kUncovered {
			return "the router is outside the supported form"
		}
		if !info.proven {
			return "served without a proven concrete *http.ServeMux or *chi.Mux"
		}
		return ""
	}
	switch r := ins.(type) {
	case *ssa.DebugRef:
		return ""
	case *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.Phi:
		return "" // the result is tracked and checked itself
	case *ssa.Return:
		if info.k == kServedOnly {
			return "a router returned by another function may only be served directly, not returned on"
		}
		return ""
	case *ssa.ChangeType:
		if isRouterValueType(r.Type()) && info.k == kCovered {
			return ""
		}
		return "converted to " + r.Type().String()
	case *ssa.BinOp:
		if (r.Op == token.EQL || r.Op == token.NEQ) && info.k != kServedOnly {
			return ""
		}
		return "used in an expression"
	case *ssa.Store:
		if fa, ok := r.Addr.(*ssa.FieldAddr); ok && r.Val == v && isHTTPServerHandlerField(fa) {
			if why := served(); why != "" {
				return why
			}
			if why := localServer(fa.X); why != "" {
				return "stored as the Handler of an http.Server that " + why
			}
			return ""
		}
		return "stored into a variable, field, slice, map or global"
	case ssa.CallInstruction:
		cc := r.Common()
		// Serving APIs: exact handler position, proven value.
		if f := cc.StaticCallee(); f != nil && !cc.IsInvoke() {
			if obj, _ := f.Object().(*types.Func); obj != nil && obj.Pkg() != nil {
				if pos, ok := servingArg[obj.Pkg().Path()][obj.Name()]; ok && obj.Type().(*types.Signature).Recv() == nil {
					if pos >= len(cc.Args) || cc.Args[pos] != v {
						return "passed to " + obj.Name() + " in a position other than its handler"
					}
					return served()
				}
			}
		}
		if info.k == kServedOnly {
			return "a router returned by another function may only be served directly"
		}
		// Dispatch: ServeHTTP and read-only methods need the proof.
		name := ""
		if cc.IsInvoke() {
			name = cc.Method.Name()
		} else if f := cc.StaticCallee(); f != nil {
			if obj, _ := f.Object().(*types.Func); obj != nil {
				name = obj.Name()
			}
		}
		recv := (cc.IsInvoke() && cc.Value == v) || (!cc.IsInvoke() && len(cc.Args) > 0 && cc.Args[0] == v && cc.Signature().Recv() != nil)
		if recv && (name == "ServeHTTP" || readOnlyChi[name] || name == "Handler") {
			if o := classify(cc); o != nil || name == "ServeHTTP" || isServeMuxMethod(cc) {
				return served()
			}
		}
		// Recognized registration ops: the receiver only.
		if o := classify(cc); o != nil && !o.isFunc && o.recv == v && !readOnlyChi[o.name] {
			for _, arg := range o.args {
				if arg == v {
					return "passed as an argument of a router operation"
				}
			}
			if info.k == kUncovered {
				return "registration on a router outside the supported form"
			}
			return ""
		}
		if cc.IsInvoke() && cc.Value == v {
			return "method " + cc.Method.Name() + " called through an interface"
		}
		return "passed to a call (helpers, factories and any other callee are outside the supported form)"
	}
	return "used by " + instrName(ins)
}

func isServeMuxMethod(c *ssa.CallCommon) bool {
	f := c.StaticCallee()
	return f != nil && f.Signature.Recv() != nil && isServeMux(f.Signature.Recv().Type())
}

// localServer reports why an http.Server holding a router is not a local
// value that is only written and served. "" means allowed.
func localServer(v ssa.Value) string {
	alloc, ok := v.(*ssa.Alloc)
	if !ok {
		return "is not a local value"
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
