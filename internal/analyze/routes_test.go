package analyze

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const fixtures = "../../testdata/routerfixtures"

var loaded *Result

func load(t *testing.T) *Result {
	t.Helper()
	if loaded == nil {
		loaded = Load(context.Background(), fixtures)
		if loaded.Fatal {
			t.Fatalf("loading %s failed: %+v", fixtures, loaded.Global)
		}
	}
	return loaded
}

// expectedChains mirrors chains.Accepted in testdata/routerfixtures/chains
// (whose own test checks the same chains against real chi at run time).
// Fixture i is the (i+1)-th function literal of the package initializer.
var expectedChains = []map[string][]string{
	{"GET /x": {"A"}},
	{"GET /x": {"A", "B"}, "GET /y": {"A"}},
	{"GET /x": {"A", "B"}},
	{"GET /w": {"A", "C"}, "GET /w2": {"A", "B"}},
	{"GET /x": {"A", "B"}},
	{"GET /p/x": {"A", "B"}},
	{"GET /p/x": {"A", "B", "C"}, "GET /p/y": {"A", "B"}},
	{"GET /x": {"A", "B"}, "GET /y": {"A"}, "GET /z": {}},
	{"GET /p/x": {"A", "B", "C", "D"}},
}

const chainsPkg = "example.com/routerfixtures/chains"

// rootLiteral returns N for chains.init$N (and its nested literals).
func rootLiteral(sym string) int {
	rest, ok := strings.CutPrefix(sym, chainsPkg+".init$")
	if !ok {
		return 0
	}
	if i := strings.IndexByte(rest, '$'); i >= 0 {
		rest = rest[:i]
	}
	n, _ := strconv.Atoi(rest)
	return n
}

func short(sym string) string { return sym[strings.LastIndex(sym, ".")+1:] }

func TestAcceptedChainsStatic(t *testing.T) {
	res := load(t)
	got := map[int]map[string][]string{}
	for _, rt := range res.Routes {
		n := rootLiteral(rt.Fact.RegisteredIn)
		if n == 0 || n > len(expectedChains) {
			continue
		}
		if len(rt.Uncertain) > 0 {
			t.Errorf("accepted fixture %d route %s has an uncertain chain", n, rt.Fact.ID)
		}
		if got[n] == nil {
			got[n] = map[string][]string{}
		}
		chain := []string{}
		for _, e := range rt.Chain {
			chain = append(chain, short(e.Sym))
		}
		got[n][*rt.Fact.Method+" "+*rt.Fact.Path] = chain
	}
	for i, want := range expectedChains {
		if fmt.Sprint(got[i+1]) != fmt.Sprint(want) {
			t.Errorf("accepted fixture %d: chains %v, want %v", i, got[i+1], want)
		}
	}
	for _, d := range res.RouteDiags {
		if n := rootLiteral(deref(d.Diag.SymbolID, "")); n >= 1 && n <= len(expectedChains) {
			t.Errorf("accepted fixture %d has diagnostic %s: %s", n, d.Diag.Kind, d.Diag.Message)
		}
	}
}

// Rejected fixtures (late Use) are chains.init$10..$14: each gets
// unsupported_middleware_form and A is credited to no route.
func TestRejectedLateUseStatic(t *testing.T) {
	res := load(t)
	for n := len(expectedChains) + 1; n <= len(expectedChains)+5; n++ {
		found := false
		for _, d := range res.RouteDiags {
			if rootLiteral(deref(d.Diag.SymbolID, "")) == n && d.Diag.Kind == "unsupported_middleware_form" && strings.Contains(d.Diag.Message, "late Use") {
				found = true
			}
		}
		if !found {
			t.Errorf("rejected fixture init$%d: no late-Use diagnostic", n)
		}
		for _, rt := range res.Routes {
			if rootLiteral(rt.Fact.RegisteredIn) != n {
				continue
			}
			for _, e := range rt.Chain {
				if short(e.Sym) == "A" {
					t.Errorf("rejected fixture init$%d: late Use credited A to %s", n, rt.Fact.ID)
				}
			}
			if len(rt.Uncertain) == 0 {
				t.Errorf("rejected fixture init$%d: route %s must be uncertain", n, rt.Fact.ID)
			}
		}
	}
}

var wantRE = regexp.MustCompile(`// want ([a-z_]+)`)

// wants reads "// want <kind>" annotations: file:line -> kind.
func wants(t *testing.T, rel string) map[int]string {
	t.Helper()
	f, err := os.Open(filepath.Join(fixtures, rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[int]string{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		if m := wantRE.FindStringSubmatch(sc.Text()); m != nil {
			out[line] = m[1]
		}
	}
	return out
}

// Every annotated unsupported or conditional form gets exactly its
// diagnostic at that line, and no route it concerns counts as authenticated.
func TestAnnotatedDiagnostics(t *testing.T) {
	res := load(t)
	for _, rel := range []string{"surface/unsupported.go", "conditional/conditional.go"} {
		want := wants(t, rel)
		got := map[int][]string{}
		for _, d := range res.RouteDiags {
			if d.Diag.Loc != nil && d.Diag.Loc.File == rel {
				got[d.Diag.Loc.Line] = append(got[d.Diag.Loc.Line], d.Diag.Kind)
			}
		}
		var lines []int
		for l := range want {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		for _, l := range lines {
			ok := false
			for _, k := range got[l] {
				if k == want[l] {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s:%d: want %s, got %v", rel, l, want[l], got[l])
			}
		}
		for l, ks := range got {
			if _, annotated := want[l]; !annotated {
				t.Errorf("%s:%d: unexpected diagnostics %v", rel, l, ks)
			}
		}
	}
	// No route in the conditional fixture counts as authenticated by M when
	// its chain is uncertain; routes with resolved chains are listed as such.
	for _, rt := range res.Routes {
		if rt.Fact.Loc == nil || rt.Fact.Loc.File != "conditional/conditional.go" {
			continue
		}
		switch deref(rt.Fact.Path, "") {
		case "/cond-use", "/loop-use", "/computed-mw":
			if len(rt.Uncertain) == 0 {
				t.Errorf("%s must have an uncertain chain", rt.Fact.ID)
			}
		}
	}
}

func TestSeparateMuxDuplicatesKept(t *testing.T) {
	res := load(t)
	n := 0
	for _, rt := range res.Routes {
		if rt.Fact.Path != nil && *rt.Fact.Path == "/b" && strings.HasPrefix(rt.Fact.RegisteredIn, "example.com/routerfixtures/conditional.SeparateMuxes") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("separate-mux duplicates: %d route facts, want 2", n)
	}
}

func TestNormalization(t *testing.T) {
	cases := []struct{ prefix, pattern, want string }{
		{"", "/api", "/api"},
		{"", "/api/", "/api/"},
		{"/api", "/", "/api/"},
		{"/api/", "/x", "/api/x"},
		{"/api", "/x/", "/api/x/"},
		{"", "/items/{id:[0-9]+}", "/items/{id}"},
	}
	for _, c := range cases {
		if got := normalizeChi(joinPrefix(c.prefix, c.pattern)); got != c.want {
			t.Errorf("chi %q + %q = %q, want %q", c.prefix, c.pattern, got, c.want)
		}
	}
	for _, c := range []struct{ raw, method, path, why string }{
		{"GET /x", "GET", "/x", ""},
		{"get /x", "get", "/x", ""},
		{"/x", "", "/x", ""},
		{"GET \t /x", "GET", "/x", ""},
		{"/files/{$}", "", "/files/{$}", ""},
		{"M-SEARCH /x", "", "", "bounded"},
		{"* /x", "", "", "bounded"},
		{"example.com/x", "", "", "host"},
		{"/a/../b", "", "", "segments"},
		{"/x/{a", "", "", "rejects"},
	} {
		m, p, why := netHTTPPattern(newScratch(), c.raw)
		if c.why == "" && (why != "" || m != c.method || p != c.path) {
			t.Errorf("net/http %q = (%q, %q, %q), want (%q, %q)", c.raw, m, p, why, c.method, c.path)
		}
		if c.why != "" && !strings.Contains(why, c.why) {
			t.Errorf("net/http %q: why %q, want it to mention %q", c.raw, why, c.why)
		}
	}
}
