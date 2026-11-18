// Package analyze loads one Go module under the pinned build profile and
// turns every package in its import closure into a unit shard (schema 0.4):
// unit provenance, source status, source files, content and dependency
// hashes, declared symbols, imports and load diagnostics. Call, route and
// middleware facts are later steps (plan section 8, steps 3 and 4).
//
// Every package keeps its identity and import edges whether or not it has
// compiled files: unsafe is a builtin unit, and a package that cannot be
// loaded is a "missing" unit carrying its load error.
package analyze

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"

	"github.com/LavishGent/bedrock/internal/facts"
)

// Pinned toolchain and build profile (plan section 7).
const (
	PinnedGoVersion     = "go1.26.11"
	PinnedXToolsVersion = "v0.52.0"
	ProfileGOOS         = "linux"
	ProfileGOARCH       = "amd64"
)

// stdlibInventory is `go list std` for the pinned toolchain and profile. It is
// a byte-identical copy of docs/superpowers/specs/schema/stdlib-go1.26.11.txt
// (checked by a test) and is compared with the live toolchain on every run.
//
//go:embed stdlib-go1.26.11.txt
var stdlibInventory string

// StdlibInventory returns the pinned standard-library package set.
func StdlibInventory() map[string]bool {
	set := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(stdlibInventory))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			set[l] = true
		}
	}
	return set
}

// builtinUnits are the source-less packages the compiler provides.
var builtinUnits = map[string]bool{"unsafe": true}

// Profile returns the pinned build profile as recorded in documents.
func Profile() facts.BuildProfile {
	return facts.BuildProfile{GOOS: ProfileGOOS, GOARCH: ProfileGOARCH, CgoEnabled: false,
		Tags: []string{}, GoVersion: PinnedGoVersion, XToolsVersion: PinnedXToolsVersion}
}

// ProfileDigest is the build_profile_digest every shard carries.
func ProfileDigest() string { return facts.SHA256(facts.Canon(Profile())) }

// passthrough are the only Go-related variables taken from the caller's
// environment: they locate caches and module sources but do not change what
// is built. Everything else that can affect the build is pinned or removed.
var passthrough = map[string]bool{
	"GOPATH": true, "GOMODCACHE": true, "GOCACHE": true, "GOPROXY": true, "GOSUMDB": true,
	"GONOSUMDB": true, "GONOPROXY": true, "GOPRIVATE": true, "GOINSECURE": true, "GOVCS": true,
	"GOTMPDIR": true,
}

// Env returns the controlled environment for the go command and go/packages.
// It drops GOROOT, GOFLAGS, GOEXPERIMENT, GOAMD64, CC and the like from the
// caller, disables go.env files (GOENV=off) and any go/packages driver, and
// pins the profile.
func Env() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if (strings.HasPrefix(k, "GO") || strings.HasPrefix(k, "CGO_")) && !passthrough[k] {
			continue
		}
		switch k {
		case "CC", "CXX", "AR", "PKG_CONFIG":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GOOS="+ProfileGOOS, "GOARCH="+ProfileGOARCH, "GOAMD64=v1", "CGO_ENABLED=0",
		"GOFLAGS=-mod=readonly", "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off",
		"GOEXPERIMENT=", "GOPACKAGESDRIVER=off")
}

// UnitInfo is everything later evaluation needs about one unit.
type UnitInfo struct {
	Shard   *facts.Shard
	Origin  string
	Imports []facts.Import
}

// Result is a loaded module.
type Result struct {
	ModulePath    string
	Units         map[string]*UnitInfo // by unit ID: every package in the closure
	Module        []string             // module unit IDs (any source status), sorted
	Excluded      []facts.ExcludedPackage
	ExcludedFiles []facts.ExcludedFile // files of excluded directories
	Global        []facts.Diagnostic   // scope=global diagnostics (layout, provenance)
	Fatal         bool                 // an error-severity global diagnostic stopped loading
}

// ModulePath reads the module path from dir/go.mod.
func ModulePath(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	mp := modfile.ModulePath(raw)
	if mp == "" {
		return "", errors.New("go.mod has no module directive")
	}
	return mp, nil
}

func globalDiag(kind, sev, msg string) facts.Diagnostic {
	return facts.NewDiagnostic(kind, sev, nil, nil, msg, nil)
}

func (r *Result) fatal(kind, msg string) {
	r.Global = append(r.Global, globalDiag(kind, "error", msg))
	r.Fatal = true
}

// Load analyzes the module rooted at dir.
func Load(ctx context.Context, dir string) *Result {
	res := &Result{Units: map[string]*UnitInfo{}}
	abs, err := filepath.Abs(dir)
	if err != nil {
		res.fatal("unsupported_layout", err.Error())
		return res
	}
	if res.ModulePath, err = ModulePath(abs); err != nil {
		res.fatal("unsupported_layout", "the analyzed directory must be a module root with a go.mod: "+err.Error())
		return res
	}
	if _, err := os.Stat(filepath.Join(abs, "vendor")); err == nil {
		res.fatal("unsupported_layout", "vendor/ directories are not supported (the profile pins GOFLAGS=-mod=readonly)")
		return res
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.work")); err == nil {
			res.Global = append(res.Global, globalDiag("unsupported_layout", "warning",
				"go.work found and ignored (GOWORK=off); only module "+res.ModulePath+" is analyzed."))
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	goroot, err := checkToolchain(abs)
	if err != nil {
		res.fatal("unsupported_layout", err.Error())
		return res
	}

	cfg := &packages.Config{
		Context:    ctx,
		Mode:       packages.LoadAllSyntax | packages.NeedModule,
		Dir:        abs,
		Env:        Env(),
		BuildFlags: []string{"-tags="},
		Tests:      false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		res.fatal("unsupported_layout", "package loading failed: "+err.Error())
		return res
	}
	var all []*packages.Package
	packages.Visit(pkgs, nil, func(p *packages.Package) { all = append(all, p) })
	sort.Slice(all, func(i, j int) bool { return all[i].PkgPath < all[j].PkgPath })

	l := &locator{repo: abs, goroot: filepath.Join(goroot, "src"), modulePath: res.ModulePath}
	pv := &provenance{modulePath: res.ModulePath, repo: abs, gorootSrc: l.goroot, std: StdlibInventory()}
	taken := addressTaken(all)
	for _, p := range all {
		if testOnly(p) {
			continue // not a package under the profile; inventoryExcluded records it as test_only
		}
		o, perr := pv.origin(p)
		if perr != "" {
			res.fatal("unsupported_layout", perr)
			continue
		}
		u := buildUnit(p, o, l, taken)
		var dups []string
		u.Shard.Symbols, dups = collisions(u.Shard.Symbols)
		for _, id := range dups {
			res.Global = append(res.Global, globalDiag("symbol_id_collision", "error", "two distinct symbols share the ID "+id))
		}
		res.Units[p.PkgPath] = u
	}
	// An import whose target was not visited still keeps its edge: the
	// target becomes a missing unit that says why.
	for _, id := range sortedUnitIDs(res.Units) {
		for _, im := range res.Units[id].Imports {
			if _, ok := res.Units[im.ToUnit]; !ok {
				res.Units[im.ToUnit] = missingUnit(im.ToUnit, pv, "import "+strconv.Quote(im.ToPath)+" of "+id+" was not loaded")
			}
		}
	}
	for _, id := range sortedUnitIDs(res.Units) {
		u := res.Units[id]
		if u.Origin == "module" {
			res.Module = append(res.Module, id)
		}
		u.Shard.Unit.DepHashes = []facts.DepHash{}
		seen := map[string]bool{}
		for _, im := range u.Imports {
			if !seen[im.ToUnit] {
				seen[im.ToUnit] = true
				u.Shard.Unit.DepHashes = append(u.Shard.Unit.DepHashes,
					facts.DepHash{UnitID: im.ToUnit, ContentHash: res.Units[im.ToUnit].Shard.Unit.ContentHash})
			}
		}
		sort.Slice(u.Shard.Unit.DepHashes, func(i, j int) bool {
			return u.Shard.Unit.DepHashes[i].UnitID < u.Shard.Unit.DepHashes[j].UnitID
		})
		u.Shard.Imports = u.Imports
	}
	res.inventoryExcluded(abs, res.Units)
	return res
}

// checkToolchain verifies the go command is the pinned toolchain and that its
// standard library matches the pinned inventory, and returns GOROOT.
func checkToolchain(dir string) (string, error) {
	out, err := goCmd(dir, "env", "GOROOT", "GOVERSION")
	if err != nil {
		return "", fmt.Errorf("go env failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return "", fmt.Errorf("unexpected go env output %q", out)
	}
	goroot, gover := lines[0], lines[1]
	if gover != PinnedGoVersion {
		return "", fmt.Errorf("toolchain %s does not match the pinned %s", gover, PinnedGoVersion)
	}
	std, err := goCmd(dir, "list", "std")
	if err != nil {
		return "", fmt.Errorf("go list std failed: %v", err)
	}
	if strings.TrimSpace(std) != strings.TrimSpace(stdlibInventory) {
		return "", fmt.Errorf("the standard library of %s at %s does not match the pinned inventory", gover, goroot)
	}
	return goroot, nil
}

func goCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	out, err := cmd.Output()
	return string(out), err
}

func sortedUnitIDs(m map[string]*UnitInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// XToolsVersion reports the linked golang.org/x/tools version.
func XToolsVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "golang.org/x/tools" {
				return d.Version
			}
		}
	}
	return "unknown"
}

// provenance decides a unit's origin from loader facts and checks them.
type provenance struct {
	modulePath, repo, gorootSrc string
	std                         map[string]bool
}

func inModulePath(modulePath, id string) bool {
	return id == modulePath || strings.HasPrefix(id, modulePath+"/")
}

// origin returns the unit origin, or a non-empty error if the loader's facts
// contradict each other (for example a GOROOT package not in the inventory).
func (pv *provenance) origin(p *packages.Package) (string, string) {
	switch {
	case p.Module != nil && p.Module.Main:
		if filepath.Clean(p.Module.Dir) != pv.repo {
			return "", fmt.Sprintf("package %s claims the main module but its module dir is %s", p.PkgPath, p.Module.Dir)
		}
		return "module", ""
	case p.Module != nil:
		return "dependency", ""
	}
	dir := pkgDir(p)
	if pv.std[p.PkgPath] {
		if dir != "" && !within(pv.gorootSrc, dir) {
			return "", fmt.Sprintf("package %s is in the stdlib inventory but was loaded from %s, outside GOROOT", p.PkgPath, dir)
		}
		return "stdlib", ""
	}
	if dir != "" && within(pv.gorootSrc, dir) {
		return "", fmt.Sprintf("package %s was loaded from GOROOT but is not in the pinned stdlib inventory", p.PkgPath)
	}
	if inModulePath(pv.modulePath, p.PkgPath) {
		return "module", "" // unresolvable path inside the main module: governed module code
	}
	return "unresolved", ""
}

// testOnly reports a main-module directory matched by ./... that holds only
// _test.go files: go list reports it with "no non-test Go files", which is an
// exclusion (test_only), not a load failure.
func testOnly(p *packages.Package) bool {
	if p.Module == nil || !p.Module.Main || len(p.GoFiles) > 0 || len(p.CompiledGoFiles) > 0 || p.Dir == "" {
		return false
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return false
	}
	tests := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if !strings.HasSuffix(e.Name(), "_test.go") {
			return false
		}
		tests++
	}
	return tests > 0
}

func pkgDir(p *packages.Package) string {
	for _, f := range append(append([]string{}, p.CompiledGoFiles...), p.GoFiles...) {
		return filepath.Dir(f)
	}
	return p.Dir
}

func within(root, path string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, "../")
}

// locator turns file names into schema locations.
type locator struct {
	repo, goroot, modulePath string
}

func (l *locator) rel(p *packages.Package, file string) (root string, module *string, rel string, ok bool) {
	switch {
	case p.Module != nil && p.Module.Main:
		if !within(l.repo, file) {
			return "", nil, "", false
		}
		r, _ := filepath.Rel(l.repo, file)
		return "repo", nil, filepath.ToSlash(r), true
	case p.Module != nil:
		if !within(p.Module.Dir, file) {
			return "", nil, "", false
		}
		r, _ := filepath.Rel(p.Module.Dir, file)
		return "module", facts.Str(p.Module.Path + "@" + p.Module.Version), filepath.ToSlash(r), true
	default:
		if within(l.goroot, file) {
			r, _ := filepath.Rel(l.goroot, file)
			return "goroot", nil, filepath.ToSlash(r), true
		}
		if within(l.repo, file) { // an unresolved package inside the module tree
			r, _ := filepath.Rel(l.repo, file)
			return "repo", nil, filepath.ToSlash(r), true
		}
		return "", nil, "", false
	}
}

// pos returns the physical location of pos: //line directives are ignored
// (PositionFor with adjusted=false), so evidence always points at real files.
func (l *locator) pos(p *packages.Package, pos token.Pos) *facts.Location {
	if !pos.IsValid() || p.Fset == nil {
		return nil
	}
	return l.loc(p, p.Fset.PositionFor(pos, false))
}

func (l *locator) loc(p *packages.Package, pos token.Position) *facts.Location {
	if !pos.IsValid() || pos.Filename == "" {
		return nil
	}
	root, mod, rel, ok := l.rel(p, pos.Filename)
	if !ok {
		return nil
	}
	col := pos.Column
	if col < 1 {
		col = 1
	}
	return &facts.Location{Root: root, Module: mod, File: rel, Line: pos.Line, Column: col}
}

func missingUnit(id string, pv *provenance, why string) *UnitInfo {
	o := "unresolved"
	if inModulePath(pv.modulePath, id) {
		o = "module"
	}
	d := facts.NewDiagnostic("package_load_error", "error", facts.Str(id), nil, why, nil)
	u := facts.Unit{ID: id, Lang: "go", Origin: o, SourceStatus: "missing", Files: []facts.File{},
		ExcludedFiles: []facts.ExcludedFile{}, ContentHash: facts.ContentHash(nil)}
	if o == "module" {
		u.Module = facts.Str(pv.modulePath)
	}
	return &UnitInfo{Origin: o, Imports: []facts.Import{}, Shard: newShard(u, []facts.Symbol{}, []facts.Diagnostic{d})}
}

func newShard(u facts.Unit, syms []facts.Symbol, diags []facts.Diagnostic) *facts.Shard {
	return &facts.Shard{
		SchemaVersion: facts.SchemaVersion, Kind: "unit_shard", BuildProfileDigest: ProfileDigest(),
		Unit: u, Symbols: syms, ExternalSymbols: []facts.ExtSymbol{}, Imports: []facts.Import{},
		CallSites: []any{}, Routes: []any{}, RouteMiddleware: []any{}, Diagnostics: diags,
	}
}

func buildUnit(p *packages.Package, o string, l *locator, taken map[types.Object]bool) *UnitInfo {
	// Load errors first: they decide the source status, not the reverse.
	diags := loadDiags(p, l)
	u := facts.Unit{ID: p.PkgPath, Lang: "go", Origin: o, Files: []facts.File{}, ExcludedFiles: []facts.ExcludedFile{}}
	switch o {
	case "module":
		// A missing in-module package has no Module record; it still
		// belongs to the main module.
		u.Module = facts.Str(l.modulePath)
	case "dependency":
		u.Module = facts.Str(p.Module.Path + "@" + p.Module.Version)
	}
	compiled := map[string]bool{}
	dir := pkgDir(p)
	for _, f := range p.CompiledGoFiles {
		_, _, rel, ok := l.rel(p, f)
		raw, err := os.ReadFile(f)
		if !ok || err != nil {
			diags = append(diags, facts.NewDiagnostic("package_load_error", "error", facts.Str(p.PkgPath), nil,
				"compiled file "+filepath.Base(f)+" cannot be read or located", nil))
			continue
		}
		compiled[f] = true
		u.Files = append(u.Files, facts.File{Path: rel, SHA256: facts.SHA256(raw)})
	}
	sort.Slice(u.Files, func(i, j int) bool { return u.Files[i].Path < u.Files[j].Path })
	switch {
	case builtinUnits[p.PkgPath]:
		u.SourceStatus = "builtin"
		u.Files = []facts.File{}
	case len(u.Files) > 0:
		u.SourceStatus = "compiled"
	default:
		u.SourceStatus = "missing"
		if !hasError(diags) {
			diags = append(diags, facts.NewDiagnostic("package_load_error", "error", facts.Str(p.PkgPath), nil,
				"package has no compiled Go files under the pinned profile", nil))
		}
	}
	u.ContentHash = facts.ContentHash(u.Files)
	if dir != "" {
		if _, _, rel, ok := l.rel(p, filepath.Join(dir, "x.go")); ok {
			u.Dir = filepath.ToSlash(filepath.Dir(rel))
		}
	}
	if u.SourceStatus == "compiled" {
		u.ExcludedFiles = excludedFiles(p, l, dir, compiled)
	}
	sortDiags(diags)
	sh := newShard(u, symbols(p, l, taken), diags)
	return &UnitInfo{Shard: sh, Origin: o, Imports: imports(p, l)}
}

func hasError(ds []facts.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

func sortDiags(ds []facts.Diagnostic) {
	sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
}

func excludedFiles(p *packages.Package, l *locator, dir string, compiled map[string]bool) []facts.ExcludedFile {
	out := []facts.ExcludedFile{}
	ignored := map[string]bool{}
	for _, f := range p.IgnoredFiles {
		ignored[f] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(dir, name)
		if e.IsDir() || !strings.HasSuffix(name, ".go") || compiled[full] {
			continue
		}
		_, _, rel, ok := l.rel(p, full)
		if !ok {
			continue
		}
		out = append(out, facts.ExcludedFile{Path: rel, Reason: fileReason(name, ignored[full])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func fileReason(name string, ignoredByConstraint bool) string {
	switch {
	case strings.HasSuffix(name, "_test.go"):
		return "test_file"
	case strings.HasPrefix(name, "_") || strings.HasPrefix(name, "."):
		return "ignored_by_go"
	case ignoredByConstraint:
		return "build_constraint"
	}
	return "build_constraint"
}

// loadDiags converts go/packages errors into unit diagnostics.
func loadDiags(p *packages.Package, l *locator) []facts.Diagnostic {
	out := []facts.Diagnostic{}
	seen := map[string]bool{}
	for _, e := range p.Errors {
		kind := "package_load_error"
		if e.Kind == packages.ParseError || e.Kind == packages.TypeError {
			kind = "parse_or_type_error"
		}
		d := facts.NewDiagnostic(kind, "error", facts.Str(p.PkgPath), nil, e.Msg, l.loc(p, errPos(p, l, e.Pos)))
		if !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, d)
		}
	}
	return out
}

// errPos parses "file:line:col". go list reports paths relative to the
// directory it ran in (the module root); go/types reports absolute paths.
func errPos(p *packages.Package, l *locator, s string) token.Position {
	parts := strings.Split(s, ":")
	if len(parts) < 3 {
		return token.Position{}
	}
	col, err1 := strconv.Atoi(parts[len(parts)-1])
	line, err2 := strconv.Atoi(parts[len(parts)-2])
	if err1 != nil || err2 != nil {
		return token.Position{}
	}
	file := strings.Join(parts[:len(parts)-2], ":")
	if !filepath.IsAbs(file) {
		file = filepath.Join(l.repo, file)
	}
	return token.Position{Filename: file, Line: line, Column: col}
}

// imports lists every import spec of the compiled files. An edge is never
// dropped: an unresolvable location gives loc null, an unresolved target keeps
// the path as written (Load then supplies a missing unit for it).
func imports(p *packages.Package, l *locator) []facts.Import {
	out := []facts.Import{}
	for _, f := range p.Syntax {
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue // the parser already reported a syntax error
			}
			to := path
			if ip, ok := p.Imports[path]; ok && ip.PkgPath != "" {
				to = ip.PkgPath
			}
			out = append(out, facts.Import{FromUnit: p.PkgPath, ToUnit: to, ToPath: path, Loc: l.pos(p, spec.Pos())})
		}
	}
	sortImports(out)
	return out
}

func sortImports(imps []facts.Import) {
	key := func(im facts.Import) string {
		if im.Loc == nil {
			return im.ToUnit + "\x00"
		}
		return fmt.Sprintf("%s\x00\x01%s\x00%012d\x00%012d", im.ToUnit, im.Loc.File, im.Loc.Line, im.Loc.Column)
	}
	sort.Slice(imps, func(i, j int) bool { return key(imps[i]) < key(imps[j]) })
}

// addressTaken finds functions and methods used other than as the callee of a
// call, across every loaded package.
func addressTaken(all []*packages.Package) map[types.Object]bool {
	taken := map[types.Object]bool{}
	for _, p := range all {
		if p.TypesInfo == nil {
			continue
		}
		callee := map[*ast.Ident]bool{}
		mark := func(e ast.Expr) {
			switch fn := e.(type) {
			case *ast.Ident:
				callee[fn] = true
			case *ast.SelectorExpr:
				callee[fn.Sel] = true
			}
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					switch fn := ast.Unparen(c.Fun).(type) {
					case *ast.IndexExpr:
						mark(fn.X)
					case *ast.IndexListExpr:
						mark(fn.X)
					default:
						mark(fn)
					}
				}
				return true
			})
		}
		for id, obj := range p.TypesInfo.Uses {
			if fn, ok := obj.(*types.Func); ok && !callee[id] {
				taken[fn.Origin()] = true
			}
		}
	}
	return taken
}

// symbols lists the package's declared funcs, methods, types and vars.
func symbols(p *packages.Package, l *locator, taken map[types.Object]bool) []facts.Symbol {
	out := []facts.Symbol{}
	if p.Types == nil {
		return out
	}
	bodies := map[token.Pos]bool{}
	for _, f := range p.Syntax {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				bodies[fd.Name.Pos()] = fd.Body != nil
			}
		}
	}
	add := func(id, kind, name string, exported bool, obj types.Object) {
		s := facts.Symbol{ID: id, UnitID: p.PkgPath, Kind: kind, Name: name, Exported: exported, Loc: l.pos(p, obj.Pos())}
		if kind == "func" || kind == "method" {
			hasBody, known := bodies[obj.Pos()]
			s.HasBody = hasBody || !known
			s.InUniverse = true
			s.EntryHints.AddressTaken = taken[obj]
			s.EntryHints.IsMain = p.Name == "main" && kind == "func" && name == "main"
		}
		out = append(out, s)
	}
	scope := p.Types.Scope()
	for _, name := range scope.Names() {
		if name == "_" {
			continue
		}
		path := p.PkgPath + "." + name
		switch o := scope.Lookup(name).(type) {
		case *types.Func:
			add(o.FullName(), "func", name, o.Exported(), o)
		case *types.Var:
			add(path, "var", name, o.Exported(), o)
		case *types.TypeName:
			add(path, "type", name, o.Exported(), o)
			named, ok := o.Type().(*types.Named)
			if !ok || o.IsAlias() {
				continue
			}
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				continue
			}
			for i := 0; i < named.NumMethods(); i++ {
				m := named.Method(i)
				add(m.FullName(), "method", m.Name(), m.Exported(), m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// collisions removes duplicate symbol IDs and returns them; the caller turns
// each into a global symbol_id_collision (schema README, rule C3).
func collisions(syms []facts.Symbol) ([]facts.Symbol, []string) {
	var dups []string
	out := syms[:0]
	for i, s := range syms {
		if i > 0 && s.ID == syms[i-1].ID {
			if len(dups) == 0 || dups[len(dups)-1] != s.ID {
				dups = append(dups, s.ID)
			}
			continue
		}
		out = append(out, s)
	}
	return out, dups
}

// inventoryExcluded walks the module tree and records every directory that
// holds Go files but is not analyzed (policy contract and plan section 7:
// the report lists excluded packages and files). Walk errors fail closed.
func (r *Result) inventoryExcluded(root string, units map[string]*UnitInfo) {
	analyzed := map[string]bool{}
	for _, u := range units {
		if u.Origin == "module" && u.Shard.Unit.SourceStatus == "compiled" {
			analyzed[filepath.Join(root, filepath.FromSlash(u.Shard.Unit.Dir))] = true
		}
	}
	id := func(dir string) string {
		rel, _ := filepath.Rel(root, dir)
		if rel == "." {
			return r.ModulePath
		}
		return r.ModulePath + "/" + filepath.ToSlash(rel)
	}
	hasGo := func(dir string) bool {
		found := false
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		return found
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || path == root {
			if path == root {
				r.excludeDir(root, path, analyzed, id)
			}
			return nil
		}
		name := d.Name()
		switch {
		case fileExists(filepath.Join(path, "go.mod")):
			if hasGo(path) {
				r.Excluded = append(r.Excluded, facts.ExcludedPackage{ID: id(path), Reason: "outside_module"})
			}
			return filepath.SkipDir
		case name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_"):
			if hasGo(path) {
				r.Excluded = append(r.Excluded, facts.ExcludedPackage{ID: id(path), Reason: "ignored_by_go"})
			}
			return filepath.SkipDir
		}
		r.excludeDir(root, path, analyzed, id)
		return nil
	})
	if err != nil {
		r.fatal("unsupported_layout", "cannot inventory the module tree: "+err.Error())
	}
	sort.Slice(r.Excluded, func(i, j int) bool { return r.Excluded[i].ID < r.Excluded[j].ID })
	sort.Slice(r.ExcludedFiles, func(i, j int) bool { return r.ExcludedFiles[i].Path < r.ExcludedFiles[j].Path })
}

// excludeDir records a directory with Go files that produced no analyzed
// package: only tests (test_only) or nothing that builds under the profile.
func (r *Result) excludeDir(root, dir string, analyzed map[string]bool, id func(string) string) {
	if analyzed[dir] {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.fatal("unsupported_layout", "cannot read "+dir+": "+err.Error())
		return
	}
	var files []string
	onlyTests := true
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		files = append(files, e.Name())
		if !strings.HasSuffix(e.Name(), "_test.go") {
			onlyTests = false
		}
	}
	if len(files) == 0 {
		return
	}
	reason := "build_constraint"
	if onlyTests {
		reason = "test_only"
	}
	r.Excluded = append(r.Excluded, facts.ExcludedPackage{ID: id(dir), Reason: reason})
	for _, f := range files {
		rel, _ := filepath.Rel(root, filepath.Join(dir, f))
		r.ExcludedFiles = append(r.ExcludedFiles, facts.ExcludedFile{Path: filepath.ToSlash(rel), Reason: fileReason(f, true)})
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
