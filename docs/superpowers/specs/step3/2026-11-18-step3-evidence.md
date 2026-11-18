# Step 3 evidence: route and middleware extraction, require_middleware

**Date:** 2026-11-18
**Plan:** `../2026-11-17-bedrock-poc-plan.md`, section 8, step 3
**Branch:** `LavishGent/poc-step3`, cut from the N2 fix (`eb837d1`) on `LavishGent/poc-step2`

## What was built

- **`internal/analyze/routes.go`** extracts routes per the router support matrix v1 (`../routers/README.md`) on SSA of module code:
  - router identity: constructors, `Group`/`Route` literals, `With` values, `DefaultServeMux`, and escape analysis;
  - chi chains with `With`/`Group`/`Route` snapshot inheritance, and a late `Use` diagnosed and credited to no route;
  - net/http patterns replayed on a scratch `ServeMux` per mux, so duplicates and conflicts are rejected with no fact;
  - chi patterns and methods validated on a scratch chi router;
  - the bounded subset;
  - case-preserving net/http methods;
  - chi `/api` and `/api/` kept distinct;
  - rule-independent recognition of the direct net/http wrapper `W(h)`.
- **Shards** now carry `routes` and `route_middleware` facts, plus the closure, package-initializer and `$bound` wrapper symbols they reference, with `external_symbols` entries. Route diagnostics go to the registering unit. The schema did not need to change (still 0.4).
- **`internal/check/middleware.go`** evaluates `require_middleware` per policy contract 6.2:
  - scope is projected from the registering package;
  - `except_routes` are matched with case-sensitive methods and segment globs;
  - a route is satisfied by any concept symbol anywhere in its effective chain;
  - unmatched exceptions are warnings;
  - diagnostic relevance follows router matrix section 2 (identity diagnostics reach every rule; site and path diagnostics reach rules covering the unit; chain and handler diagnostics reach rules that govern the route and do not except it).
- **chi direct methods** (`Patch`, `Head`, `Options`, `Connect`, `Trace`, `Query`) are now supported with `Method(X)` semantics (coordinator decision). The router matrix audit table and `testdata/routerfixtures/surface` were updated.
- **`restrict_concept_to_boundary`** is still `rule_not_evaluated` (fail closed) until step 4.

## Results

From the repository root, with go1.26.11:

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerrules; reports + shards): byte-identical
schemacheck over 388 emitted documents: exit 0
```

The commands were:

- `bedrock check --policy .bedrock/policy.toml demo/clean`
- `bedrock check --policy .bedrock/policy.toml demo/violating`
- `bedrock check testdata/<fixture>`, using each fixture's own policy

**`demo/clean`:** `forbid_import` and `require_middleware` pass, and `restrict_concept_to_boundary` is `INDETERMINATE` (`rule_not_evaluated`), so exit 2.

**`demo/violating`:** exit 1, with exactly two violations:
- `internal/billing/billing.go:9` (`forbid_import`);
- `internal/server/server.go:18`, the `GET /api/v1/admin/users` registration (`require_middleware`).

`testdata/broken`, `testdata/regress` and `testdata/regress-missing` are unchanged from step 2.

Routes extracted from `demo/violating/internal/server` (`id -> handler, line`), then the middleware of each route (`route, middleware, source, order`):

```
route:example.com/demo/internal/server:GET:/api/v1/admin/users#0 -> (*example.com/demo/internal/handler.Handlers).AdminUsers line 18
route:example.com/demo/internal/server:GET:/api/v1/billing/summary#0 -> (*example.com/demo/internal/handler.Handlers).BillingSummary line 23
route:example.com/demo/internal/server:GET:/api/v1/items#0 -> (*example.com/demo/internal/handler.Handlers).ListItems line 21
route:example.com/demo/internal/server:GET:/healthz#0 -> (*example.com/demo/internal/handler.Handlers).Healthz line 17
route:example.com/demo/internal/server:GET:/internal/export#0 -> (*example.com/demo/internal/handler.Handlers).Export line 32
route:example.com/demo/internal/server:POST:/api/v1/items#0 -> (*example.com/demo/internal/handler.Handlers).CreateItem line 22
   GET:/api/v1/billing/summary#0 example.com/demo/internal/auth.RequireAuth use 0
   GET:/api/v1/items#0 example.com/demo/internal/auth.RequireAuth use 0
   GET:/internal/export#0 example.com/demo/internal/auth.RequireAuth direct_wrapper 0
   POST:/api/v1/items#0 example.com/demo/internal/auth.RequireAuth use 0
```

`GET /api/v1/admin/users` (line 18) has no middleware, so it is the violation. `GET /healthz` has none either, but it matches the `GET /healthz` exception.

## Router fixture conformance

- **Accepted chains:** each `chains.Accepted` case yields the README's expected chain (`TestAcceptedChainsStatic`).
- **Late `Use`:** each `chains.Rejected` case yields `unsupported_middleware_form` and credits nothing (`TestRejectedLateUseStatic`).
- **Annotated forms:** every `// want` line in `surface/unsupported.go` and `conditional/conditional.go` yields its diagnostic, and no other line does. Uncertain chains never count as authenticated (`TestAnnotatedDiagnostics`).
- **Two rules and an unmatched exception:** `testdata/routerrules` (`TestRouterRulesTwoRules`):
  - `mux.Handle("GET /export", Auth(...))` satisfies `needs-auth` and violates `needs-audit`;
  - `except_routes = ["GET /nomatch"]` is a warning;
  - the unprotected `GET /open` violates both rules.
- **Projection:** chain uncertainty reaches only the rules that govern the route and do not except it, while unknown identity reaches every rule (`TestRouteDiagProjection`).

Every emitted shard and report passes `spike/schemacheck`, including all 388 documents from the two repeat-run fixtures above and the full `testdata/routerfixtures` run (`TestRouterFixtureShardsValidate`). Repeat runs are byte-identical. `go build`, `go vet` and `go test` pass in the root module, both demos and `testdata/routerfixtures`.

## Review fixes (Codex step 3 review)

Every fix fails closed. Each Codex probe runs as an isolated one-file module in `cmd/bedrock` `TestReviewProbes`, because unknown registration identity is module-wide.

| Item | Probe | Before | Now |
| --- | --- | --- | --- |
| **B1** alias `type R = chi.Router` | `B1-alias-route-param`, `B1-alias-variable` | routes missed, PASS | routes recognized, violation (exit 1) |
| **B1** registration through `interface{ Handle(string, http.Handler) }` | `B1-foreign-interface` | PASS | `unsupported_router_form`, exit 2 |
| **B2** `ms[0] = Open` before `r.Use(ms...)` | `B2-overwrite-use` | `Auth` credited, PASS | `unsupported_middleware_form`, exit 2 |
| **B2** conditional overwrite | `B2-conditional-overwrite-use` | PASS | exit 2 |
| **B2** the same through `With` | `B2-overwrite-with` | PASS | exit 2 |
| **B3** `var register = http.HandleFunc` | `B3-package-function-alias` | route missed, PASS | `unsupported_router_form`, exit 2 |
| **B3** registration function passed as an argument | `B3-passed-as-argument` | PASS | exit 2 |
| **B3** method expression stored in a global | `B3-method-expression-escapes` | PASS | exit 2 |
| **B3** local alias or method expression called directly | `B3-local-indirect`, `B3-method-expression` | (SSA makes it a static call) | recognized precisely, violation (exit 1) |
| **S1** middleware typed with `type H = http.Handler` | `TestAliasMiddlewareConcept`, `S1-alias-middleware-type` | rejected as `policy_invalid` | accepted (exit 0); a genuinely wrong signature is still `policy_invalid` |
| **S2** registration in `init()` | `S2-init-*`, fixture `testdata/routerinit` (`TestRouterInitFixture`) | `registered_in` referenced an unemitted symbol, so the shard was invalid | `pkg.init`, `pkg.init#1` and `pkg.init#2` emitted; shards pass schemacheck |

Results after the fixes. `demo/clean` and `demo/violating` are unchanged, and all other fixtures are as before:

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant []
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerinit; reports + shards): byte-identical
schemacheck: exit 0 over 385 documents, 0 failures
```

## Recheck fixes (Codex recheck of e23f6cd/2e0f416)

Each regression is an isolated module in `cmd/bedrock/probes_test.go`. Each one also runs the probe module's own tests, which send a real `httptest` request to `/private` without credentials and require HTTP 200. That proves the route really is unprotected, so the only correct analyzer answer is non-PASS. Run against the previous `routes.go`, both Codex repros give exit 0 PASS; with the fixes, they give exit 2.

| Item | Regression | Fix | Result |
| --- | --- | --- | --- |
| **B1**: a dependency's `type Mux http.ServeMux` with its own `ServeHTTP` registers and serves `/private`; the module converts `http.NewServeMux()` to `*adapter.Mux`, assigns it to `http.Handler` and calls `ServeHTTP` | `TestRecheckB1AdapterConversion` | A `ChangeType` to a defined type other than the recognized router is an escape. Interface `ServeHTTP` is allowed only while the concrete type is proven, and a phi drops the proof. A direct `(*http.ServeMux).ServeHTTP`/`Handler` call is read-only. | before: exit 0, PASS, HTTP 200. Now: `unsupported_router_form`, exit 2 |
| **B3**: `Register(reg func(*chi.Mux, string, http.HandlerFunc), r *chi.Mux)` calls `reg(r, "/private", h)`, and an external test passes `(*chi.Mux).Get` | `TestRecheckB3ReceiverFirstExternalCaller` | Receiver-first signatures and any dynamic call receiving a router-typed value are registration-shaped | before: exit 0, PASS, HTTP 200. Now: `unsupported_router_form`, exit 2 |
| **B3** (extension): a router-typed parameter handed to a dependency that registers on it | `TestRecheckRouterParamToDependency` | A static call into unanalyzed code (not module, chi or net/http) with a router-typed argument is unknown identity | `unsupported_router_form`, exit 2; HTTP 200 proven |
| **NIT**: the `S1-alias-middleware-type` probe row defined `AuthH` but registered `Auth` | | row removed; `TestAliasMiddlewareConcept` binds and registers `AuthH` | |

Results after the fixes: no new false INDETERMINATE, and `demo/clean` and `demo/violating` are unchanged:

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant []
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerrules; reports + shards): byte-identical
schemacheck: exit 0 over 388 documents, 0 failures
```
