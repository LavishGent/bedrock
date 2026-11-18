// Package analyze loads one Go module under the pinned build profile and
// turns every package in its import closure into a unit shard (schema 0.3):
// unit provenance, source files, content and dependency hashes, declared
// symbols, imports and load diagnostics. Call, route and middleware facts are
// later steps (plan section 8, steps 3 and 4) and stay empty here.
package analyze

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
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

// Profile returns the pinned build profile as recorded in documents.
func Profile() facts.BuildProfile {
	return facts.BuildProfile{GOOS: ProfileGOOS, GOARCH: ProfileGOARCH, CgoEnabled: false,
		Tags: []string{}, GoVersion: PinnedGoVersion, XToolsVersion: PinnedXToolsVersion}
}

// ProfileDigest is the build_profile_digest every shard carries.
func ProfileDigest() string { return facts.SHA256(facts.Canon(Profile())) }

// Env returns the pinned environment for the go command.
func Env() []string {
	return append(os.Environ(),
		"GOOS="+ProfileGOOS, "GOARCH="+ProfileGOARCH, "CGO_ENABLED=0",
		"GOFLAGS=-mod=readonly", "GOWORK=off", "GOTOOLCHAIN=local")
}

// UnitInfo is everything later evaluation needs about one unit.
type UnitInfo struct {
	Shard   *facts.Shard
	Origin  string
	Imports []facts.Import
}

// Result is a loaded module.
type Result struct {
	ModulePath string
	Units      map[string]*UnitInfo // by unit ID; only units with files
	Module     []string             // module unit IDs, sorted
	Global     []facts.Diagnostic   // scope=global diagnostics (layout)
	Fatal      bool                 // an error-severity global diagnostic stopped loading
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

func layout(kind, sev, msg string, loc *facts.Location) facts.Diagnostic {
	return facts.NewDiagnostic(kind, sev, nil, nil, msg, loc)
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
	if _, err := os.Stat(filepath.Join(abs, "vendor", "modules.txt")); err == nil {
		res.fatal("unsupported_layout", "vendor/ directories are not supported (the profile pins GOFLAGS=-mod=readonly)")
		return res
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.work")); err == nil {
			res.Global = append(res.Global, layout("unsupported_layout", "warning",
				"go.work found and ignored (GOWORK=off); only module "+res.ModulePath+" is analyzed.", nil))
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	goroot, gover, err := goEnv(abs)
	if err != nil {
		res.fatal("unsupported_layout", "go env failed: "+err.Error())
		return res
	}
	if gover != PinnedGoVersion {
		res.fatal("unsupported_layout", fmt.Sprintf("toolchain %s does not match the pinned %s", gover, PinnedGoVersion))
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

	l := &locator{repo: abs, goroot: filepath.Join(goroot, "src")}
	taken := addressTaken(all)
	for _, p := range all {
		if u := buildUnit(p, l, taken); u != nil {
			var dups []string
			u.Shard.Symbols, dups = collisions(u.Shard.Symbols)
			for _, id := range dups {
				res.Global = append(res.Global, layout("symbol_id_collision", "error",
					"two distinct symbols share the ID "+id, nil))
			}
			res.Units[p.PkgPath] = u
			if u.Origin == "module" {
				res.Module = append(res.Module, p.PkgPath)
			}
		}
	}
	// Dependency hashes and import consistency need every unit built first.
	for _, id := range sortedUnitIDs(res.Units) {
		u := res.Units[id]
		var imps []facts.Import
		seen := map[string]bool{}
		u.Shard.Unit.DepHashes = []facts.DepHash{}
		for _, im := range u.Imports {
			dep, ok := res.Units[im.ToUnit]
			if !ok {
				continue // unresolved import: the load error on this unit says why
			}
			imps = append(imps, im)
			if !seen[im.ToUnit] {
				seen[im.ToUnit] = true
				u.Shard.Unit.DepHashes = append(u.Shard.Unit.DepHashes,
					facts.DepHash{UnitID: im.ToUnit, ContentHash: dep.Shard.Unit.ContentHash})
			}
		}
		sort.Slice(u.Shard.Unit.DepHashes, func(i, j int) bool {
			return u.Shard.Unit.DepHashes[i].UnitID < u.Shard.Unit.DepHashes[j].UnitID
		})
		sortImports(imps)
		if imps == nil {
			imps = []facts.Import{}
		}
		u.Imports = imps
		u.Shard.Imports = imps
	}
	sort.Strings(res.Module)
	return res
}

func (r *Result) fatal(kind, msg string) {
	r.Global = append(r.Global, layout(kind, "error", msg, nil))
	r.Fatal = true
}

func sortedUnitIDs(m map[string]*UnitInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortImports(imps []facts.Import) {
	sort.Slice(imps, func(i, j int) bool {
		a, b := imps[i], imps[j]
		if a.ToUnit != b.ToUnit {
			return a.ToUnit < b.ToUnit
		}
		if a.Loc.File != b.Loc.File {
			return a.Loc.File < b.Loc.File
		}
		if a.Loc.Line != b.Loc.Line {
			return a.Loc.Line < b.Loc.Line
		}
		return a.Loc.Column < b.Loc.Column
	})
}

func goEnv(dir string) (goroot, version string, err error) {
	cmd := exec.Command("go", "env", "GOROOT", "GOVERSION")
	cmd.Dir = dir
	cmd.Env = Env()
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("unexpected go env output %q", out)
	}
	return lines[0], lines[1], nil
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

// locator turns absolute file names into schema locations.
type locator struct {
	repo, goroot string
}

// root returns the location root, module and relative path for a file of p.
func (l *locator) rel(p *packages.Package, file string) (root string, module *string, rel string, ok bool) {
	switch {
	case p.Module != nil && p.Module.Main:
		r, err := filepath.Rel(l.repo, file)
		if err != nil || strings.HasPrefix(r, "..") {
			return "", nil, "", false
		}
		return "repo", nil, filepath.ToSlash(r), true
	case p.Module != nil:
		r, err := filepath.Rel(p.Module.Dir, file)
		if err != nil || strings.HasPrefix(r, "..") {
			return "", nil, "", false
		}
		return "module", facts.Str(p.Module.Path + "@" + p.Module.Version), filepath.ToSlash(r), true
	default:
		r, err := filepath.Rel(l.goroot, file)
		if err != nil || strings.HasPrefix(r, "..") {
			return "", nil, "", false
		}
		return "goroot", nil, filepath.ToSlash(r), true
	}
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

// origin is the loader's provenance: the main module, another module, or
// GOROOT (no owning module). It is never inferred from the import path.
func origin(p *packages.Package) string {
	switch {
	case p.Module != nil && p.Module.Main:
		return "module"
	case p.Module != nil:
		return "dependency"
	}
	return "stdlib"
}

func buildUnit(p *packages.Package, l *locator, taken map[types.Object]bool) *UnitInfo {
	if len(p.CompiledGoFiles) == 0 {
		return nil
	}
	o := origin(p)
	u := facts.Unit{ID: p.PkgPath, Lang: "go", Origin: o, ExcludedFiles: []facts.ExcludedFile{}}
	switch o {
	case "module":
		u.Module = facts.Str(p.Module.Path)
	case "dependency":
		u.Module = facts.Str(p.Module.Path + "@" + p.Module.Version)
	}
	compiled := map[string]bool{}
	var pkgDir string
	for _, f := range p.CompiledGoFiles {
		_, _, rel, ok := l.rel(p, f)
		if !ok {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		compiled[f] = true
		pkgDir = filepath.Dir(f)
		u.Files = append(u.Files, facts.File{Path: rel, SHA256: facts.SHA256(raw)})
	}
	if len(u.Files) == 0 {
		return nil
	}
	sort.Slice(u.Files, func(i, j int) bool { return u.Files[i].Path < u.Files[j].Path })
	u.ContentHash = facts.ContentHash(u.Files)
	if _, _, rel, ok := l.rel(p, filepath.Join(pkgDir, "x.go")); ok {
		u.Dir = filepath.ToSlash(filepath.Dir(rel))
	}
	ignored := map[string]bool{}
	for _, f := range p.IgnoredFiles {
		ignored[f] = true
	}
	if entries, err := os.ReadDir(pkgDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(pkgDir, name)
			if e.IsDir() || !strings.HasSuffix(name, ".go") || compiled[full] {
				continue
			}
			_, _, rel, ok := l.rel(p, full)
			if !ok {
				continue
			}
			reason := "build_constraint"
			switch {
			case strings.HasSuffix(name, "_test.go"):
				reason = "test_file"
			case strings.HasPrefix(name, "_") || strings.HasPrefix(name, "."):
				reason = "ignored_by_go"
			case !ignored[full]:
				reason = "ignored_by_go"
			}
			u.ExcludedFiles = append(u.ExcludedFiles, facts.ExcludedFile{Path: rel, Reason: reason})
		}
	}
	sort.Slice(u.ExcludedFiles, func(i, j int) bool { return u.ExcludedFiles[i].Path < u.ExcludedFiles[j].Path })

	sh := &facts.Shard{
		SchemaVersion: facts.SchemaVersion, Kind: "unit_shard", BuildProfileDigest: ProfileDigest(),
		Unit: u, Symbols: symbols(p, l, taken), ExternalSymbols: []facts.ExtSymbol{},
		CallSites: []any{}, Routes: []any{}, RouteMiddleware: []any{}, Diagnostics: loadDiags(p, l),
	}
	return &UnitInfo{Shard: sh, Origin: o, Imports: imports(p, l)}
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
		d := facts.NewDiagnostic(kind, "error", facts.Str(p.PkgPath), nil, e.Msg, l.loc(p, errPos(p, e.Pos)))
		if !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// errPos parses "file:line:col" (file may be relative to the package dir).
func errPos(p *packages.Package, s string) token.Position {
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
		if len(p.CompiledGoFiles) > 0 {
			// go list reports paths relative to the directory it ran in;
			// resolve against the module root when the file exists there.
			if p.Module != nil && p.Module.Dir != "" {
				if c := filepath.Join(p.Module.Dir, file); exists(c) {
					file = c
				}
			}
			if !filepath.IsAbs(file) {
				file = filepath.Join(filepath.Dir(p.CompiledGoFiles[0]), filepath.Base(file))
			}
		}
	}
	return token.Position{Filename: file, Line: line, Column: col}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// imports lists the import specs of the compiled files.
func imports(p *packages.Package, l *locator) []facts.Import {
	var out []facts.Import
	for _, f := range p.Syntax {
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			to := path
			if ip, ok := p.Imports[path]; ok && ip.PkgPath != "" {
				to = ip.PkgPath
			}
			loc := l.loc(p, p.Fset.Position(spec.Pos()))
			if loc == nil {
				continue
			}
			out = append(out, facts.Import{FromUnit: p.PkgPath, ToUnit: to, ToPath: path, Loc: *loc})
		}
	}
	return out
}

// addressTaken finds functions and methods used other than as the callee of a
// call, across every loaded package (a use in one package can mark a symbol
// owned by another).
func addressTaken(all []*packages.Package) map[types.Object]bool {
	taken := map[types.Object]bool{}
	for _, p := range all {
		if p.TypesInfo == nil {
			continue
		}
		callee := map[*ast.Ident]bool{}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					switch fn := ast.Unparen(c.Fun).(type) {
					case *ast.Ident:
						callee[fn] = true
					case *ast.SelectorExpr:
						callee[fn.Sel] = true
					case *ast.IndexExpr: // generic call F[T](...)
						if id, ok := fn.X.(*ast.Ident); ok {
							callee[id] = true
						} else if s, ok := fn.X.(*ast.SelectorExpr); ok {
							callee[s.Sel] = true
						}
					case *ast.IndexListExpr:
						if id, ok := fn.X.(*ast.Ident); ok {
							callee[id] = true
						} else if s, ok := fn.X.(*ast.SelectorExpr); ok {
							callee[s.Sel] = true
						}
					}
				}
				return true
			})
		}
		for id, obj := range p.TypesInfo.Uses {
			fn, ok := obj.(*types.Func)
			if !ok || callee[id] {
				continue
			}
			taken[fn.Origin()] = true
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
		s := facts.Symbol{ID: id, UnitID: p.PkgPath, Kind: kind, Name: name, Exported: exported,
			Loc: l.loc(p, p.Fset.Position(obj.Pos()))}
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
		obj := scope.Lookup(name)
		path := p.PkgPath + "." + name
		switch o := obj.(type) {
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
