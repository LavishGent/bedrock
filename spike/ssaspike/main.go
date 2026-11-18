// Command ssaspike is the Bedrock POC step 0 call-graph spike.
//
// It loads one target module under the pinned build profile, builds SSA for
// the whole analysis universe (module packages plus every dependency and
// standard-library body), runs CHA, seeds VTA with it, and classifies every
// call site that a sound analysis cannot resolve. It runs under a wall-clock
// timeout and a memory limit; hitting either aborts with exit code 3 and a
// partial result, which Bedrock would treat as INDETERMINATE.
//
// It is throwaway measurement code, not the analyzer.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Pinned build profile (plan section 7). Changing any of these is a plan change.
const (
	pinnedGoVersion     = "go1.26.11"
	pinnedXToolsVersion = "v0.52.0"
	profileGOOS         = "linux"
	profileGOARCH       = "amd64"
	profileCGO          = "0"
	profileTags         = "" // no build tags
)

type phase struct {
	Name        string  `json:"name"`
	WallSeconds float64 `json:"wall_seconds"`
	HeapMiB     float64 `json:"heap_live_mib_after"`
	TotalMiB    float64 `json:"go_runtime_total_mib_after"`
}

type diag struct {
	Kind     string `json:"kind"`
	Origin   string `json:"origin"` // module | dependency | stdlib | synthetic
	Function string `json:"function"`
	Unit     string `json:"unit"`
	Detail   string `json:"detail"`
	Pos      string `json:"pos,omitempty"`
}

type result struct {
	Status         string            `json:"status"` // ok | timeout | memory_limit | load_error
	ExitCode       int               `json:"exit_code"`
	Toolchain      map[string]string `json:"toolchain"`
	BuildProfile   map[string]string `json:"build_profile"`
	Limits         map[string]string `json:"limits"`
	Target         string            `json:"target"`
	Patterns       []string          `json:"patterns"`
	Phases         []phase           `json:"phases"`
	TotalWallSecs  float64           `json:"total_wall_seconds"`
	PeakTotalMiB   float64           `json:"peak_go_runtime_total_mib"`
	MaxRSSMiB      float64           `json:"max_rss_mib"`
	Universe       map[string]any    `json:"universe"`
	Graphs         map[string]any    `json:"graphs"`
	Diagnostics    map[string]any    `json:"diagnostics"`
	Entry          map[string]any    `json:"entry"`
	Reachability   map[string]any    `json:"reachability_from_module"`
	IDCollisions   []string          `json:"symbol_id_collisions"`
	LoadErrors     []string          `json:"load_errors,omitempty"`
	AbortedInPhase string            `json:"aborted_in_phase,omitempty"`
	AbortReason    string            `json:"abort_reason,omitempty"`
}

var (
	mu      sync.Mutex
	res     = &result{Status: "running"}
	outPath string
	cur     = "init"
	peakTot float64
)

func main() {
	dir := flag.String("dir", "spike/target", "target module directory")
	timeout := flag.Duration("timeout", 120*time.Second, "wall-clock limit for the whole run")
	memMiB := flag.Int64("mem-limit-mib", 4096, "hard limit on Go runtime total memory (MiB)")
	flag.StringVar(&outPath, "out", "", "write the JSON summary here (default stdout)")
	diagOut := flag.String("diag-out", "", "write every diagnostic as JSON lines here")
	flag.StringVar(&sitesOut, "dump-module-sites", "", "write module call sites (ordinal, mode, VTA targets) as JSON lines here")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	limit := *memMiB << 20
	debug.SetMemoryLimit(limit) // soft: GC works harder before the hard limit trips
	res.Toolchain = map[string]string{
		"go_runtime":     runtime.Version(),
		"go_pinned":      pinnedGoVersion,
		"x_tools_pinned": pinnedXToolsVersion,
		"x_tools_linked": xToolsVersion(),
	}
	res.BuildProfile = map[string]string{
		"GOOS": profileGOOS, "GOARCH": profileGOARCH, "CGO_ENABLED": profileCGO,
		"tags": profileTags, "GOFLAGS": "-mod=readonly", "GOWORK": "off", "GOTOOLCHAIN": "local",
	}
	res.Limits = map[string]string{"timeout": timeout.String(), "mem_limit_mib": fmt.Sprint(*memMiB)}
	res.Target = filepath.ToSlash(*dir)
	targetAbs, _ = filepath.Abs(*dir)
	goEnv()
	res.Patterns = patterns

	start := time.Now()
	go watchdog(start, *timeout, float64(limit)/(1<<20))

	env := append(os.Environ(),
		"GOOS="+profileGOOS, "GOARCH="+profileGOARCH, "CGO_ENABLED="+profileCGO,
		"GOFLAGS=-mod=readonly", "GOWORK=off", "GOTOOLCHAIN=local")
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Phase 1: load every package in the universe with full syntax and types.
	var pkgs []*packages.Package
	timed("load", func() {
		cfg := &packages.Config{
			Context:    ctx,
			Mode:       packages.LoadAllSyntax | packages.NeedModule,
			Dir:        *dir,
			Env:        env,
			BuildFlags: []string{"-tags=" + profileTags},
			Tests:      false,
		}
		var err error
		pkgs, err = packages.Load(cfg, patterns...)
		if err != nil {
			finish("load_error", 3, err.Error())
		}
	})
	origin := map[string]string{} // import path -> module | dependency | stdlib
	files := map[string]int{}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		switch {
		case p.Module != nil && p.Module.Main:
			origin[p.PkgPath] = "module"
		case p.Module != nil:
			origin[p.PkgPath] = "dependency"
		default:
			origin[p.PkgPath] = "stdlib"
		}
		files[origin[p.PkgPath]] += len(p.CompiledGoFiles)
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, p.PkgPath+": "+e.Error())
		}
	})
	sort.Strings(loadErrs)
	if len(loadErrs) > 0 {
		mu.Lock()
		res.LoadErrors = loadErrs
		mu.Unlock()
		finish("load_error", 3, "")
	}

	// Phase 2: SSA for every loaded package, generics instantiated.
	var prog *ssa.Program
	timed("ssa", func() {
		prog, _ = ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
		prog.Build()
	})

	var universe map[*ssa.Function]bool
	timed("universe", func() { universe = ssautil.AllFunctions(prog) })

	var chaCG, vtaCG *callgraph.Graph
	timed("cha", func() { chaCG = cha.CallGraph(prog) })
	timed("vta", func() { vtaCG = vta.CallGraph(universe, chaCG) })

	var diags []diag
	timed("classify", func() {
		diags = classify(prog, universe, vtaCG, origin, chaCG, files)
	})

	if *diagOut != "" {
		f, err := os.Create(*diagOut)
		if err == nil {
			enc := json.NewEncoder(f)
			for _, d := range diags {
				_ = enc.Encode(d)
			}
			_ = f.Close()
		}
	}
	mu.Lock()
	res.TotalWallSecs = round(time.Since(start).Seconds())
	mu.Unlock()
	finish("ok", 0, "")
}

func fnUnit(fn *ssa.Function) string {
	if fn.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	if o := fn.Origin(); o != nil && o.Pkg != nil {
		return o.Pkg.Pkg.Path()
	}
	if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		return obj.Pkg().Path()
	}
	return ""
}

func fnKind(fn *ssa.Function) string {
	switch {
	case fn.Parent() != nil:
		return "closure"
	case fn.Origin() != nil:
		return "instance"
	case fn.Synthetic != "":
		return "wrapper"
	case fn.Signature.Recv() != nil:
		return "method"
	default:
		return "func"
	}
}

var reflectCalls = map[string]bool{
	"(reflect.Value).Call": true, "(reflect.Value).CallSlice": true,
	"(reflect.Value).Method": true, "(reflect.Value).MethodByName": true,
	"reflect.MakeFunc": true,
}

func classify(prog *ssa.Program, universe map[*ssa.Function]bool, cg *callgraph.Graph,
	origin map[string]string, chaCG *callgraph.Graph, files map[string]int) []diag {

	orig := func(fn *ssa.Function) string {
		if u := fnUnit(fn); u != "" {
			return origin[u]
		}
		return "synthetic"
	}
	pos := func(p token.Pos) string {
		if !p.IsValid() {
			return ""
		}
		ps := prog.Fset.Position(p)
		ps.Filename = relPath(ps.Filename)
		return ps.String()
	}

	// Sorted universe for determinism; detect ID collisions on the way.
	fns := make([]*ssa.Function, 0, len(universe))
	byID := map[string][]*ssa.Function{}
	for fn := range universe {
		fns = append(fns, fn)
		byID[fn.String()] = append(byID[fn.String()], fn)
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].String() < fns[j].String() })
	outSet := func(fn *ssa.Function) string {
		var ts []string
		if n := cg.Nodes[fn]; n != nil {
			for _, e := range n.Out {
				ts = append(ts, e.Callee.Func.String())
			}
		}
		sort.Strings(ts)
		return strings.Join(dedup(ts), "|")
	}
	var collisions []string
	equivGroups, diffGroups := 0, 0
	collisionKinds := map[string]int{}
	for id, l := range byID {
		if len(l) > 1 {
			same := true
			for _, f := range l[1:] {
				if outSet(f) != outSet(l[0]) || f.Signature.String() != l[0].Signature.String() {
					same = false
				}
			}
			tag := "differing"
			if same {
				equivGroups++
				tag = "equivalent"
			} else {
				diffGroups++
			}
			var ks []string
			for _, f := range l {
				ks = append(ks, fnKind(f))
			}
			sort.Strings(ks)
			kind := strings.Join(dedup(ks), "+")
			collisionKinds[kind+"/"+tag]++
			collisions = append(collisions, fmt.Sprintf("%s (x%d, %s %s)", id, len(l), kind, tag))
		}
	}
	sort.Strings(collisions)
	var syntheticNames []string
	for _, fn := range fns {
		if fnUnit(fn) == "" {
			syntheticNames = append(syntheticNames, fn.String()+" ("+fn.Synthetic+")")
		}
	}

	genericOrigins, typeParamInstances := 0, 0
	for _, fn := range fns {
		if fn.TypeParams().Len() > 0 && fn.Origin() == nil {
			genericOrigins++
		}
		if fn.Origin() != nil && containsTypeParam(fn.TypeArgs()) {
			typeParamInstances++
		}
	}
	uniCount := map[string]map[string]int{} // origin -> kind -> n
	bodyless := map[string]int{}
	units := map[string]map[string]bool{}
	for _, fn := range fns {
		o := orig(fn)
		if uniCount[o] == nil {
			uniCount[o] = map[string]int{}
			units[o] = map[string]bool{}
		}
		uniCount[o][fnKind(fn)]++
		units[o][fnUnit(fn)] = true
		if fn.Blocks == nil {
			bodyless[o]++
		}
	}

	// Per-site VTA target counts.
	siteTargets := map[ssa.CallInstruction]int{}
	edges := 0
	for _, n := range cg.Nodes {
		for _, e := range n.Out {
			siteTargets[e.Site]++
			edges++
		}
	}
	chaEdges := 0
	for _, n := range chaCG.Nodes {
		chaEdges += len(n.Out)
	}

	var diags []diag
	diagByFn := map[*ssa.Function][]int{}
	sites, multi := 0, 0
	add := func(fn *ssa.Function, kind, detail string, p token.Pos) {
		diagByFn[fn] = append(diagByFn[fn], len(diags))
		diags = append(diags, diag{Kind: kind, Origin: orig(fn), Function: fn.String(),
			Unit: fnUnit(fn), Detail: detail, Pos: pos(p)})
	}
	for _, fn := range fns {
		unsafeConv := 0
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if cv, ok := ins.(*ssa.Convert); ok {
					if isUnsafePtr(cv.Type()) || isUnsafePtr(cv.X.Type()) {
						unsafeConv++
					}
				}
				ci, ok := ins.(ssa.CallInstruction)
				if !ok {
					continue
				}
				c := ci.Common()
				if _, ok := c.Value.(*ssa.Builtin); ok {
					continue
				}
				sites++
				if siteTargets[ci] > 1 {
					multi++
				}
				if callee := c.StaticCallee(); callee != nil {
					if reflectCalls[callee.String()] {
						add(fn, "reflection_call", callee.String(), ci.Pos())
					}
					if callee.Blocks == nil {
						add(fn, "bodyless_function", callee.String(), ci.Pos())
					}
					continue
				}
				if siteTargets[ci] > 0 {
					continue
				}
				if c.IsInvoke() {
					add(fn, "unresolved_interface_call", c.Method.FullName(), ci.Pos())
				} else {
					add(fn, "unresolved_dynamic_call", c.Value.Type().String(), ci.Pos())
				}
			}
		}
		if unsafeConv > 0 {
			add(fn, "unsafe_pointer", fmt.Sprintf("%d unsafe.Pointer conversions", unsafeConv), fn.Pos())
		}
	}

	if sitesOut != "" {
		dumpModuleSites(fns, cg, orig, pos)
	}
	byKindOrigin := map[string]map[string]int{}
	byUnit := map[string]int{}
	for _, d := range diags {
		if byKindOrigin[d.Kind] == nil {
			byKindOrigin[d.Kind] = map[string]int{}
		}
		byKindOrigin[d.Kind][d.Origin]++
		if d.Origin != "stdlib" {
			byUnit[d.Unit]++
		}
	}
	samples := map[string][]string{}
	for _, d := range diags {
		if d.Origin == "stdlib" {
			continue
		}
		key := d.Origin + "/" + d.Kind
		if len(samples[key]) < 8 {
			samples[key] = append(samples[key], d.Function+" -> "+d.Detail+" @ "+d.Pos)
		}
	}

	// Entry: functions in the universe with no incoming VTA edge, plus route handlers.
	in := map[*ssa.Function]int{}
	for _, n := range cg.Nodes {
		for _, e := range n.Out {
			in[e.Callee.Func]++ // self-edges count, as the spec defines indegree
		}
	}
	entryByOrigin := map[string]int{}
	var entries []*ssa.Function
	var moduleEntries []string
	for _, fn := range fns {
		if in[fn] == 0 {
			entries = append(entries, fn)
			entryByOrigin[orig(fn)]++
			if orig(fn) == "module" {
				moduleEntries = append(moduleEntries, fn.String())
			}
		}
	}
	handlers := chiHandlers(fns, orig)

	// Forward reachability from the module's own functions.
	reach := func(roots []*ssa.Function) map[*ssa.Function]bool {
		seen := map[*ssa.Function]bool{}
		stack := append([]*ssa.Function(nil), roots...)
		for len(stack) > 0 {
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[f] {
				continue
			}
			seen[f] = true
			if n := cg.Nodes[f]; n != nil {
				for _, e := range n.Out {
					stack = append(stack, e.Callee.Func)
				}
			}
		}
		return seen
	}
	summarize := func(roots []*ssa.Function) map[string]any {
		r := reach(roots)
		kinds := map[string]map[string]int{}
		total := 0
		for f := range r {
			for _, i := range diagByFn[f] {
				d := diags[i]
				if kinds[d.Kind] == nil {
					kinds[d.Kind] = map[string]int{}
				}
				kinds[d.Kind][d.Origin]++
				total++
			}
		}
		byO := map[string]int{}
		for f := range r {
			byO[orig(f)]++
		}
		return map[string]any{"reachable_functions": len(r), "reachable_by_origin": byO,
			"unknown_call_diagnostics_reachable": total, "by_kind_and_origin": kinds}
	}
	var moduleFns []*ssa.Function
	perFn := map[string]int{}
	for _, fn := range fns {
		if orig(fn) == "module" {
			moduleFns = append(moduleFns, fn)
		}
	}
	for _, fn := range moduleFns {
		n := 0
		for f := range reach([]*ssa.Function{fn}) {
			n += len(diagByFn[f])
		}
		perFn[fn.String()] = n
	}
	handlerReach := map[string]any{}
	for _, h := range handlers {
		handlerReach[h.String()] = summarize([]*ssa.Function{h})
	}

	// Module functions reachable from Entry symbols outside the module: paths
	// from library entries into user code that would bypass any boundary.
	var libEntries []*ssa.Function
	for _, e := range entries {
		if orig(e) != "module" {
			libEntries = append(libEntries, e)
		}
	}
	// BFS from all library entries, keeping parents, to show one witness path.
	parent := map[*ssa.Function]*ssa.Function{}
	seenL := map[*ssa.Function]bool{}
	queue := append([]*ssa.Function(nil), libEntries...)
	for _, e := range libEntries {
		seenL[e] = true
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		n := cg.Nodes[f]
		if n == nil {
			continue
		}
		out := append([]*callgraph.Edge(nil), n.Out...)
		sort.Slice(out, func(i, j int) bool { return out[i].Callee.Func.String() < out[j].Callee.Func.String() })
		for _, e := range out {
			g := e.Callee.Func
			if !seenL[g] {
				seenL[g] = true
				parent[g] = f
				queue = append(queue, g)
			}
		}
	}
	modFromLib := map[string]string{}
	for _, fn := range moduleFns {
		if seenL[fn] {
			var path []string
			for f := fn; f != nil; f = parent[f] {
				path = append([]string{f.String()}, path...)
				if len(path) > 40 {
					break
				}
			}
			modFromLib[fn.String()] = strings.Join(path, " -> ")
		}
	}

	moduleFnNames := make([]string, 0, len(moduleFns))
	for _, fn := range moduleFns {
		moduleFnNames = append(moduleFnNames, fn.String()+" ["+fnKind(fn)+"]")
	}
	handlerNames := []string{}
	for _, h := range handlers {
		handlerNames = append(handlerNames, h.String())
	}

	unitCounts := map[string]int{}
	for o, m := range units {
		unitCounts[o] = len(m)
	}
	mu.Lock()
	res.Universe = map[string]any{
		"definition":             "ssautil.AllFunctions over SSA built (InstantiateGenerics) from every package in the transitive import closure of the target patterns, loaded with full syntax; tests excluded",
		"functions_total":        len(fns),
		"functions_by_origin":    uniCount,
		"bodyless_by_origin":     bodyless,
		"units_by_origin":        unitCounts,
		"go_files_by_origin":     files,
		"module_functions":       moduleFnNames,
		"call_sites_non_builtin": sites,
	}
	res.Graphs = map[string]any{
		"cha_nodes": len(chaCG.Nodes), "cha_edges": chaEdges,
		"vta_nodes": len(cg.Nodes), "vta_edges": edges,
		"vta_sites_with_multiple_targets": multi,
	}
	res.Diagnostics = map[string]any{
		"total":              len(diags),
		"by_kind_and_origin": byKindOrigin,
		"non_stdlib_by_unit": byUnit,
		"non_stdlib_samples": samples,
	}
	res.Entry = map[string]any{
		"definition":            "universe functions with zero incoming VTA edges, plus chi route handlers",
		"no_incoming_total":     len(entries),
		"no_incoming_by_origin": entryByOrigin,
		"module_no_incoming":    moduleEntries,
		"route_handlers":        handlerNames,
	}
	res.Reachability = map[string]any{
		"from_all_module_functions":                          summarize(moduleFns),
		"from_each_route_handler":                            handlerReach,
		"diagnostics_reachable_per_module_fn":                perFn,
		"module_functions_reachable_from_non_module_entries": modFromLib,
	}
	res.IDCollisions = collisions
	res.Universe["synthetic_without_package"] = syntheticNames
	res.Universe["generic_origins_in_universe"] = genericOrigins
	res.Universe["instances_with_type_param_args"] = typeParamInstances
	res.Universe["id_collision_groups"] = map[string]any{
		"total": len(collisions), "equivalent": equivGroups, "differing": diffGroups, "by_kind": collisionKinds,
	}
	unitTop := map[string]int{}
	for _, d := range diags {
		unitTop[d.Unit]++
	}
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range unitTop {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	var top []string
	for i := 0; i < len(kvs) && i < 15; i++ {
		top = append(top, fmt.Sprintf("%s: %d", kvs[i].k, kvs[i].v))
	}
	res.Diagnostics["top_units"] = top
	mu.Unlock()
	return diags
}

// chiHandlers finds handler arguments of chi Mux route registrations made in
// module code. Method values appear as bound-method closures.
func chiHandlers(fns []*ssa.Function, orig func(*ssa.Function) string) []*ssa.Function {
	var out []*ssa.Function
	seen := map[*ssa.Function]bool{}
	for _, fn := range fns {
		if orig(fn) != "module" {
			continue
		}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				ci, ok := ins.(ssa.CallInstruction)
				if !ok {
					continue
				}
				c := ci.Common()
				var name string
				var args []ssa.Value
				if c.IsInvoke() {
					name, args = c.Method.FullName(), c.Args
				} else if callee := c.StaticCallee(); callee != nil {
					name, args = callee.String(), c.Args
					if len(args) > 0 {
						args = args[1:] // drop receiver
					}
				}
				if !strings.HasPrefix(name, "(*github.com/go-chi/chi/v5.Mux).") &&
					!strings.HasPrefix(name, "(github.com/go-chi/chi/v5.Router).") {
					continue
				}
				if len(args) != 2 {
					continue
				}
				var h *ssa.Function
				v := args[1]
				for {
					// chi takes http.HandlerFunc / http.Handler: peel conversions.
					if ct, ok := v.(*ssa.ChangeType); ok {
						v = ct.X
						continue
					}
					if mi, ok := v.(*ssa.MakeInterface); ok {
						v = mi.X
						continue
					}
					break
				}
				switch v := v.(type) {
				case *ssa.Function:
					h = v
				case *ssa.MakeClosure:
					h, _ = v.Fn.(*ssa.Function)
				}
				if h != nil && !seen[h] {
					seen[h] = true
					out = append(out, h)
				}
			}
		}
	}
	return out
}

func containsTypeParam(ts []types.Type) bool {
	for _, t := range ts {
		found := false
		var walk func(types.Type)
		walk = func(t types.Type) {
			if found {
				return
			}
			switch t := t.(type) {
			case *types.TypeParam:
				found = true
			case *types.Pointer:
				walk(t.Elem())
			case *types.Slice:
				walk(t.Elem())
			case *types.Array:
				walk(t.Elem())
			case *types.Map:
				walk(t.Key())
				walk(t.Elem())
			case *types.Chan:
				walk(t.Elem())
			case *types.Named:
				for i := 0; i < t.TypeArgs().Len(); i++ {
					walk(t.TypeArgs().At(i))
				}
			}
		}
		walk(t)
		if found {
			return true
		}
	}
	return false
}

func dedup(s []string) []string {
	out := s[:0]
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func isUnsafePtr(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.UnsafePointer
}

func timed(name string, f func()) {
	mu.Lock()
	cur = name
	mu.Unlock()
	t := time.Now()
	f()
	heap, total := memSample()
	mu.Lock()
	res.Phases = append(res.Phases, phase{Name: name, WallSeconds: round(time.Since(t).Seconds()),
		HeapMiB: round(heap), TotalMiB: round(total)})
	mu.Unlock()
}

var memSamples = []metrics.Sample{
	{Name: "/memory/classes/heap/objects:bytes"},
	{Name: "/memory/classes/total:bytes"},
}

func memSample() (heapMiB, totalMiB float64) {
	s := append([]metrics.Sample(nil), memSamples...)
	metrics.Read(s)
	heapMiB = float64(s[0].Value.Uint64()) / (1 << 20)
	totalMiB = float64(s[1].Value.Uint64()) / (1 << 20)
	return
}

func watchdog(start time.Time, timeout time.Duration, limitMiB float64) {
	tick := time.NewTicker(25 * time.Millisecond)
	for range tick.C {
		_, total := memSample()
		mu.Lock()
		if total > peakTot {
			peakTot = total
		}
		mu.Unlock()
		if total > limitMiB {
			finish("memory_limit", 3, fmt.Sprintf("go runtime total %.0f MiB > limit %.0f MiB", total, limitMiB))
		}
		if time.Since(start) > timeout {
			finish("timeout", 3, "wall-clock limit "+timeout.String()+" exceeded")
		}
	}
}

var targetAbs, goroot, gomodcache, sitesOut string

type siteDump struct {
	ID      string   `json:"id"`
	Caller  string   `json:"caller"`
	Mode    string   `json:"mode"`
	Callee  string   `json:"callee_hint"`
	Targets []string `json:"targets"`
	Pos     string   `json:"pos"`
}

// dumpModuleSites writes every non-builtin call site in module functions,
// numbered in SSA block then instruction order (the schema's site ordinal).
func dumpModuleSites(fns []*ssa.Function, cg *callgraph.Graph, orig func(*ssa.Function) string, pos func(token.Pos) string) {
	f, err := os.Create(sitesOut)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, fn := range fns {
		if orig(fn) != "module" {
			continue
		}
		targets := map[ssa.CallInstruction][]string{}
		if n := cg.Nodes[fn]; n != nil {
			for _, e := range n.Out {
				targets[e.Site] = append(targets[e.Site], e.Callee.Func.String())
			}
		}
		k := 0
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				ci, ok := ins.(ssa.CallInstruction)
				if !ok {
					continue
				}
				c := ci.Common()
				if _, ok := c.Value.(*ssa.Builtin); ok {
					continue
				}
				d := siteDump{ID: fmt.Sprintf("%s#%d", fn.String(), k), Caller: fn.String(), Pos: pos(ci.Pos())}
				switch {
				case c.IsInvoke():
					d.Mode, d.Callee = "invoke", c.Method.FullName()
				case c.StaticCallee() != nil:
					d.Mode, d.Callee = "static", c.StaticCallee().String()
				default:
					d.Mode = "dynamic"
				}
				t := targets[ci]
				sort.Strings(t)
				d.Targets = dedup(t)
				_ = enc.Encode(d)
				k++
			}
		}
	}
}

func goEnv() {
	out, err := exec.Command("go", "env", "GOROOT", "GOMODCACHE").Output()
	if err != nil {
		return
	}
	l := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(l) == 2 {
		goroot, gomodcache = l[0], l[1]
	}
}

// relPath makes a file path portable: target-relative for module files,
// module@version-relative for dependencies, $GOROOT-relative for stdlib.
func relPath(f string) string {
	switch {
	case targetAbs != "" && strings.HasPrefix(f, targetAbs+string(filepath.Separator)):
		return "<target>/" + filepath.ToSlash(f[len(targetAbs)+1:])
	case gomodcache != "" && strings.HasPrefix(f, gomodcache+string(filepath.Separator)):
		return "<modcache>/" + filepath.ToSlash(f[len(gomodcache)+1:])
	case goroot != "" && strings.HasPrefix(f, goroot+string(filepath.Separator)):
		return "$GOROOT/" + filepath.ToSlash(f[len(goroot)+1:])
	}
	return f
}

var finishOnce sync.Once

func finish(status string, code int, msg string) {
	finishOnce.Do(func() {
		mu.Lock()
		res.Status, res.ExitCode = status, code
		if status != "ok" {
			res.AbortedInPhase = cur
			res.AbortReason = msg
		}
		_, total := memSample()
		if total > peakTot {
			peakTot = total
		}
		res.PeakTotalMiB = round(peakTot)
		res.MaxRSSMiB = round(maxRSSMiB())
		b, _ := json.MarshalIndent(res, "", "  ")
		mu.Unlock()
		b = append(b, '\n')
		if outPath != "" {
			_ = os.WriteFile(outPath, b, 0o644)
			fmt.Fprintf(os.Stderr, "ssaspike: status=%s exit=%d wrote %s\n", status, code, outPath)
		} else {
			os.Stdout.Write(b)
		}
		os.Exit(code)
	})
	select {} // another goroutine is finishing
}

func maxRSSMiB() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss) / (1 << 20) // bytes
	}
	return float64(ru.Maxrss) / (1 << 10) // KiB
}

func xToolsVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, d := range bi.Deps {
		if d.Path == "golang.org/x/tools" {
			return d.Version
		}
	}
	return "unknown"
}

func round(f float64) float64 { return float64(int64(f*1000+0.5)) / 1000 }
