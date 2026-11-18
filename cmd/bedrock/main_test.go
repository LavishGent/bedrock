package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const repoPolicy = "../../.bedrock/policy.toml"

type report struct {
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

func TestDemoClean(t *testing.T) {
	code, raw, facts := run(t, "--policy", repoPolicy, "../../demo/clean")
	r := decode(t, raw)
	if code != 0 || r.Status != "PASS" || len(r.Violations) != 0 {
		t.Fatalf("clean: exit %d status %s violations %d; want 0 PASS 0", code, r.Status, len(r.Violations))
	}
	if len(r.Rules) != 1 || r.Rules[0].Kind != "forbid_import" || r.Rules[0].Status != "PASS" {
		t.Fatalf("clean rules = %+v, want forbid_import PASS only", r.Rules)
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

// A policy whose rules are all of kinds this build does not evaluate must
// not produce a PASS report.
func TestNoEvaluableRuleFailsClosed(t *testing.T) {
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
	if code != 2 || len(raw) != 0 {
		t.Fatalf("exit %d with %d report bytes; want exit 2 and no report", code, len(raw))
	}
}
