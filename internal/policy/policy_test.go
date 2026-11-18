package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const base = `version = 1
module = "example.com/demo"

[boundary.app]
packages = ["example.com/demo/**"]
[boundary.private]
packages = ["example.com/demo/internal/auth/private"]

[concept."database.query"]
packs = ["go-stdlib-sql"]
[concept."auth.middleware"]
symbols = ["example.com/demo/internal/auth.RequireAuth"]
`

const rule = `
[[rule]]
id = "r1"
kind = "forbid_import"
scope = "app"
target = "private"
message = "m"
remediation.message = "fix"
`

func TestRepoPolicyIsValid(t *testing.T) {
	res := Load("../../.bedrock/policy.toml", "example.com/demo")
	if res.Diag != nil {
		t.Fatalf("repo policy rejected: %s", res.Diag.Message)
	}
	if got := len(res.Policy.Rules); got != 3 {
		t.Fatalf("rules = %d, want 3", got)
	}
}

func TestFailClosed(t *testing.T) {
	cases := []struct {
		name, src, kind, msg string
	}{
		{"ok", base + rule, "", ""},
		{"zero rules", base, "policy_no_rules", "no [[rule]]"},
		{"syntax", base + "[[rule]\n", "policy_invalid", "TOML syntax error"},
		{"unknown top key", base + "extra = 1\n" + rule, "policy_invalid", `unknown key "extra"`},
		{"unknown rule key", base + rule + "scopes = \"app\"\n", "policy_invalid", `unknown key "scopes"`},
		{"other kind's key", base + rule + "middleware = \"auth.middleware\"\n", "policy_invalid", `unknown key "middleware"`},
		{"unknown remediation key", base + rule + "remediation.hint = \"x\"\n", "policy_invalid", `unknown key "hint"`},
		{"unknown kind", base + strings.Replace(rule, "forbid_import", "forbid_call", 1), "policy_invalid", `unknown kind "forbid_call"`},
		{"duplicate id", base + rule + rule, "policy_invalid", `duplicate rule id "r1"`},
		{"undefined boundary", base + strings.Replace(rule, `target = "private"`, `target = "nope"`, 1), "policy_invalid", `undefined boundary "nope"`},
		{"bad glob", strings.Replace(base, `"example.com/demo/**"`, `"example.com/demo*"`, 1) + rule, "policy_invalid", "wildcard must be a whole segment"},
		{"unknown pack", strings.Replace(base, `"go-stdlib-sql"`, `"go-pgx"`, 1) + rule, "policy_invalid", `unknown pack "go-pgx"`},
		{"empty rule array", strings.Replace(base, "module = \"example.com/demo\"\n", "module = \"example.com/demo\"\nrule = []\n", 1), "policy_no_rules", "no [[rule]]"},
		{"malformed symbol ???", strings.Replace(base, `"example.com/demo/internal/auth.RequireAuth"`, `"???"`, 1) + rule, "policy_invalid", `malformed symbol ID "???"`},
		{"symbol without package", strings.Replace(base, `"example.com/demo/internal/auth.RequireAuth"`, `"RequireAuth"`, 1) + rule, "policy_invalid", "malformed symbol ID"},
		{"unused concept malformed", base + "[concept.\"spare\"]\nsymbols = [\"(example.com/x.T.M\"]\n" + rule, "policy_invalid", "malformed symbol ID"},
		{"empty concept", base + "[concept.\"x.y\"]\n" + rule, "policy_invalid", "needs a non-empty packs or symbols"},
		{"version", strings.Replace(base, "version = 1", "version = 2", 1) + rule, "policy_invalid", "version must be 1"},
		{"module mismatch", strings.Replace(base, `module = "example.com/demo"`, `module = "example.com/other"`, 1) + rule, "policy_invalid", "does not match the analyzed module"},
		{"missing remediation", base + strings.Replace(rule, "remediation.message = \"fix\"\n", "", 1), "policy_invalid", `missing required key "remediation"`},
		{"bad except route", base + `
[[rule]]
id = "r2"
kind = "require_middleware"
scope = "app"
middleware = "auth.middleware"
except_routes = ["GET healthz"]
message = "m"
remediation.message = "fix"
`, "policy_invalid", "path must start with /"},
		{"undefined concept", base + `
[[rule]]
id = "r3"
kind = "restrict_concept_to_boundary"
scope = "app"
concept = "db.write"
boundary = "app"
message = "m"
remediation.message = "fix"
`, "policy_invalid", `undefined concept "db.write"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, kind, msg, _ := Parse([]byte(c.src), ".bedrock/policy.toml", "example.com/demo")
			if kind != c.kind || !strings.Contains(msg, c.msg) {
				t.Fatalf("got %q %q, want %q containing %q", kind, msg, c.kind, c.msg)
			}
		})
	}
}

func TestMissingAndSyntaxLocation(t *testing.T) {
	dir := t.TempDir()
	res := Load(filepath.Join(dir, "policy.toml"), "example.com/demo")
	if res.Diag == nil || res.Diag.Kind != "policy_missing" || res.SHA256 != nil {
		t.Fatalf("missing policy: %+v", res)
	}
	p := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(p, []byte("version = 1\nmodule = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = Load(p, "example.com/demo")
	if res.Diag == nil || res.Diag.Kind != "policy_invalid" || res.SHA256 == nil {
		t.Fatalf("syntax error: %+v", res)
	}
}

func TestSymbolIDGrammar(t *testing.T) {
	good := []string{"example.com/demo/internal/auth.RequireAuth", "(*database/sql.DB).QueryContext",
		"(example.com/x.T).M", "app/internal/helper.Run", "net/http.HandlerFunc"}
	bad := []string{"???", "", "RequireAuth", "example.com/x.", "(example.com/x.T.M", "(*example.com/x.T).", "example.com/x.Run$1",
		"(*example.com/x.T).M$bound", "example.com/x.Map[int]", " example.com/x.F", "example.com/x.F "}
	for _, s := range good {
		if !ValidSymbolID(s) {
			t.Errorf("ValidSymbolID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidSymbolID(s) {
			t.Errorf("ValidSymbolID(%q) = true, want false", s)
		}
	}
}
