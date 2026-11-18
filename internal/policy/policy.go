// Package policy parses and validates .bedrock/policy.toml against the policy
// contract v1 (docs/superpowers/specs/policy/README.md). Validation fails
// closed: every problem becomes a global diagnostic (policy_missing,
// policy_invalid or policy_no_rules) and no rule is evaluated.
package policy

import (
	"errors"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/glob"
)

// Rule kinds.
const (
	ForbidImport              = "forbid_import"
	RequireMiddleware         = "require_middleware"
	RestrictConceptToBoundary = "restrict_concept_to_boundary"
	packGoStdlibSQL           = "go-stdlib-sql"
	conceptDatabaseQuery      = "database.query"
)

// Policy is a validated policy.
type Policy struct {
	Version    int
	Module     string
	Boundaries map[string][]string // name -> import-path patterns
	Concepts   map[string]Concept
	Rules      []Rule
}

// Concept is a named symbol set.
type Concept struct {
	Packs   []string
	Symbols []string
}

// Rule is one [[rule]] entry.
type Rule struct {
	ID, Kind, Scope, Message string
	Remediation              facts.Remediation
	// forbid_import
	Target     string
	ExceptFrom []string
	// require_middleware
	Middleware   string
	ExceptRoutes []string
	// restrict_concept_to_boundary
	Concept, Boundary string
}

// Result of loading a policy: either a Policy or a failure diagnostic.
type Result struct {
	Policy *Policy
	SHA256 *string
	Diag   *facts.Diagnostic // policy_missing, policy_invalid or policy_no_rules
}

// Load reads and validates the policy at path. modulePath is the analyzed
// module's path; the policy's module key must equal it.
func Load(path, modulePath string) Result {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		d := facts.NewDiagnostic("policy_missing", "error", nil, nil,
			fmt.Sprintf("%s not found; no rule can be evaluated.", path), nil)
		return Result{Diag: &d}
	}
	if err != nil {
		d := facts.NewDiagnostic("policy_invalid", "error", nil, nil, fmt.Sprintf("reading %s: %v", path, err), nil)
		return Result{Diag: &d}
	}
	sum := facts.SHA256(raw)
	p, kind, msg, loc := Parse(raw, path, modulePath)
	if kind != "" {
		d := facts.NewDiagnostic(kind, "error", nil, nil, msg, loc)
		return Result{SHA256: &sum, Diag: &d}
	}
	return Result{Policy: p, SHA256: &sum}
}

var (
	boundaryName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	conceptName  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	ruleID       = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	methodToken  = regexp.MustCompile(`^[A-Za-z]+$`)

	// symbolIDShape splits a bindable symbol ID into import path and names:
	// "importpath.Func", "(importpath.Type).Method" or
	// "(*importpath.Type).Method". The import path is "/"-separated elements
	// of letters, digits and ._~+-. Each name is then checked with
	// go/token.IsIdentifier, which rejects keywords and non-decimal digits.
	symbolIDShape = regexp.MustCompile(`^(?:(` + importPathRE + `)\.([^./()*\s]+)` +
		`|\(\*?(` + importPathRE + `)\.([^./()*\s]+)\)\.([^./()*\s]+))$`)
)

const importPathRE = `[\pL\pN_.~+-]+(?:/[\pL\pN_.~+-]+)*`

// ValidSymbolID reports whether s is a canonical bindable symbol ID.
func ValidSymbolID(s string) bool {
	m := symbolIDShape.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	var names []string
	if m[1] != "" {
		names = []string{m[2]}
	} else {
		names = []string{m[4], m[5]}
	}
	for _, n := range names {
		if !token.IsIdentifier(n) {
			return false
		}
	}
	return true
}

// policyLoc returns the location of a policy parse error.
func policyLoc(path string, line, col int) *facts.Location {
	if line < 1 {
		return nil
	}
	if col < 1 {
		col = 1
	}
	// Only a clean relative path is a valid repo-relative location; a policy
	// outside the analyzed module (CI's base-branch copy) gets loc null.
	file := strings.TrimPrefix(path, "./")
	if strings.HasPrefix(file, "/") || strings.Contains(file, "..") || strings.Contains(file, "\\") {
		return nil
	}
	return &facts.Location{Root: "repo", File: file, Line: line, Column: col}
}

type validator struct{ errs []string }

func (v *validator) errf(format string, a ...any) { v.errs = append(v.errs, fmt.Sprintf(format, a...)) }

// keysOnly rejects keys of t outside allowed.
func (v *validator) keysOnly(where string, t map[string]any, allowed ...string) {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var unknown []string
	for k := range t {
		if !ok[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		v.errf("unknown key %q in %s", k, where)
	}
}

func (v *validator) str(where string, t map[string]any, key string, required bool) string {
	raw, ok := t[key]
	if !ok {
		if required {
			v.errf("missing required key %q in %s", key, where)
		}
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		v.errf("%s.%s must be a string", where, key)
		return ""
	}
	if required && s == "" {
		v.errf("%s.%s must not be empty", where, key)
	}
	return s
}

func (v *validator) strs(where string, t map[string]any, key string, required, nonEmpty bool) []string {
	raw, ok := t[key]
	if !ok {
		if required {
			v.errf("missing required key %q in %s", key, where)
		}
		return []string{}
	}
	arr, ok := raw.([]any)
	if !ok {
		v.errf("%s.%s must be an array of strings", where, key)
		return []string{}
	}
	out := []string{}
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			v.errf("%s.%s[%d] must be a string", where, key, i)
			continue
		}
		out = append(out, s)
	}
	if nonEmpty && len(out) == 0 {
		v.errf("%s.%s must not be empty", where, key)
	}
	return out
}

func table(raw any) (map[string]any, bool) {
	t, ok := raw.(map[string]any)
	return t, ok
}

// Parse validates policy bytes. On failure it returns the diagnostic kind,
// message and location; kind is "" on success.
func Parse(raw []byte, path, modulePath string) (*Policy, string, string, *facts.Location) {
	var doc map[string]any
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return nil, "policy_invalid", "TOML syntax error: " + pe.Message, policyLoc(path, pe.Position.Line, pe.Position.Col)
		}
		return nil, "policy_invalid", "TOML error: " + err.Error(), nil
	}
	v := &validator{}
	v.keysOnly("the top level", doc, "version", "module", "boundary", "concept", "rule")
	p := &Policy{Boundaries: map[string][]string{}, Concepts: map[string]Concept{}}

	switch ver := doc["version"].(type) {
	case nil:
		v.errf("missing required key \"version\"")
	case int64:
		if ver != 1 {
			v.errf("version must be 1, got %d", ver)
		}
		p.Version = int(ver)
	default:
		v.errf("version must be an integer")
	}
	p.Module = v.str("the top level", doc, "module", true)
	if p.Module != "" && modulePath != "" && p.Module != modulePath {
		v.errf("module %q does not match the analyzed module %q", p.Module, modulePath)
	}

	if raw, ok := doc["boundary"]; ok {
		bt, ok := table(raw)
		if !ok {
			v.errf("boundary must be a table")
		}
		for _, name := range sortedKeys(bt) {
			where := "[boundary." + name + "]"
			if !boundaryName.MatchString(name) {
				v.errf("invalid boundary name %q", name)
			}
			t, ok := table(bt[name])
			if !ok {
				v.errf("%s must be a table", where)
				continue
			}
			v.keysOnly(where, t, "packages")
			pats := v.strs(where, t, "packages", true, true)
			for _, pat := range pats {
				if err := glob.Validate(pat); err != nil {
					v.errf("%s: invalid pattern %q: %v", where, pat, err)
				}
			}
			p.Boundaries[name] = pats
		}
	}
	if raw, ok := doc["concept"]; ok {
		ct, ok := table(raw)
		if !ok {
			v.errf("concept must be a table")
		}
		for _, name := range sortedKeys(ct) {
			where := fmt.Sprintf("[concept.%q]", name)
			if !conceptName.MatchString(name) {
				v.errf("invalid concept name %q", name)
			}
			t, ok := table(ct[name])
			if !ok {
				v.errf("%s must be a table", where)
				continue
			}
			v.keysOnly(where, t, "packs", "symbols")
			c := Concept{Packs: v.strs(where, t, "packs", false, false), Symbols: v.strs(where, t, "symbols", false, false)}
			if len(c.Packs) == 0 && len(c.Symbols) == 0 {
				v.errf("%s needs a non-empty packs or symbols", where)
			}
			for _, pk := range c.Packs {
				if pk != packGoStdlibSQL {
					v.errf("%s: unknown pack %q", where, pk)
				} else if name != conceptDatabaseQuery {
					v.errf("%s: pack %q has no binding for concept %q", where, pk, name)
				}
			}
			for _, s := range c.Symbols { // every bound symbol, used by a rule or not
				if !ValidSymbolID(s) {
					v.errf("%s: malformed symbol ID %q (want importpath.Func, (importpath.Type).Method or (*importpath.Type).Method)", where, s)
				}
			}
			p.Concepts[name] = c
		}
	}

	rules, ok := doc["rule"]
	var list []map[string]any
	switch r := rules.(type) {
	case nil:
	case []map[string]any:
		list = r
	case []any:
		if len(r) > 0 { // "rule = []" is zero rules: policy_no_rules below
			v.errf("rule must be an array of tables ([[rule]])")
		}
	default:
		if ok {
			v.errf("rule must be an array of tables ([[rule]])")
		}
	}
	seen := map[string]bool{}
	for i, t := range list {
		where := fmt.Sprintf("[[rule]] #%d", i+1)
		r := Rule{}
		r.ID = v.str(where, t, "id", true)
		if r.ID != "" {
			where = fmt.Sprintf("[[rule]] %q", r.ID)
			if !ruleID.MatchString(r.ID) {
				v.errf("%s: malformed id", where)
			}
			if seen[r.ID] {
				v.errf("duplicate rule id %q", r.ID)
			}
			seen[r.ID] = true
		}
		r.Kind = v.str(where, t, "kind", true)
		common := []string{"id", "kind", "scope", "message", "remediation"}
		switch r.Kind {
		case ForbidImport:
			v.keysOnly(where, t, append(common, "target", "except_from")...)
			r.Target = v.str(where, t, "target", true)
			r.ExceptFrom = v.strs(where, t, "except_from", false, false)
			v.boundaryRef(p, where, "target", r.Target)
			for _, b := range r.ExceptFrom {
				v.boundaryRef(p, where, "except_from", b)
			}
		case RequireMiddleware:
			v.keysOnly(where, t, append(common, "middleware", "except_routes")...)
			r.Middleware = v.str(where, t, "middleware", true)
			r.ExceptRoutes = v.strs(where, t, "except_routes", false, false)
			v.conceptRef(p, where, "middleware", r.Middleware)
			for _, rp := range r.ExceptRoutes {
				if err := validRoutePattern(rp); err != nil {
					v.errf("%s: invalid route pattern %q: %v", where, rp, err)
				}
			}
		case RestrictConceptToBoundary:
			v.keysOnly(where, t, append(common, "concept", "boundary")...)
			r.Concept = v.str(where, t, "concept", true)
			r.Boundary = v.str(where, t, "boundary", true)
			v.conceptRef(p, where, "concept", r.Concept)
			v.boundaryRef(p, where, "boundary", r.Boundary)
		case "":
		default:
			v.errf("%s: unknown kind %q", where, r.Kind)
		}
		r.Scope = v.str(where, t, "scope", true)
		v.boundaryRef(p, where, "scope", r.Scope)
		r.Message = v.str(where, t, "message", true)
		if rem, ok := t["remediation"]; !ok {
			v.errf("%s: missing required key \"remediation\"", where)
		} else if rt, ok := table(rem); !ok {
			v.errf("%s: remediation must be a table", where)
		} else {
			v.keysOnly(where+" remediation", rt, "message", "suggest")
			r.Remediation = facts.Remediation{
				Message: v.str(where+" remediation", rt, "message", true),
				Suggest: v.strs(where+" remediation", rt, "suggest", false, false),
			}
		}
		p.Rules = append(p.Rules, r)
	}

	if len(v.errs) > 0 {
		return nil, "policy_invalid", strings.Join(v.errs, "; "), nil
	}
	if len(p.Rules) == 0 {
		return nil, "policy_no_rules", "Policy defines no [[rule]] entries.", nil
	}
	return p, "", "", nil
}

func (v *validator) boundaryRef(p *Policy, where, key, name string) {
	if name == "" {
		return
	}
	if _, ok := p.Boundaries[name]; !ok {
		v.errf("%s: %s references undefined boundary %q", where, key, name)
	}
}

func (v *validator) conceptRef(p *Policy, where, key, name string) {
	if name == "" {
		return
	}
	if _, ok := p.Concepts[name]; !ok {
		v.errf("%s: %s references undefined concept %q", where, key, name)
	}
}

// validRoutePattern checks "[METHOD ]PATH" (policy contract section 6.2).
func validRoutePattern(s string) error {
	method, path, hasMethod := strings.Cut(s, " ")
	if !hasMethod {
		path = method
	} else if !methodToken.MatchString(method) {
		return fmt.Errorf("method %q must match ^[A-Za-z]+$", method)
	}
	if !strings.HasPrefix(path, "/") {
		return errors.New("path must start with /")
	}
	if strings.ContainsAny(path, " \t") {
		return errors.New("whitespace in path")
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if strings.Contains(seg, "*") && seg != "*" && seg != "**" {
			return fmt.Errorf("wildcard must be a whole segment in %q", seg)
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
