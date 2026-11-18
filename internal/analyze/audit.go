package analyze

// The router audit (router matrix, section 2, "Router value audit"). Every
// SSA value of every module function that may carry a router is tracked, and
// each of its uses must be on a short allowlist; any other use means routes
// could be registered or served in a way the analyzer cannot see, which is
// module-wide unknown registration identity (unsupported_router_form).
//
// Tracked values ("router-bearing"):
//   - seeds: any value whose type is a router (*http.ServeMux, http.ServeMux,
//     *chi.Mux, chi.Router, aliases of these) or a defined type convertible to
//     one (same underlying type as http.ServeMux or chi.Mux): parameters,
//     free variables, loads (http.DefaultServeMux), allocations (new, zero
//     values), field loads, call results, type assertions, extracts, phis;
//   - results of calls to module functions that return a tracked value
//     (factories), and parameters of module functions that receive one
//     (computed to a fixed point);
//   - MakeInterface, ChangeInterface, ChangeType, Convert, TypeAssert and Phi
//     results of tracked values.
//
// Allowed uses of a tracked value (everything else is a violation):
//  1. the receiver of a recognized router operation (registration, Use, With,
//     Group, Route, Mount, read-only chi methods; matrix section 1);
//  2. returned to the caller;
//  3. the handler argument of an enumerated serving API (http.ListenAndServe,
//     ListenAndServeTLS, Serve, ServeTLS; httptest.NewServer,
//     NewUnstartedServer, NewTLSServer) or the Handler field of an
//     http.Server;
//  4. ServeHTTP (or (*http.ServeMux).Handler) when the concrete type is proven
//     to be the recognized router: proof starts only from a value whose static
//     type is *http.ServeMux or *chi.Mux and survives only MakeInterface and
//     ChangeInterface, never a parameter or value of interface type or a phi;
//  5. read-only: debug references, ==/!= comparisons;
//  6. an argument to an analyzed module function whose corresponding
//     parameter is itself tracked (so the callee is audited for it), or an
//     interface-preserving conversion or phi (the result is tracked).

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

// concreteRouter reports a static type that is a concrete recognized router
// (*http.ServeMux, http.ServeMux, *chi.Mux): its dynamic type is proven.
func concreteRouter(t types.Type) bool {
	n, ok := named(t)
	if !ok {
		return false
	}
	if _, isIface := n.Underlying().(*types.Interface); isIface {
		return false
	}
	return isServeMux(t) || (isChiRouterType(t) && n.Obj().Name() == "Mux")
}

type taint struct {
	tracked map[ssa.Value]bool
	proven  map[ssa.Value]bool
}

// auditState carries the interprocedural facts of the fixed point.
type auditState struct {
	params map[*ssa.Parameter]bool        // module params that receive a tracked value
	rets   map[*ssa.Function]map[int]bool // module functions returning a tracked value at result index
}

func (x *extractor) taint(fn *ssa.Function, st *auditState) taint {
	t := taint{tracked: map[ssa.Value]bool{}, proven: map[ssa.Value]bool{}}
	var work []ssa.Value
	add := func(v ssa.Value, proven bool) {
		if v == nil || t.tracked[v] {
			return
		}
		t.tracked[v], t.proven[v] = true, proven
		work = append(work, v)
	}
	for _, p := range fn.Params {
		if x.routerish(p.Type()) || st.params[p] {
			add(p, concreteRouter(p.Type()) && x.routerish(p.Type()) && !st.params[p])
		}
	}
	for _, fv := range fn.FreeVars {
		if x.routerish(fv.Type()) {
			add(fv, false)
		}
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			v, ok := ins.(ssa.Value)
			if !ok {
				continue
			}
			if x.routerish(v.Type()) {
				add(v, concreteRouter(v.Type()))
				continue
			}
			call, ok := ins.(*ssa.Call)
			if !ok {
				continue
			}
			f := call.Call.StaticCallee()
			if f == nil || st.rets[f] == nil {
				continue
			}
			if call.Call.Signature().Results().Len() == 1 {
				if st.rets[f][0] {
					add(call, false)
				}
				continue
			}
			for _, ref := range *call.Referrers() {
				if e, ok := ref.(*ssa.Extract); ok && st.rets[f][e.Index] {
					add(e, false)
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
			case *ssa.MakeInterface:
				add(r, t.proven[v])
			case *ssa.ChangeInterface:
				add(r, t.proven[v])
			case *ssa.ChangeType:
				add(r, false)
			case *ssa.Convert:
				add(r, false)
			case *ssa.TypeAssert:
				add(r, false)
			case *ssa.Phi:
				add(r, false)
			}
		}
	}
	return t
}

// audit runs the fixed point over parameters and returns, then checks every
// use of every tracked value.
func (x *extractor) audit(fns []*ssa.Function) {
	st := &auditState{params: map[*ssa.Parameter]bool{}, rets: map[*ssa.Function]map[int]bool{}}
	audited := map[*ssa.Function]bool{}
	for _, fn := range fns {
		audited[fn] = true
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			t := x.taint(fn, st)
			for v := range t.tracked {
				if v.Referrers() == nil {
					continue
				}
				for _, ref := range *v.Referrers() {
					switch r := ref.(type) {
					case *ssa.Return:
						for i, res := range r.Results {
							if res == v {
								if st.rets[fn] == nil {
									st.rets[fn] = map[int]bool{}
								}
								if !st.rets[fn][i] {
									st.rets[fn][i] = true
									changed = true
								}
							}
						}
					case ssa.CallInstruction:
						c := r.Common()
						callee := c.StaticCallee()
						if c.IsInvoke() || callee == nil || !audited[callee] {
							continue
						}
						for i, a := range c.Args {
							if a == v && i < len(callee.Params) && !st.params[callee.Params[i]] && !x.routerish(callee.Params[i].Type()) {
								st.params[callee.Params[i]] = true
								changed = true
							}
						}
					}
				}
			}
		}
	}
	for _, fn := range fns {
		t := x.taint(fn, st)
		unit := fn.Pkg.Pkg.Path()
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				for _, op := range ins.Operands(nil) {
					if op == nil || *op == nil || !t.tracked[*op] {
						continue
					}
					if why := x.allowed(*op, ins, t, st, audited); why != "" {
						pos := ins.Pos()
						if !pos.IsValid() {
							pos = fn.Pos()
						}
						x.diag(CatIdentity, unit, fn, "unsupported_router_form",
							"a router value is used outside the router audit allowlist ("+why+"); routes registered or served through it cannot be identified", x.pos(fn, pos))
					}
				}
			}
		}
	}
}

// servingAPIs are the enumerated trusted serving sinks (router matrix 2).
var servingAPIs = map[string]map[string]bool{
	httpPath:            {"ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true},
	"net/http/httptest": {"NewServer": true, "NewUnstartedServer": true, "NewTLSServer": true},
}

// allowed returns "" if the use of tracked value v by ins is on the
// allowlist, or why it is not.
func (x *extractor) allowed(v ssa.Value, ins ssa.Instruction, t taint, st *auditState, audited map[*ssa.Function]bool) string {
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
			return ""
		}
		return "stored into a variable, field, slice, map or global"
	case ssa.CallInstruction:
		c := r.Common()
		if o := classify(c); o != nil && !o.isFunc && o.recv == v {
			// A recognized router operation on it (the receiver only).
			for _, a := range o.args {
				if a == v {
					return "passed as an argument of a router operation"
				}
			}
			return ""
		}
		if c.IsInvoke() {
			if c.Value == v && c.Method.Name() == "ServeHTTP" && t.proven[v] {
				return ""
			}
			if c.Value == v {
				return "method " + c.Method.Name() + " called through an interface whose concrete type is not proven to be the router"
			}
			return "passed to an interface method call"
		}
		callee := c.StaticCallee()
		if callee == nil {
			return "passed to a call through a function value"
		}
		if obj, _ := callee.Object().(*types.Func); obj != nil && obj.Pkg() != nil {
			sig := obj.Type().(*types.Signature)
			if sig.Recv() == nil && servingAPIs[obj.Pkg().Path()][obj.Name()] {
				return ""
			}
			if sig.Recv() != nil && isServeMux(sig.Recv().Type()) && (obj.Name() == "ServeHTTP" || obj.Name() == "Handler") &&
				len(c.Args) > 0 && c.Args[0] == v && t.proven[v] {
				return ""
			}
		}
		if audited[callee] {
			for i, a := range c.Args {
				if a == v && i < len(callee.Params) && (st.params[callee.Params[i]] || x.routerish(callee.Params[i].Type())) {
					return ""
				}
			}
		}
		return "passed to " + callee.String() + ", which is not an audited module function receiving it"
	}
	return "used by " + instrName(ins)
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
