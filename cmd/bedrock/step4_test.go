package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// failMarkers returns every file:line in the fixture marked "// FAIL": the
// expected restrict_concept_to_boundary violations.
func failMarkers(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, _ := filepath.Rel(dir, p)
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			if strings.HasSuffix(sc.Text(), "// FAIL") {
				out = append(out, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), n))
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// TestStep4Fixtures is the plan section 10 matrix for
// restrict_concept_to_boundary (testdata/step4, one module per case). Each
// fixture's expected violations are the lines marked "// FAIL"; relevant
// lists the indeterminate diagnostics as kind/reason.
func TestStep4Fixtures(t *testing.T) {
	twoReflect := []string{"reflection_call/unit_in_scope", "reflection_call/unit_in_scope"}
	cases := []struct {
		name     string
		exit     int
		relevant []string
		npt      bool // a no_possible_target exemption in module code
	}{
		{"sharedhelper", 1, nil, false},   // shared-helper bypass
		{"cycles", 1, nil, true},          // cycle via repo passes; unreachable cycle fails
		{"exportedhelper", 1, nil, false}, // exported helper that is not an Entry passes
		{"closure", 1, nil, false},        // closure callers are owned by their package
		{"iface", 1, nil, false},          // interface dispatch (VTA)
		{"cexfail", 1, nil, false},        // Codex counterexample: Entry={b,u}, c fails
		{"cexpass", 0, nil, false},        // ... plus edge b->u: Entry={b}, c passes
		{"escape", 1, nil, false},         // escaped *sql.Stmt and *sql.Conn
		{"reflection", 2, []string{"reflection_call/unit_in_scope"}, false},                      // module reflection gives 2
		{"unsafeptr", 2, []string{"unsafe_pointer/unit_in_scope"}, false},                        // module unsafe gives 2
		{"bodyless", 2, []string{"bodyless_function/unit_in_scope"}, false},                      // module bodyless gives 2
		{"helperuncertain", 2, []string{"reflection_call/reachable_from_governed_entry"}, false}, // through an out-of-scope helper
		{"mixed", 1, []string{"reflection_call/unit_in_scope"}, false},                           // violation + indeterminate gives 1
		{"emptybinding", 2, []string{"empty_match/policy_config"}, false},                        // empty binding gives 2
		// Review of 9a70d44, B1: a concept-bound ordinary function, method,
		// closure body, generic instance, method value or interface target is
		// still checked; only the synthetic wrapper's forwarding call is not.
		{"b1ordinary", 1, nil, false},
		{"b1control", 1, nil, false},
		{"b1method", 1, nil, false},
		{"b1closure", 1, nil, false},
		{"b1generic", 1, nil, false},
		{"b1boundvalue", 1, nil, false},
		{"b1interface", 1, nil, false},
		// B2: reflection is classified from all resolved targets through
		// synthetic wrappers, and a reflective entry point used as a value is
		// reported where it is used.
		{"b2direct", 2, []string{"reflection_call/unit_in_scope"}, false},
		{"b2interface", 2, []string{"reflection_call/unit_in_scope"}, false},
		{"b2bound", 2, twoReflect, false},
		{"b2expression", 2, twoReflect, false},
		{"b2callslice", 2, twoReflect, false},
		{"b2argument", 2, twoReflect, false},
		{"b2field", 2, twoReflect, false},
		{"b2generic", 2, twoReflect, true}, // the generic origin's t.Call has no target: exempt
		{"b2pointerexpr", 2, twoReflect, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join("../../testdata/step4", c.name)
			code, raw, facts := run(t, dir)
			r := decode(t, raw)
			if code != c.exit || r.ExitCode != c.exit {
				t.Fatalf("exit %d (report %d), want %d\n%s", code, r.ExitCode, c.exit, raw)
			}
			var got []string
			for _, v := range r.Violations {
				if v.RuleKind != "restrict_concept_to_boundary" {
					t.Fatalf("unexpected violation %+v", v)
				}
				l := fmt.Sprintf("%s:%d", v.Location.File, v.Location.Line)
				if len(got) == 0 || !contains(got, l) { // a generic body and its instance share a line
					got = append(got, l)
				}
			}
			sort.Strings(got)
			if want := failMarkers(t, dir); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("violations at %v, want the // FAIL lines %v", got, want)
			}
			var rel []string
			npt := false
			for _, d := range r.Diagnostics {
				if d.Relevance == "global" {
					t.Fatalf("unexpected global diagnostic %+v", d)
				}
				if d.Relevance == "indeterminate" {
					for _, a := range d.AffectsRules {
						rel = append(rel, d.Kind+"/"+a.Reason)
					}
				}
				if d.ExemptedBy != nil && *d.ExemptedBy == "no_possible_target" {
					npt = true
				}
			}
			sort.Strings(rel)
			if fmt.Sprint(rel) != fmt.Sprint(c.relevant) && !(len(rel) == 0 && len(c.relevant) == 0) {
				t.Fatalf("relevant diagnostics %v, want %v", rel, c.relevant)
			}
			if npt != c.npt {
				t.Fatalf("no_possible_target exemption present = %v, want %v", npt, c.npt)
			}
			if len(r.Assumptions) != 2 {
				t.Fatalf("assumptions = %+v, want no_possible_target and trusted_stdlib", r.Assumptions)
			}
			schemacheck(t, runDocs(t, facts, raw)...)
		})
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
