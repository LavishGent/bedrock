package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/glob"
	"github.com/LavishGent/bedrock/internal/policy"
)

// middlewareShapeErrors checks every concept used as a middleware binds only
// known functions of type func(http.Handler) http.Handler and no packs.
func middlewareShapeErrors(pol *policy.Policy, res *analyze.Result) string {
	if res.MiddlewareShaped == nil {
		return ""
	}
	var errs []string
	used := map[string]bool{}
	for _, ru := range pol.Rules {
		if ru.Kind == policy.RequireMiddleware {
			used[ru.Middleware] = true
		}
	}
	for name := range used {
		c := pol.Concepts[name]
		if len(c.Packs) > 0 {
			errs = append(errs, fmt.Sprintf("concept %q is used as middleware but binds packs %v; packs bind no middleware", name, c.Packs))
		}
		for _, s := range c.Symbols {
			if shaped, known := res.MiddlewareShaped[s]; known && !shaped {
				errs = append(errs, fmt.Sprintf("concept %q is used as middleware but %s is not of type func(http.Handler) http.Handler", name, s))
			}
		}
	}
	sort.Strings(errs)
	return strings.Join(errs, "; ")
}

// requireMiddleware evaluates one require_middleware rule (policy contract
// 6.2, router matrix section 2). It returns violations, rule-specific
// empty_match diagnostics, warnings (unmatched except_routes), the governed
// units, and the route diagnostics that affect this rule with their reason.
func requireMiddleware(pol *policy.Policy, ru policy.Rule, res *analyze.Result) (
	[]facts.Violation, []facts.Diagnostic, []facts.Diagnostic, map[string]bool, map[string]string) {

	scope := map[string]bool{}
	for _, u := range res.Module {
		for _, pat := range pol.Boundaries[ru.Scope] {
			if glob.Match(pat, u) {
				scope[u] = true
			}
		}
	}
	concept := map[string]bool{}
	known := 0
	for _, s := range pol.Concepts[ru.Middleware].Symbols {
		concept[s] = true
		if _, ok := res.MiddlewareShaped[s]; ok {
			known++
		}
	}
	var empties []facts.Diagnostic
	if len(scope) == 0 {
		empties = append(empties, facts.NewDiagnostic("empty_match", "error", nil, nil,
			fmt.Sprintf("rule %q: scope boundary %q matches no module package; the rule cannot be evaluated.", ru.ID, ru.Scope), nil))
	}
	if known == 0 {
		empties = append(empties, facts.NewDiagnostic("empty_match", "error", nil, nil,
			fmt.Sprintf("rule %q: middleware concept %q matches no loaded function; the rule cannot be evaluated.", ru.ID, ru.Middleware), nil))
	}
	affects := map[string]string{}
	excepted := func(rt *analyze.Route) bool {
		for _, pat := range ru.ExceptRoutes {
			if matchRoute(pat, rt.Fact.Method, rt.Fact.Path) {
				return true
			}
		}
		return false
	}
	governed := func(rt *analyze.Route) bool {
		return scope[rt.Fact.UnitID] && rt.Fact.Path != nil && !excepted(rt)
	}
	for _, d := range res.RouteDiags {
		id := d.Diag.ID
		switch d.Cat {
		case analyze.CatIdentity:
			affects[id] = "route_registration"
		case analyze.CatSite, analyze.CatPath:
			if scope[d.Unit] {
				affects[id] = "route_registration"
			}
		case analyze.CatChain:
			for _, rt := range d.Routes {
				if governed(rt) {
					affects[id] = "middleware_chain"
				}
			}
		case analyze.CatHandler:
			for _, rt := range d.Routes {
				if governed(rt) {
					affects[id] = "route_registration"
				}
			}
		}
	}
	if len(empties) > 0 {
		return nil, empties, nil, scope, affects
	}
	// Unmatched exceptions narrow nothing: warnings, not 2.
	var warnings []facts.Diagnostic
	for _, pat := range ru.ExceptRoutes {
		hit := false
		for _, rt := range res.Routes {
			if scope[rt.Fact.UnitID] && matchRoute(pat, rt.Fact.Method, rt.Fact.Path) {
				hit = true
			}
		}
		if !hit {
			warnings = append(warnings, facts.NewDiagnostic("empty_match", "warning", nil, nil,
				fmt.Sprintf("rule %q: except_routes pattern %q matches no governed route.", ru.ID, pat), nil))
		}
	}
	var vs []facts.Violation
	for _, rt := range res.Routes {
		if !governed(rt) || len(rt.Uncertain) > 0 {
			continue // uncertain chains are reported through their diagnostics
		}
		ok := false
		var chain []string
		for _, e := range rt.Chain {
			chain = append(chain, e.Sym)
			if concept[e.Sym] {
				ok = true
			}
		}
		if ok {
			continue
		}
		method := "*"
		if rt.Fact.Method != nil {
			method = *rt.Fact.Method
		}
		handler := "(unresolved handler)"
		if rt.Fact.HandlerSym != nil {
			handler = *rt.Fact.HandlerSym
		}
		v := facts.Violation{
			RuleID: ru.ID, RuleKind: ru.Kind, Location: rt.Fact.Loc,
			Message: fmt.Sprintf("%s (%s %s has middleware chain [%s], none bound to %s)", ru.Message, method, *rt.Fact.Path,
				strings.Join(chain, ", "), ru.Middleware),
			EvidenceChain: []facts.EvidenceStep{
				{Kind: "route", From: rt.Fact.ID, To: handler, Loc: rt.Fact.Loc},
				{Kind: "middleware_missing", From: rt.Fact.ID, To: ru.Middleware, Loc: nil},
			},
			Remediation: facts.Remediation{Message: ru.Remediation.Message, Suggest: nonNil(ru.Remediation.Suggest)},
		}
		v.ID = facts.ViolID(v)
		vs = append(vs, v)
	}
	return vs, nil, warnings, scope, affects
}

// matchRoute matches an except_routes pattern "[METHOD ]PATH" against a
// normalized route (policy contract 6.2): a method in the pattern must equal
// the route's method exactly (an any-method route matches only method-less
// patterns); PATH segments are literal, "*" (one segment) or "**" (zero or
// more); a trailing "/" is significant.
func matchRoute(pat string, method, path *string) bool {
	if path == nil {
		return false
	}
	pm, pp, hasMethod := strings.Cut(pat, " ")
	if !hasMethod {
		pp = pm
	} else if method == nil || *method != pm {
		return false
	}
	return matchSegs(strings.Split(strings.TrimPrefix(pp, "/"), "/"), strings.Split(strings.TrimPrefix(*path, "/"), "/"))
}

func matchSegs(p, s []string) bool {
	for len(p) > 0 {
		switch p[0] {
		case "**":
			for i := 0; i <= len(s); i++ {
				if matchSegs(p[1:], s[i:]) {
					return true
				}
			}
			return false
		case "*":
			if len(s) == 0 || s[0] == "" {
				return false
			}
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
		}
		p, s = p[1:], s[1:]
	}
	return len(s) == 0
}
