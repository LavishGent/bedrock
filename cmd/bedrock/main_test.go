package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const repoPolicy = "../../.bedrock/policy.toml"

type report struct {
	Packages struct {
		Excluded []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"excluded"`
		ExcludedFiles []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"excluded_files"`
	} `json:"packages"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Rules    []struct {
		ID, Kind, Status string
	} `json:"rules"`
	Violations []struct {
		RuleID   string `json:"rule_id"`
		RuleKind string `json:"rule_kind"`
		Location struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		} `json:"location"`
	} `json:"violations"`
	Diagnostics []struct {
		Kind         string  `json:"kind"`
		Scope        string  `json:"scope"`
		UnitID       *string `json:"unit_id"`
		Relevance    string  `json:"relevance"`
		AffectsRules []struct {
			RuleID string `json:"rule_id"`
			Reason string `json:"reason"`
		} `json:"affects_rules"`
		Loc *struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		} `json:"loc"`
	} `json:"diagnostics"`
}

// run executes `bedrock check` in-process and returns the exit code, the raw
// report and the facts directory.
func run(t *testing.T, args ...string) (int, []byte, string) {
	t.Helper()
	facts := t.TempDir()
	var out, errb bytes.Buffer
	code := runCheck(append([]string{"--facts-dir", facts}, args...), &out, &errb)
	return code, out.Bytes(), facts
}

func decode(t *testing.T, raw []byte) report {
	t.Helper()
	var r report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("report: %v\n%s", err, raw)
	}
	return r
}

// schemacheck validates documents with spike/schemacheck (schema + semantic rules).
func schemacheck(t *testing.T, docs ...string) {
	t.Helper()
	args := append([]string{"run", "../../spike/schemacheck", "../../docs/superpowers/specs/schema"}, docs...)
	cmd := exec.Command("go", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("schemacheck failed: %v\n%s", err, out)
	}
}

func writeReport(t *testing.T, dir string, raw []byte) string {
	t.Helper()
	p := filepath.Join(dir, "report.json")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func shardsIn(t *testing.T, dir string) []string {
	t.Helper()
	s, _ := filepath.Glob(filepath.Join(dir, "shards", "*.json"))
	if len(s) == 0 {
		t.Fatal("no shards written")
	}
	return s
}

func TestMain(m *testing.M) {
	// The supervisor re-executes this binary as the analysis child.
	if len(os.Args) > 1 && os.Args[1] == childCmd {
		os.Exit(childMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// Until steps 3-4 land, the demo's require_middleware and
// restrict_concept_to_boundary rules are listed but not evaluated, so the
// clean demo is INDETERMINATE (exit 2), never PASS.
func TestDemoClean(t *testing.T) {
	code, raw, facts := run(t, "--policy", repoPolicy, "../../demo/clean")
	r := decode(t, raw)
	if code != 2 || r.Status != "INDETERMINATE" || len(r.Violations) != 0 {
		t.Fatalf("clean: exit %d status %s violations %d; want 2 INDETERMINATE 0", code, r.Status, len(r.Violations))
	}
	want := map[string]string{"auth-private-import": "PASS", "db-in-repo": "INDETERMINATE", "routes-require-auth": "INDETERMINATE"}
	if len(r.Rules) != 3 {
		t.Fatalf("clean rules = %+v, want all 3 configured rules", r.Rules)
	}
	for _, ru := range r.Rules {
		if want[ru.ID] != ru.Status {
			t.Fatalf("rule %s status %s, want %s", ru.ID, ru.Status, want[ru.ID])
		}
	}
	notEval := 0
	for _, d := range r.Diagnostics {
		if d.Kind == "rule_not_evaluated" {
			notEval++
			if d.Relevance != "indeterminate" || len(d.AffectsRules) != 1 || d.AffectsRules[0].Reason != "not_evaluated" {
				t.Fatalf("rule_not_evaluated diagnostic malformed: %+v", d)
			}
		}
	}
	if notEval != 2 {
		t.Fatalf("rule_not_evaluated diagnostics = %d, want 2", notEval)
	}
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}

func TestDemoViolating(t *testing.T) {
	code, raw, facts := run(t, "--policy", repoPolicy, "../../demo/violating")
	r := decode(t, raw)
	if code != 1 || r.Status != "VIOLATION" {
		t.Fatalf("violating: exit %d status %s; want 1 VIOLATION", code, r.Status)
	}
	if len(r.Violations) != 1 {
		t.Fatalf("violations = %+v, want exactly one", r.Violations)
	}
	if len(r.Rules) != 3 {
		t.Fatalf("rules = %+v, want all 3 configured rules listed", r.Rules)
	}
	v := r.Violations[0]
	if v.RuleID != "auth-private-import" || v.RuleKind != "forbid_import" ||
		v.Location.File != "internal/billing/billing.go" || v.Location.Line != 9 || v.Location.Column != 2 {
		t.Fatalf("violation = %+v, want auth-private-import at internal/billing/billing.go:9:2", v)
	}
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}

func TestBrokenFixture(t *testing.T) {
	code, raw, facts := run(t, "../../testdata/broken")
	r := decode(t, raw)
	if code != 2 || r.Status != "INDETERMINATE" || len(r.Violations) != 0 {
		t.Fatalf("broken: exit %d status %s violations %d; want 2 INDETERMINATE 0", code, r.Status, len(r.Violations))
	}
	found := false
	for _, d := range r.Diagnostics {
		if strings.HasPrefix(d.Kind, "policy_") {
			t.Fatalf("fixture policy rejected: %+v", d)
		}
		if d.Kind == "package_load_error" && d.UnitID != nil && *d.UnitID == "example.com/broken/b" &&
			d.Loc != nil && d.Loc.File == "b/b.go" && d.Loc.Line == 6 && d.Loc.Column == 8 &&
			d.Relevance == "indeterminate" && len(d.AffectsRules) == 1 &&
			d.AffectsRules[0].RuleID == "no-a-root-import" && d.AffectsRules[0].Reason == "unit_in_scope" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no package_load_error for example.com/broken/b at b/b.go:6:8 affecting no-a-root-import:\n%s", raw)
	}
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}

func TestPolicyFailures(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good, err := os.ReadFile(repoPolicy)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ path, kind string }{
		"missing":   {filepath.Join(dir, "absent.toml"), "policy_missing"},
		"empty":     {write("empty.toml", "version = 1\nmodule = \"example.com/demo\"\n"), "policy_no_rules"},
		"malformed": {write("bad.toml", strings.Replace(string(good), "scope = \"app\"", "scopes = \"app\"", 1)), "policy_invalid"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			code, raw, _ := run(t, "--policy", c.path, "../../demo/clean")
			r := decode(t, raw)
			if code != 2 || r.Status != "INDETERMINATE" || len(r.Rules) != 0 || len(r.Violations) != 0 {
				t.Fatalf("exit %d status %s rules %d; want 2 INDETERMINATE rules=[]", code, r.Status, len(r.Rules))
			}
			if len(r.Diagnostics) != 1 || r.Diagnostics[0].Kind != c.kind || r.Diagnostics[0].Relevance != "global" {
				t.Fatalf("diagnostics = %+v, want one global %s", r.Diagnostics, c.kind)
			}
			schemacheck(t, writeReport(t, t.TempDir(), raw))
		})
	}
}

func TestDeterministic(t *testing.T) {
	_, a, fa := run(t, "--policy", repoPolicy, "../../demo/violating")
	_, b, fb := run(t, "--policy", repoPolicy, "../../demo/violating")
	if !bytes.Equal(a, b) {
		t.Fatal("reports differ between runs")
	}
	sa, sb := shardsIn(t, fa), shardsIn(t, fb)
	if len(sa) != len(sb) {
		t.Fatalf("shard counts differ: %d vs %d", len(sa), len(sb))
	}
	for i := range sa {
		x, _ := os.ReadFile(sa[i])
		y, _ := os.ReadFile(sb[i])
		if filepath.Base(sa[i]) != filepath.Base(sb[i]) || !bytes.Equal(x, y) {
			t.Fatalf("shard %s differs between runs", filepath.Base(sa[i]))
		}
	}
}

// All-unsupported policy: the report is still written, every rule is
// INDETERMINATE through rule_not_evaluated, exit 2.
func TestAllRulesUnsupported(t *testing.T) {
	good, err := os.ReadFile(repoPolicy)
	if err != nil {
		t.Fatal(err)
	}
	src := string(good)
	src = src[:strings.Index(src, "[[rule]]")] + src[strings.Index(src, "[[rule]]\nid = \"routes-require-auth\""):]
	p := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	code, raw, _ := run(t, "--policy", p, "../../demo/clean")
	r := decode(t, raw)
	if code != 2 || r.Status != "INDETERMINATE" || len(r.Rules) != 2 {
		t.Fatalf("exit %d status %s rules %+v; want 2 INDETERMINATE with both rules listed", code, r.Status, r.Rules)
	}
	for _, ru := range r.Rules {
		if ru.Status != "INDETERMINATE" {
			t.Fatalf("rule %s status %s, want INDETERMINATE", ru.ID, ru.Status)
		}
	}
	schemacheck(t, writeReport(t, t.TempDir(), raw))
}

// B2/B3 regressions: a direct unsafe import and an import after a //line
// directive are both violations at physical locations.
func TestRegressUnsafeAndLineDirective(t *testing.T) {
	code, raw, facts := run(t, "../../testdata/regress")
	r := decode(t, raw)
	if code != 1 || len(r.Violations) != 2 {
		t.Fatalf("exit %d violations %+v; want 1 with two violations", code, r.Violations)
	}
	got := map[string]string{}
	for _, v := range r.Violations {
		got[v.RuleID] = fmt.Sprintf("%s:%d:%d", v.Location.File, v.Location.Line, v.Location.Column)
	}
	want := map[string]string{"no-unsafe": "unsafeuse/unsafeuse.go:5:8", "no-forbidden-import": "lined/lined.go:7:8"}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("violation %s at %q, want %q (all: %v)", k, got[k], w, got)
		}
	}
	for _, d := range r.Diagnostics {
		if d.Relevance != "warning" {
			t.Fatalf("unexpected relevant diagnostic %+v (a test-only directory is an exclusion, not a load error)", d)
		}
	}
	// S4: excluded coverage with reasons.
	excl := map[string]string{}
	for _, e := range r.Packages.Excluded {
		excl[e.ID] = e.Reason
	}
	wantExcl := map[string]string{
		"example.com/regress/constrained": "build_constraint", "example.com/regress/onlytests": "test_only",
		"example.com/regress/nested": "outside_module", "example.com/regress/testdata": "ignored_by_go",
		"example.com/regress/testplusignored": "build_constraint",
	}
	if fmt.Sprint(excl) != fmt.Sprint(wantExcl) {
		t.Fatalf("packages.excluded = %v, want %v", excl, wantExcl)
	}
	files := map[string]string{}
	for _, f := range r.Packages.ExcludedFiles {
		files[f.Path] = f.Reason
	}
	wantFiles := map[string]string{
		"constrained/constrained.go": "build_constraint", "onlytests/only_test.go": "test_file",
		"nested/nested.go": "ignored_by_go", "testdata/data.go": "ignored_by_go",
		"testplusignored/plugin.go": "build_constraint", "testplusignored/dynamic_test.go": "test_file",
	}
	for f, why := range wantFiles {
		if files[f] != why {
			t.Fatalf("excluded_files[%s] = %q, want %q (all: %v)", f, files[f], why, files)
		}
	}
	assertDisjoint(t, raw)
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}

// B2 regression: a governed package with a load error and no compiled files
// makes its rule INDETERMINATE.
func TestRegressMissingGovernedPackage(t *testing.T) {
	code, raw, facts := run(t, "../../testdata/regress-missing")
	r := decode(t, raw)
	if code != 2 || r.Status != "INDETERMINATE" || len(r.Violations) != 0 {
		t.Fatalf("exit %d status %s; want 2 INDETERMINATE", code, r.Status)
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.UnitID != nil && *d.UnitID == "example.com/regressmissing/absent" && d.Relevance == "indeterminate" &&
			len(d.AffectsRules) == 1 && d.AffectsRules[0].Reason == "unit_in_scope" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no indeterminate load error for the missing governed package:\n%s", raw)
	}
	sh, err := os.ReadFile(filepath.Join(facts, "shards", url.PathEscape("example.com/regressmissing/absent")+".json"))
	if err != nil || !strings.Contains(string(sh), `"source_status": "missing"`) || !strings.Contains(string(sh), `"origin": "module"`) {
		t.Fatalf("absent shard missing or wrong: %v\n%s", err, sh)
	}
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}

// Codex repros through the CLI: malformed concept symbol ID and an empty rule array.
func TestPolicyRepros(t *testing.T) {
	good, err := os.ReadFile(repoPolicy)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ src, kind string }{
		"malformed-symbol":  {strings.Replace(string(good), `"example.com/demo/internal/auth.RequireAuth"`, `"???"`, 1), "policy_invalid"},
		"empty-rule-array":  {"version = 1\nmodule = \"example.com/demo\"\nrule = []\n", "policy_no_rules"},
		"superscript-digit": {strings.Replace(string(good), `"example.com/demo/internal/auth.RequireAuth"`, `"example.com/demo/internal/auth.F²"`, 1), "policy_invalid"},
		"keyword-func":      {strings.Replace(string(good), `"example.com/demo/internal/auth.RequireAuth"`, `"example.com/demo/internal/auth.func"`, 1), "policy_invalid"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "policy.toml")
			if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			code, raw, _ := run(t, "--policy", p, "../../demo/clean")
			r := decode(t, raw)
			if code != 2 || len(r.Rules) != 0 || len(r.Diagnostics) != 1 || r.Diagnostics[0].Kind != c.kind {
				t.Fatalf("exit %d rules %d diags %+v; want 2, rules=[], one %s", code, len(r.Rules), r.Diagnostics, c.kind)
			}
		})
	}
}

// S3: the supervisor turns a child goroutine panic, a hang and a memory
// breach into run-level reports built from parent-observed metadata.
func TestSupervisorFaults(t *testing.T) {
	cases := []struct {
		fault, kind string
		args        []string
	}{
		{"panic", "analyzer_crash", nil},
		{"hang", "analyzer_timeout", []string{"--timeout", "2s"}},
		{"alloc", "analyzer_memory_limit", []string{"--mem-limit-mib", "128"}},
	}
	for _, c := range cases {
		t.Run(c.fault, func(t *testing.T) {
			t.Setenv(faultEnv, c.fault)
			args := append(append([]string{"--policy", repoPolicy}, c.args...), "../../demo/clean")
			code, raw, _ := run(t, args...)
			r := decode(t, raw)
			if code != 2 || r.Status != "INDETERMINATE" || len(r.Diagnostics) != 1 ||
				r.Diagnostics[0].Kind != c.kind || r.Diagnostics[0].Relevance != "global" {
				t.Fatalf("exit %d status %s diags %+v; want 2 with one global %s", code, r.Status, r.Diagnostics, c.kind)
			}
			if len(r.Rules) != 3 {
				t.Fatalf("rules = %+v, want all 3 configured rules, INDETERMINATE", r.Rules)
			}
			schemacheck(t, writeReport(t, t.TempDir(), raw))
		})
	}
}

func TestLimitValidation(t *testing.T) {
	for _, args := range [][]string{{"--timeout", "0s"}, {"--timeout", "2h"}, {"--mem-limit-mib", "8"}, {"--mem-limit-mib", "99999999"}} {
		var out, errb bytes.Buffer
		if code := runCheck(append(args, "../../demo/clean"), &out, &errb); code != 2 || out.Len() != 0 {
			t.Errorf("%v: exit %d, %d report bytes; want 2 and no report", args, code, out.Len())
		}
	}
}

// S1: caller environment that could change the build is ignored.
func TestHostileEnvironment(t *testing.T) {
	t.Setenv("GOFLAGS", "-mod=mod -tags=evil")
	t.Setenv("GOROOT", "/nonexistent/goroot")
	t.Setenv("GOPACKAGESDRIVER", "/bin/false")
	t.Setenv("GOOS", "windows")
	t.Setenv("GOWORK", "/nonexistent/go.work")
	t.Setenv("GOTOOLCHAIN", "go1.99.0")
	t.Setenv("CGO_ENABLED", "1")
	_, a, _ := run(t, "--policy", repoPolicy, "../../demo/violating")
	t.Setenv("GOFLAGS", "")
	for _, k := range []string{"GOROOT", "GOPACKAGESDRIVER", "GOOS", "GOWORK", "GOTOOLCHAIN", "CGO_ENABLED"} {
		os.Unsetenv(k)
	}
	_, b, _ := run(t, "--policy", repoPolicy, "../../demo/violating")
	if !bytes.Equal(a, b) {
		t.Fatalf("report differs under a hostile environment:\n%s", a)
	}
	if decode(t, a).ExitCode != 1 {
		t.Fatalf("hostile environment changed the verdict:\n%s", a)
	}
}

// S3 repro: a policy FIFO with no writer and --timeout 100ms. Before the fix
// the parent re-read the policy after the abort and hung forever. Now a
// report is written and the run exits 2 promptly, whether the child rejects
// the FIFO first (policy_invalid) or the 100ms limit fires first.
func TestPolicyFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "policy.toml")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	for _, timeout := range []string{"100ms", "30s"} {
		start := time.Now()
		code, raw, _ := run(t, "--timeout", timeout, "--policy", fifo, "../../demo/clean")
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("timeout %s: took %s", timeout, elapsed)
		}
		r := decode(t, raw)
		if code != 2 || r.Status != "INDETERMINATE" || len(r.Diagnostics) != 1 || r.Diagnostics[0].Relevance != "global" {
			t.Fatalf("timeout %s: exit %d status %s diags %+v; want 2 with one global diagnostic", timeout, code, r.Status, r.Diagnostics)
		}
		k := r.Diagnostics[0].Kind
		if k != "policy_invalid" && k != "analyzer_timeout" {
			t.Fatalf("timeout %s: diagnostic %s, want policy_invalid (FIFO rejected) or analyzer_timeout", timeout, k)
		}
		if timeout == "30s" && k != "policy_invalid" {
			t.Fatalf("with a generous limit the FIFO must be rejected as policy_invalid, got %s", k)
		}
		schemacheck(t, writeReport(t, t.TempDir(), raw))
	}
}

// S3: a fault while acquiring the policy (before the handshake) still gives a
// report, with the policy metadata marked unavailable and no rules listed.
func TestFaultDuringPolicyAcquisition(t *testing.T) {
	t.Setenv(faultEnv, "hang-policy")
	start := time.Now()
	code, raw, _ := run(t, "--timeout", "1s", "--policy", repoPolicy, "../../demo/clean")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("took %s", elapsed)
	}
	r := decode(t, raw)
	if code != 2 || len(r.Rules) != 0 || len(r.Diagnostics) != 1 || r.Diagnostics[0].Kind != "analyzer_timeout" {
		t.Fatalf("exit %d rules %+v diags %+v; want 2, rules=[], one analyzer_timeout", code, r.Rules, r.Diagnostics)
	}
	if !strings.Contains(string(raw), "policy metadata unavailable") || !strings.Contains(string(raw), `"sha256": null`) {
		t.Fatalf("abort report must mark the policy metadata unavailable:\n%s", raw)
	}
	schemacheck(t, writeReport(t, t.TempDir(), raw))
}

// assertDisjoint checks that no package is both included and excluded.
func assertDisjoint(t *testing.T, raw []byte) {
	t.Helper()
	var r struct {
		Packages struct {
			Included []string `json:"included"`
			Excluded []struct {
				ID string `json:"id"`
			} `json:"excluded"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	inc := map[string]bool{}
	for _, id := range r.Packages.Included {
		inc[id] = true
	}
	for _, e := range r.Packages.Excluded {
		if inc[e.ID] {
			t.Fatalf("%s is both included and excluded", e.ID)
		}
	}
}

// S4b: an unreadable directory inside an excluded tree fails closed.
func TestExcludedSubtreeUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	dir := filepath.Join(t.TempDir(), "regress")
	if err := exec.Command("cp", "-R", "../../testdata/regress", dir).Run(); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "testdata", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x.go"), []byte("package locked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	code, raw, _ := run(t, dir)
	r := decode(t, raw)
	found := false
	for _, d := range r.Diagnostics {
		if d.Kind == "unsupported_layout" && d.Relevance == "global" && strings.Contains(string(raw), "cannot inventory the module tree") {
			found = true
		}
	}
	if code != 2 || r.Status != "INDETERMINATE" || !found {
		t.Fatalf("exit %d status %s; want 2 with a global unsupported_layout for the unreadable subtree:\n%s", code, r.Status, raw)
	}
	schemacheck(t, writeReport(t, t.TempDir(), raw))
}

// S4a repro on the real module Codex used: xxhash v2.3.0's dynamic/ (a test
// file plus a build-ignored file) is excluded, not a load error.
func TestXXHashRepro(t *testing.T) {
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Skip(err)
	}
	src := filepath.Join(strings.TrimSpace(string(out)), "github.com", "cespare", "xxhash", "v2@v2.3.0")
	if _, err := os.Stat(src); err != nil {
		t.Skip("xxhash v2.3.0 not in the module cache")
	}
	dir := filepath.Join(t.TempDir(), "xxhash")
	if err := exec.Command("cp", "-R", src, dir).Run(); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("chmod", "-R", "u+w", dir).Run()
	pol := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(pol, []byte(`version = 1
module = "github.com/cespare/xxhash/v2"
[boundary.all]
packages = ["github.com/cespare/xxhash/v2/**"]
[boundary.unsafe]
packages = ["unsafe"]
[[rule]]
id = "no-unsafe"
kind = "forbid_import"
scope = "all"
target = "unsafe"
message = "No unsafe."
remediation.message = "Remove unsafe."
`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, raw, facts := run(t, "--policy", pol, dir)
	r := decode(t, raw)
	if code != 1 || len(r.Violations) != 1 || r.Violations[0].Location.File != "xxhash_unsafe.go" {
		t.Fatalf("exit %d violations %+v; want 1 with only the xxhash_unsafe.go violation", code, r.Violations)
	}
	for _, d := range r.Diagnostics {
		if d.Kind == "package_load_error" || d.Relevance != "warning" {
			t.Fatalf("unexpected diagnostic %+v", d)
		}
	}
	assertDisjoint(t, raw)
	schemacheck(t, append(shardsIn(t, facts), writeReport(t, facts, raw))...)
}
