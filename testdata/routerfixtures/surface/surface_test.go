package surface

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const specPath = "../../../docs/superpowers/specs/routers/README.md"

var rowRE = regexp.MustCompile("^\\| `([^`]+)` \\| (supported|unsupported_router_form|unsupported_middleware_form|not registration) \\|")

// specTable reads the chi API audit table: API name -> classification.
func specTable(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(specPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "### chi v5.5.0 API audit") {
			in = true
			continue
		}
		if in && strings.HasPrefix(line, "#") {
			break
		}
		if m := rowRE.FindStringSubmatch(line); in && m != nil {
			out[m[1]] = m[2]
		}
	}
	if len(out) == 0 {
		t.Fatal("no audit rows found in spec")
	}
	return out
}

// chiAPI lists every exported function and exported-type method in chi's
// root package, as "Name" or "(*T).M" / "(T).M".
func chiAPI(t *testing.T) []string {
	t.Helper()
	dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/go-chi/chi/v5").Output()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files, _ := filepath.Glob(filepath.Join(strings.TrimSpace(string(dir)), "*.go"))
	var api []string
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !fd.Name.IsExported() {
				continue
			}
			if fd.Recv == nil {
				api = append(api, fd.Name.Name)
				continue
			}
			recv := fd.Recv.List[0].Type
			star := ""
			if s, ok := recv.(*ast.StarExpr); ok {
				star, recv = "*", s.X
			}
			id, ok := recv.(*ast.Ident)
			if !ok || !id.IsExported() {
				continue
			}
			api = append(api, "("+star+id.Name+")."+fd.Name.Name)
		}
	}
	sort.Strings(api)
	return api
}

func TestAuditCoversChiAPI(t *testing.T) {
	spec := specTable(t)
	api := chiAPI(t)
	seen := map[string]bool{}
	for _, name := range api {
		seen[name] = true
		if _, ok := spec[name]; !ok {
			t.Errorf("chi v5.5.0 exports %s but the audit table does not classify it", name)
		}
	}
	for name := range spec {
		if !seen[name] {
			t.Errorf("audit table lists %s, which chi v5.5.0 does not export", name)
		}
	}
}

var wantRE = regexp.MustCompile(`// want (unsupported_router_form|unsupported_middleware_form)`)

// TestEveryUnsupportedAPIHasFixture checks the step 3 checklist: each API the
// audit marks unsupported is called in unsupported.go on a line whose want
// comment names the same diagnostic.
func TestEveryUnsupportedAPIHasFixture(t *testing.T) {
	spec := specTable(t)
	src, err := os.ReadFile("unsupported.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "unsupported.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]map[string]bool{} // selector name -> diagnostics on its lines
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok {
				line := lines[fset.Position(c.Pos()).Line-1]
				if m := wantRE.FindStringSubmatch(line); m != nil {
					if called[s.Sel.Name] == nil {
						called[s.Sel.Name] = map[string]bool{}
					}
					called[s.Sel.Name][m[1]] = true
				}
			}
		}
		return true
	})
	for name, class := range spec {
		if !strings.HasPrefix(class, "unsupported") {
			continue
		}
		short := name[strings.LastIndex(name, ".")+1:]
		if !called[short][class] {
			t.Errorf("%s is %s in the spec but unsupported.go has no call to %s with // want %s", name, class, short, class)
		}
	}
}
