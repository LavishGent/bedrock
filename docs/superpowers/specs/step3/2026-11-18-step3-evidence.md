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

## Router value audit (Codex re-review of 45133a0/b10332d: design fix)

This was the third finding in one class: router values escaping an audit that only tracked constructor-owned values. The per-repro patches are replaced by a whole-module audit, `internal/analyze/audit.go`. The normative rule is the "Router value audit" section of `../routers/README.md`:

- **Seeding.** Every router-bearing SSA source is tracked: parameters, free variables, global loads, allocations, field loads, call results, factories and helper parameters (an interprocedural fixed point), and every conversion, phi, type assertion or extract derived from them.
- **Allowlist.** Each use must be one of: a recognized router operation on it; a return; an enumerated serving API (now including `httptest.NewServer`, `NewUnstartedServer` and `NewTLSServer`); `ServeHTTP` with a proven concrete type; a read-only use; or an argument to an audited module function that tracks it.
- **Everything else** is module-wide `unsupported_router_form`. No further callback signatures were added.

**Regressions** (`cmd/bedrock/audit_test.go`): isolated modules sharing a dependency `example.com/dep` that registers an unauthenticated `/private`. Each Codex source and each adversarial variant first proves, with a real `httptest` request, that `/private` answers 200 without credentials, then asserts exit 2 with `unsupported_router_form`.

- `TestRouterAuditCodexSources` covers the ten Codex sources:
  - a `*http.ServeMux` parameter, `new(http.ServeMux)`, or `http.DefaultServeMux`, each converted to the dependency's adapter type;
  - `Wire(r){dep.SetupAny(r)}`;
  - `dep.SetupBox(&dep.Box{R:r})`;
  - `dep.SetupUnsafe(unsafe.Pointer(r))`;
  - `reflect.ValueOf(r).MethodByName("Get").Call(...)`;
  - `dep.Run(func(){dep.SetupAny(r)})`;
  - a `makeRouter()` factory, and a factory returning `http.Handler`.
- `TestRouterAuditAdversarial` covers my own 13 variants: a generic pass-through, a method value passed out, struct embedding, interface embedding, a channel, a map, `defer`, a `go` statement, a slice, a global, a returned closure, an `any` helper in module code, and a tuple-returning factory.
- `TestRouterAuditNoFalsePositives` stays exit 0 under the auth-only policy. It covers:
  - a chi router served by `http.ListenAndServe`;
  - a ServeMux in an `http.Server`;
  - a serve helper with direct and interface `ServeHTTP` on a proven router;
  - `httptest.NewServer`, a trusted serving sink added with this regression.

Against the previous code (`routes.go` at `b10332d`, without the audit), all ten Codex sources and 9 of the 13 adversarial variants were silent bypasses (exit 0, PASS). The other four were already non-PASS:

```
TestRouterAuditCodexSources/servemux-parameter-adapter -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/new-servemux-adapter -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/default-servemux-adapter -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/wire-setup-any -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/setup-box -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/setup-unsafe -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/reflect-method-call -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/run-closure -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/factory-router -> old code: exit 0 PASS (bypass)
TestRouterAuditCodexSources/factory-interface -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/generic-pass-through -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/method-value-out -> old code: already non-PASS
TestRouterAuditAdversarial/struct-embedding -> old code: already non-PASS
TestRouterAuditAdversarial/interface-embedding -> old code: already non-PASS
TestRouterAuditAdversarial/channel -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/map -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/defer -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/go-statement -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/slice -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/global -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/returned-closure -> old code: already non-PASS
TestRouterAuditAdversarial/module-any-helper -> old code: exit 0 PASS (bypass)
TestRouterAuditAdversarial/tuple-factory -> old code: exit 0 PASS (bypass)
```

**Results after the fix.** `demo/clean` (exit 2; `forbid_import` and `require_middleware` PASS; only `rule_not_evaluated`) and `demo/violating` (exit 1; exactly `billing.go:9` and `server.go:18`) are unchanged. `testdata/routerinit` gains a conservative `unsupported_router_form`, because its initializer stores a router into the package-level `Admin` variable; it still exits 1 on its real violation.

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant [('unsupported_router_form', 'example.com/routerinit')]
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerrules; reports + shards): byte-identical
schemacheck: exit 0 over 388 documents, 0 failures
```

## Origin allowlist (Codex review of 52c28ce/68da0ef: four blocker classes)

**Root cause.** The audit allowlisted uses but treated "tracked" as "covered". The fix adds an **origin allowlist** under a closed-world rule (`../routers/README.md`, "Router value audit: origin and use allowlists", now normative):

- **Covered origins.** A router is covered only if it comes from a recognized constructor in module code, a covered `With`/`Group`/`Route`, an audited factory whose every return is covered, or a closed (unexported, package-level, never-a-value) helper whose every call site is covered.
- **Uncovered sources.** Every other router-bearing source is module-wide `unsupported_router_form` at its origin. Sources are now enumerated over operands too, so globals count.
- **Use allowlist, tightened:**
  - `ServeHTTP`, the read-only methods and the serving sinks need a proven concrete `*http.ServeMux` or `*chi.Mux`, checked before any router-operation allowance, with the exact handler position enforced;
  - `http.Server.Handler` is allowed only on a local, field-write-only server that is used just to serve.
- **Two fixes found while validating:**
  - registrations in a function with `defer` were treated as conditional, because of the recover block;
  - the fixed point treated not-yet-computed factories as uncovered instead of using a greatest fixed point.

**Regressions** (`cmd/bedrock/audit_test.go`), each an isolated module:

| Test | What it covers |
| --- | --- |
| `TestRouterOriginCodexModules` | All nine review modules, each with a real `httptest` request proving `/private` answers 200 unauthenticated, then asserting exit 2: `zero_global`, `server_field_escape`, `server_field_reload`, `server_field_helper`, `chi_interface_dispatch`, `chi_phi_dispatch`, `allocated_adapter_sink`, `dep_adapter_sink`, `dep_factory`. |
| `TestRouterOriginAdversarial` | 40 self-review variants, ten per blocker class, all non-PASS (exit 2). **A, sources:** global pointer, global chi, local zero value, `new(chi.Mux)`, global alias, field load, map read, exported parameter, free variable, type assertion. **B, server:** returned, `Handler` read back, passed to a module helper, global, assigned then passed, method value, goroutine capture, copy, unproven (phi) handler, dependency handler. **C, dispatch and sinks:** `chi.Router` parameter `ServeHTTP`, phi of covered routers, adapter to `ListenAndServe`, `new` adapter to `httptest`, embedding wrapper to a sink, phi to a sink, read-only method on an unproven router, `ServeHTTP` method value, adapter through a closed helper, dependency router served. **D, dependency producers:** returned, served, adapter via `httptest`, through a module helper, extended with `With`, tuple, struct field, dependency global, generic factory, dependency method. |
| `TestRouterOriginLocalServerNoFalsePositive` | Stays exit 0: a local `http.Server` literal with `defer srv.Close()`, a field-store server, and a closed factory plus serve helper. `TestRouterAuditNoFalsePositives` (local build, register, return, `ListenAndServe`, `httptest.NewServer`) also stays exit 0. |
| earlier rounds | All earlier audit, probe and recheck tests still pass unchanged. |

**Against the previous code** (`audit.go` and `routes.go` at `52c28ce`), all nine Codex modules and 28 of the 40 variants were silent bypasses (exit 0 PASS):

```
TestRouterOriginCodexModules/zero_global -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/server_field_escape -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/server_field_reload -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/server_field_helper -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/chi_interface_dispatch -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/chi_phi_dispatch -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/allocated_adapter_sink -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/dep_adapter_sink -> old code: exit 0 PASS (bypass)
TestRouterOriginCodexModules/dep_factory -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/A1-global-pointer -> old code: already non-PASS
TestRouterOriginAdversarial/A2-global-chi -> old code: already non-PASS
TestRouterOriginAdversarial/A3-local-zero-value -> old code: already non-PASS
TestRouterOriginAdversarial/A4-new-chi -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/A5-global-alias -> old code: already non-PASS
TestRouterOriginAdversarial/A6-field-load -> old code: already non-PASS
TestRouterOriginAdversarial/A7-map-read -> old code: already non-PASS
TestRouterOriginAdversarial/A8-exported-parameter -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/A9-free-variable -> old code: already non-PASS
TestRouterOriginAdversarial/A10-type-assertion -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B1-server-returned -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B2-handler-read-back -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B3-server-to-module-helper -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B4-server-global -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B5-handler-assigned-then-passed -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B6-server-method-value -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B7-server-in-goroutine -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B8-server-copied -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B9-unproven-handler -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/B10-dependency-handler -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/C1-chi-router-param-servehttp -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/C2-phi-of-covered-servehttp -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/C3-adapter-to-listenandserve -> old code: already non-PASS
TestRouterOriginAdversarial/C4-new-adapter-to-httptest -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/C5-embedding-wrapper-to-sink -> old code: already non-PASS
TestRouterOriginAdversarial/C6-phi-to-sink -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/C7-read-only-on-unproven -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/C8-servehttp-method-value -> old code: already non-PASS
TestRouterOriginAdversarial/C9-adapter-through-closed-helper -> old code: already non-PASS
TestRouterOriginAdversarial/C10-dependency-router-to-serve -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D1-returned -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D2-served -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D3-adapter-to-httptest -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D4-through-module-helper -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D5-dependency-router-extended -> old code: already non-PASS
TestRouterOriginAdversarial/D6-tuple -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D7-struct-field -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D8-dependency-global -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D9-generic-factory -> old code: exit 0 PASS (bypass)
TestRouterOriginAdversarial/D10-dependency-method -> old code: exit 0 PASS (bypass)
```

**Results.** No false INDETERMINATE was introduced:

- `demo/clean` is exit 2, with `require_middleware` PASS and only `rule_not_evaluated`;
- `demo/violating` is exit 1, with exactly `billing.go:9` and `server.go:18`;
- the other fixtures are unchanged.

The `f := s.Serve` bound-method wrapper limitation is documented as conservative in `../routers/README.md`.

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant [('unsupported_router_form', 'example.com/routerinit')]
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerrules; reports + shards): byte-identical
schemacheck: exit 0 over 388 documents, 0 failures
```

## Supported router form (owner decision after five review rounds)

`require_middleware` is narrowed to one documented form, normative in `../routers/README.md` ("Supported router form") and recorded in the plan. A router is created by a direct `chi.NewRouter`, `chi.NewMux` or `http.NewServeMux` call and fully populated inside one function. It may only be returned, passed directly to an enumerated serving API (or a local, only-served `http.Server`), or handed to a `Group`/`Route` literal. A caller may only serve the result of a form factory directly. Everything else is module-wide `unsupported_router_form`.

**What changed in the code:**

- **Deleted.** The whole-module greatest-fixed-point audit is gone: coverage and proof summaries, closed helpers, factory summaries, and the helper-parameter taint.
- **Kept.** `internal/analyze/audit.go` keeps per-function origin and use checks. The only whole-module fact is a monotone "may return a router" boolean, which makes interface-typed router results visible at their call sites.
- **New rejections:**
  - `http.Handle`/`HandleFunc` (`DefaultServeMux`); the route is still recorded;
  - functions exposed through `//go:linkname`;
  - every helper or factory flow that is not a single-function form factory.
- **Demo.** It already fits the form: `NewAPI` and `NewAdmin` each build and return their router, and `main` serves the results directly. No change was needed.
- **Stale docs.** The README claim that `s.Serve(m)` is accepted was wrong: helpers and methods taking routers are rejected. It now says so.

**Regressions.** Every bypass regression asserts exit 2, or a precise exit 1, and is backed by a real `httptest` request where it applies:

- the nine origin-review modules;
- the 40 adversarial variants;
- all earlier rounds' sources and probes;
- the seven fresh variants of the review of `1acec5b`: `mixed_factory`, `mixed_closed_helper`, `mixed_open_factory`, `open_external_param`, `recursive_factory_unknown`, `closed_variadic` (`TestSupportedFormFreshVariants`), and `linkname_closed` (`TestSupportedFormLinkname`, an actual cross-module `go:linkname`).

**Tests rewritten for the narrower scope:**

- The helper-based no-false-positive cases now expect exit 2: `serve-helper-rejected` and `helper-factory-rejected`.
- `S2-init-registration` uses `http.Handle`, so it now expects exit 2.
- The conditional fixture annotates `register(r)` in `Helper`.

**No false positives.** A single function that builds, registers and returns a router, and is served through `ListenAndServe`, `httptest.NewServer` or a local `http.Server`, stays exit 0. So do direct `ServeHTTP` calls on a proven local router.

**Results:**

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant [('unsupported_router_form', 'example.com/routerinit'), ('unsupported_router_form', 'example.com/routerinit'), ('unsupported_router_form', 'example.com/routerinit')]
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerrules; reports + shards): byte-identical
schemacheck: exit 0 over 388 documents, 0 failures
```

## Factory boundaries (Codex review of 024bbb3/33be3ba: bounded fix)

This is the coordinator's bounded fix inside the narrowed form. The normative text is "Factory boundaries" in `../routers/README.md`.

- **B1, factories as values or through unresolved calls.** A router-returning module function referenced other than as a direct callee is module-wide `unsupported_router_form`. That covers a function value, a global, a field, a map, a bound method or method expression mapped back through its wrapper, and an argument or callback. So is a call through a generated wrapper, and any dynamic or interface call whose signature is identical to a router-returning module function's.
- **B2, dependency-side callers.**
  - `//go:linkname` directives are scanned in all loaded non-stdlib packages, and a target naming a module function exposes it.
  - A module package that contains a router-returning function and is imported by a loaded dependency is rejected at those functions.

**Regressions** (`cmd/bedrock/audit_test.go`), each an isolated module with a real `httptest` request proving `/private` answers 200 unauthenticated, asserting exit 2:

- `TestFactoryBoundaryCodexModules`: the eight review modules `form_global_factory`, `form_bound_factory`, `form_interface_factory`, `form_callback_factory`, `form_dependency_callback`, `form_tuple_global`, `link_factory_dependency_only` and `dependency_import_factory`.
- `TestFactoryBoundaryAdversarial`, five per blocker:
  - **B1:** factory in a struct field, factory in a map, generic call, closure wrapper, and interface method on a pointer receiver;
  - **B2:** linkname pull of an exported factory, a dependency importing an interface-returning factory, a method factory, a wrapped unexported factory, and a linkname pull of a chi factory.

Against the previous code (`024bbb3`), all eight Codex modules and 8 of the 10 variants were silent bypasses (exit 0, PASS):

```
TestFactoryBoundaryCodexModules/form_global_factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/form_bound_factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/form_interface_factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/form_callback_factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/form_dependency_callback -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/form_tuple_global -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/link_factory_dependency_only -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryCodexModules/dependency_import_factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B1-struct-field-factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B1-map-factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B1-generic-call -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B1-closure-wrapper -> old code: already non-PASS
TestFactoryBoundaryAdversarial/B1-interface-pointer-method -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B2-linkname-pull-exported -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B2-import-interface-factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B2-import-method-factory -> old code: exit 0 PASS (bypass)
TestFactoryBoundaryAdversarial/B2-import-wrapped-unexported -> old code: already non-PASS
TestFactoryBoundaryAdversarial/B2-linkname-pull-chi-factory -> old code: exit 0 PASS (bypass)
```

All earlier regressions still pass. The positive cases stay exit 0: a single-function build/register/return/`ListenAndServe`, a direct form factory served directly, and a local `http.Server`.

**Results.** `demo/clean` (exit 2; `require_middleware` PASS; only `rule_not_evaluated`) and `demo/violating` (exit 1; exactly `billing.go:9` and `server.go:18`) are unchanged, as are all fixtures:

```
demo/clean: exit 2 INDETERMINATE | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'PASS')] | violations [] | relevant [('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [('rule_not_evaluated', None)]
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant []
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant [('unsupported_router_form', 'example.com/routerinit'), ('unsupported_router_form', 'example.com/routerinit'), ('unsupported_router_form', 'example.com/routerinit')]
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant []
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant []
repeat runs (demo/violating, testdata/routerrules; reports + shards): byte-identical
schemacheck: exit 0 over 388 documents, 0 failures
```

## Final review: documented limits (owner decision)

The final Codex review of `fae5790`/`072a5df` found two residual classes. The owner decided to **record them as limits, not fix them**:

- **L1:** factory self-reference callbacks are exempt from the factory-value check.
- **L2:** dependency linkname targets naming module methods are not matched, because linker and SSA names differ.

They are listed with their fix hints in `../routers/README.md` ("Known limits"). The three runnable modules (`self_callback`, `self_method_callback`, `dependency_link_method`) are tracked as skipped tests in `cmd/bedrock/audit_test.go` `TestKnownLimits`, each skipped with its limit ID.

As a consequence, `require_middleware` is stated to be **advisory against adversarial code** (dependencies or linkname mutating module-created routers) in the policy contract (section 6.2) and the plan (section 9). It is sound for the supported form without such code.
