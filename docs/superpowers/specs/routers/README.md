# Router support matrix, version 1

**Status:** Normative for POC step 3 (plan `../2026-11-17-bedrock-poc-plan.md`, section 6; review finding B4). This is a specification only. The executable fixtures in `testdata/routerfixtures` check its claims against the real routers.

**Pinned versions:** `github.com/go-chi/chi/v5 v5.5.0` and `net/http` from go1.26.11, using the Go 1.22+ ServeMux pattern grammar. `GODEBUG=httpmuxgo121=1` is not supported. Route facts use the shard schema (`../schema/README.md`): `routes[]` and `route_middleware[]`.

The analyzer recognizes registrations in **module** code only. A recognized call in an unsupported form never disappears, and never counts as satisfying a middleware rule. It yields the listed diagnostic, which affects `require_middleware` rules (policy contract, section 6.2).

## 1. Matrix

### chi v5.5.0 API audit

Every exported function and method of `github.com/go-chi/chi/v5` v5.5.0 (root package) is listed here. Methods are listed on `*Mux`; the same names called through the `chi.Router` interface are classified the same way. `testdata/routerfixtures/surface` contains two tests:

- `TestAuditCoversChiAPI` fails if chi exports a name this table does not list, or if the table lists a name chi does not export.
- `TestEveryUnsupportedAPIHasFixture` fails unless every `unsupported_*` row has a call in `surface/unsupported.go` annotated `// want <diagnostic>`. That file is the step 3 checklist: the analyzer must emit the named diagnostic at every annotated call.

| API | Classification | Notes |
| --- | --- | --- |
| `NewRouter` | supported | Creates a root mux identity (section 2). |
| `NewMux` | supported | Same as `NewRouter`. |
| `(*Mux).Get` | supported | `GET` |
| `(*Mux).Post` | supported | `POST` |
| `(*Mux).Put` | supported | `PUT` |
| `(*Mux).Delete` | supported | `DELETE` |
| `(*Mux).Method` | supported | Constant method, upper-cased by chi, must be one of chi's built-in methods. Otherwise `unsupported_router_form` (chi panics). |
| `(*Mux).Use` | supported | Section 4. A late `Use` is `unsupported_middleware_form`. |
| `(*Mux).With` | supported | Section 4. |
| `(*Mux).Group` | supported | Only with a function literal written in place. Otherwise `unsupported_router_form`. |
| `(*Mux).Route` | supported | Only with a function literal written in place. Otherwise `unsupported_router_form`. |
| `(*Mux).Handle` | unsupported_router_form | Not in v1. It also accepts a `"METHOD /path"` pattern. |
| `(*Mux).HandleFunc` | unsupported_router_form | Not in v1. |
| `(*Mux).MethodFunc` | unsupported_router_form | Not in v1. |
| `(*Mux).Connect` | supported | `CONNECT`. Same semantics as `Method("CONNECT", ...)` (coordinator decision, step 3). |
| `(*Mux).Head` | supported | `HEAD`. Same semantics as `Method("HEAD", ...)` (coordinator decision, step 3). |
| `(*Mux).Options` | supported | `OPTIONS`. Same semantics as `Method("OPTIONS", ...)` (coordinator decision, step 3). |
| `(*Mux).Patch` | supported | `PATCH`. Same semantics as `Method("PATCH", ...)` (coordinator decision, step 3). |
| `(*Mux).Query` | supported | `QUERY`. Same semantics as `Method("QUERY", ...)` (coordinator decision, step 3). |
| `(*Mux).Trace` | supported | `TRACE`. Same semantics as `Method("TRACE", ...)` (coordinator decision, step 3). |
| `(*Mux).NotFound` | unsupported_router_form | Installs a fallback handler that is served for unmatched paths, so the analyzer cannot leave it unchecked. |
| `(*Mux).MethodNotAllowed` | unsupported_router_form | Same reason as `NotFound`. |
| `(*Mux).Mount` | unsupported_router_form | Deferred (plan section 6). |
| `RegisterMethod` | unsupported_router_form | Changes the set of valid methods globally. |
| `Chain` | unsupported_middleware_form | When its result wraps a handler that is registered. |
| `(Middlewares).Handler` | unsupported_middleware_form | Wrapped handler. |
| `(Middlewares).HandlerFunc` | unsupported_middleware_form | Wrapped handler. |
| `(*Mux).ServeHTTP` | not registration | |
| `(*Mux).Routes` | not registration | Read-only. |
| `(*Mux).Middlewares` | not registration | Read-only. |
| `(*Mux).Match` | not registration | Read-only. |
| `(*Mux).Find` | not registration | Read-only. |
| `(*Mux).NotFoundHandler` | not registration | Read-only. |
| `(*Mux).MethodNotAllowedHandler` | not registration | Read-only. |
| `(*ChainHandler).ServeHTTP` | not registration | |
| `(*Context).Clone` | not registration | |
| `(*Context).Reset` | not registration | |
| `(*Context).RoutePattern` | not registration | |
| `(*Context).URLParam` | not registration | |
| `(*RouteParams).Add` | not registration | |
| `NewRouteContext` | not registration | |
| `RouteContext` | not registration | |
| `URLParam` | not registration | |
| `URLParamFromCtx` | not registration | |
| `Walk` | not registration | Read-only. |

`chi/middleware` and other subpackages register no routes. Their middleware constructors (`middleware.Timeout(d)` and so on) are call results, and section 4 classifies them.

### net/http (go1.26.11) registration surface

| API | Classification | Notes |
| --- | --- | --- |
| `NewServeMux` | supported | Creates a mux identity. |
| `(*ServeMux).Handle` | supported | Section 3 covers the direct wrapper form. |
| `(*ServeMux).HandleFunc` | supported | |
| `Handle`, `HandleFunc` | supported | Identity `http.DefaultServeMux`. |
| `DefaultServeMux.Handle/HandleFunc` | supported | Identity `http.DefaultServeMux`. |
| `(*ServeMux).Handler`, `(*ServeMux).ServeHTTP` | not registration | |
| `StripPrefix`, `TimeoutHandler`, `MaxBytesHandler`, `AllowQuerySemicolons` around a registered handler | unsupported_middleware_form | Not of type `func(http.Handler) http.Handler`, so not the direct wrapper form. |

### Other unsupported forms (both routers)

| Form | Diagnostic |
| --- | --- |
| Receiver fails the identity rules (section 2) | `unsupported_router_form` |
| Pattern the real router rejects (it would panic), or one outside the bounded subset (section 5) | `unsupported_router_form` |
| Registration in a loop, or under a condition, inside the registering function | `unsupported_router_form` |
| Non-constant pattern | `unresolved_dynamic_route` (route kept with `path: null`) |
| Handler that is not a direct handler (section 3) | `unresolved_dynamic_route` (route kept with `handler_sym: null`) |

## 2. Router-receiver identity

A registration is analyzed only when its receiver is a **supported router value** in the function that makes the registration. Identity is decided on SSA values, after peeling `ChangeType` and `MakeInterface`. Supported values:

1. The result of exactly one `chi.NewRouter()`/`chi.NewMux()` or `http.NewServeMux()` call in the same function. A Go local variable assigned once from such a call is this SSA value. Such a value is a **mux**.
2. The first parameter of a function literal passed directly as `fn` to `Route` on a supported chi value. This is a mux (a sub-router).
3. The first parameter of a function literal passed directly as `fn` to `Group`, or the result of `With(...)` on a supported chi value, chained or held in a local variable assigned once. Such a value is **inline**.
4. A load of the package variable `http.DefaultServeMux`, or the package functions `http.Handle`/`http.HandleFunc`, which use it implicitly.

Anything else is `unsupported_router_form`:

- a function parameter, other than cases 2 and 3;
- a struct field, global (other than `DefaultServeMux`), map, slice or channel element;
- the result of any other call;
- a value merged at an SSA phi.

**Router escape.** A supported value created in a module package may be used as a registration receiver, returned, passed as the `handler` of `http.ListenAndServe`/`ListenAndServeTLS`/`Serve`, or assigned to the `Handler` field of an `http.Server` literal. Passing it to any other function gives `unsupported_router_form` in the creating package. This is the "router registered in a helper package" case of plan section 6.

**Which rules a diagnostic affects** depends on how much Bedrock knows about the route.

| Situation | Diagnostic | Affects |
| --- | --- | --- |
| **Unknown registration identity:** the receiver fails the identity rules, a supported router escapes into a helper, or the registration uses an API outside the matrix (section 1). Bedrock cannot tell which router, route or scope it belongs to. | `unsupported_router_form` | **Every** `require_middleware` rule, module-wide (reason `route_registration`). |
| **Known registration site, no route fact:** the receiver is supported but the registration is rejected, because the pattern is invalid or bounded out, or it conflicts on the same mux (section 5). | `unsupported_router_form` | Each rule whose `scope` contains the registering package (reason `route_registration`). The path is unknown, so exceptions cannot apply. |
| **Known route, uncertain chain:** a middleware argument is not a direct reference, a late `Use`, a conditional `Use`/`With`, or a wrapper other than the direct form. | `unsupported_middleware_form` on each affected route | Each rule **for which that route is governed**: the registering package is in the rule's `scope`, and the route matches none of that rule's `except_routes` (reason `middleware_chain`). Otherwise the diagnostic is a warning for that rule. |
| **Known route, unresolved path or handler.** | `unresolved_dynamic_route` | Each rule whose `scope` contains the registering package. An unresolved path cannot be checked against exceptions. With a resolved path but unresolved handler, the rule's exceptions apply (reason `route_registration`). |

Example, with two disjoint route scopes and a per-rule exception:

```toml
[boundary.api_routes]
packages = ["example.com/app/internal/api"]
[boundary.admin_routes]
packages = ["example.com/app/internal/admin"]

[[rule]]
id = "api-auth"
kind = "require_middleware"
scope = "api_routes"
middleware = "auth.middleware"
except_routes = ["GET /healthz"]
[[rule]]
id = "admin-auth"
kind = "require_middleware"
scope = "admin_routes"
middleware = "auth.middleware"
```

| Code | Diagnostic | `api-auth` | `admin-auth` |
| --- | --- | --- | --- |
| `internal/admin`: `r.Use(middleware.Timeout(5*time.Second)); r.Get("/users", h.Users)` | `unsupported_middleware_form` on `GET /users` | not affected (route not in scope) | **indeterminate** |
| `internal/api`: `r.With(mk()).Get("/healthz", h.Health)` | `unsupported_middleware_form` on `GET /healthz` | warning (excepted by `GET /healthz`) | not affected |
| `internal/api`: `r.With(mk()).Get("/items", h.Items)` | `unsupported_middleware_form` on `GET /items` | **indeterminate** | not affected |
| `internal/api`: `mux.Handle("GET /x/{b}", h.B)` after `mux.Handle("GET /x/{a}", h.A)` | `unsupported_router_form` (same-mux conflict, no fact) | **indeterminate** | not affected |
| `internal/util`: `func reg(r chi.Router) { r.Get("/z", h.Z) }` | `unsupported_router_form` (receiver is a parameter) | **indeterminate** | **indeterminate** |

## 3. Handlers and the direct wrapper form

A **direct handler** is one of the following:

- a reference to a top-level function: `pkg.F`, or `http.HandlerFunc(pkg.F)`;
- a method value: `h.List`, or `http.HandlerFunc(h.List)`. Its `$bound` wrapper is `handler_value_sym`, and the method is `handler_sym`;
- a function literal written in place, which becomes `handler_sym`.

`handler_sym` is the policy `Entry` for the route (plan decision 4).

**Direct wrapper form (net/http only).** `X.Handle(pattern, W(h))`, where:

- `X` is a supported ServeMux value;
- `h` is a direct handler;
- `W` is a direct reference to a function or method value of type exactly `func(http.Handler) http.Handler`.

It is recognized **structurally, independently of any policy rule**. The route gets one `route_middleware` entry, `{middleware_sym: W, order: 0, source: direct_wrapper}`. Each `require_middleware` rule then checks whether `W` is in its own middleware concept.

These variants are `unsupported_middleware_form`:

- nested wrappers, `W1(W2(h))`;
- a wrapper of another type (`http.StripPrefix(p, h)`);
- a wrapper that is itself a call result (`mk()(h)`);
- any wrapped handler on chi.

Two rules over one route (policy excerpt and code):

```toml
[concept."auth.middleware"]
symbols = ["example.com/demo/internal/auth.RequireAuth"]
[concept."audit.middleware"]
symbols = ["example.com/demo/internal/audit.Log"]

[[rule]]
id = "routes-require-auth"
kind = "require_middleware"
scope = "routes"
middleware = "auth.middleware"
# message, remediation omitted
[[rule]]
id = "routes-require-audit"
kind = "require_middleware"
scope = "routes"
middleware = "audit.middleware"
# message, remediation omitted
```

```go
mux.Handle("GET /internal/export", auth.RequireAuth(http.HandlerFunc(h.Export)))
```

The facts are one route plus `route_middleware [{RequireAuth, 0, direct_wrapper}]`. The outcomes:

| Rule | Result | Why |
| --- | --- | --- |
| `routes-require-auth` | satisfied | `RequireAuth` is in its concept. |
| `routes-require-audit` | one **violation** | The chain is fully resolved and contains no `audit.Log`. Not a diagnostic. |

## 4. Effective middleware (chi)

These rules follow chi v5.5.0's implementation and are checked at run time by `testdata/routerfixtures/chains`.

**Router values and lists.** Each supported router value `X` has:

- a kind: mux or inline;
- a base mux: itself if it is a mux, otherwise the base of the value it was derived from;
- an **own list** `L(X)` of middleware, which grows in source (call) order.

**Operations, in call order:**

- **`X.Use(m...)`** appends `m...` to `L(X)`, unless the `Use` is **late**. A late `Use` is diagnosed `unsupported_middleware_form` and **credited to no route**, because chi panics at run time ("all middlewares must be defined before routes on a mux"). A `Use` is late when:
  - `X` is a mux and has already had a route registered, or a `With`, `Group`, `Route` or `Mount` call made on it;
  - `X` is inline and has already had a route registered on it, including through `Route` or `Mount`.
- **`I := X.With(m...)`** creates an inline value with `L(I) = (X inline ? a snapshot copy of L(X) at this call : []) ++ m...`, and the same base mux as `X`. Later `Use` calls on `X` do not reach `I`.
- **`X.Group(fn)`** is `I := X.With()` followed by `fn(I)`.
- **`X.Route(p, fn)`** creates a fresh mux `S` whose **inherited chain** is `Chain(X)` evaluated at the `Route` call, and whose path prefix is `p` joined to `X`'s prefix. Then `fn(S)` runs.

**Chain at registration.** The chain of a route registered on `X` is evaluated at the registration call, outermost first:

```
Chain(X) = MuxChain(base(X)) ++ (X inline ? L(X) : [])
MuxChain(M) = inherited(M) ++ L(M)    // inherited(root) = []; L(M) is final for valid code, since later Use is late
```

`route_middleware[].order` is the position in this list, and `source` is `use` or `with` according to the call that contributed the entry.

**A chain is unresolved** when a middleware argument anywhere in it is not a direct reference to a function or method value. Examples: a call result such as `middleware.Timeout(5*time.Second)`, a variable, a function literal, or a `Use`/`With` inside an `if` or a loop. That case gives `unsupported_middleware_form` on every route whose chain includes it.

**Accepted fixtures** (`chains.Accepted`; A, B, C, D are direct-reference middlewares):

| Fixture | Code | Expected chains |
| --- | --- | --- |
| root-use | `r.Use(A); r.Get("/x")` | `GET /x`: [A] |
| use-then-with | `r.Use(A); r.With(B).Get("/x"); r.Get("/y")` | `/x`: [A B]; `/y`: [A] |
| with-with | `r.With(A).With(B).Get("/x")` | [A B] |
| with-snapshot | `w := r.With(A); w2 := w.With(B); w.Use(C); w.Get("/w"); w2.Get("/w2")` | `/w`: [A C]; `/w2`: [A B] |
| with-group | `r.With(A).Group(func(g){ g.Use(B); g.Get("/x") })` | [A B] |
| with-route | `r.With(A).Route("/p", func(s){ s.Use(B); s.Get("/x") })` | `/p/x`: [A B] |
| route-with | `r.Use(A); r.Route("/p", func(s){ s.Use(B); s.With(C).Get("/x"); s.Get("/y") })` | `/p/x`: [A B C]; `/p/y`: [A B] |
| nested-groups | `r.Group(func(g){ g.Use(A); g.Group(func(h){ h.Use(B); h.Get("/x") }); g.Get("/y") }); r.Get("/z")` | `/x`: [A B]; `/y`: [A]; `/z`: [] |
| group-with-route-use | `r.Use(A); r.Group(func(g){ g.Use(B); g.With(C).Route("/p", func(s){ s.Use(D); s.Get("/x") }) })` | `/p/x`: [A B C D] |

**Rejected fixtures** (`chains.Rejected`). Each is a late `Use`; chi panics on all of them, the analyzer emits `unsupported_middleware_form`, and `A` is credited nowhere:

| Fixture | Code |
| --- | --- |
| use-after-route | `r.Get("/x"); r.Use(A)` |
| use-after-with | `r.With(B).Get("/x"); r.Use(A)` |
| use-after-group | `r.Group(...); r.Use(A)` |
| use-after-route-mount | `r.Route("/p", ...); r.Use(A)` |
| inline-use-after-route | `g.Get("/x"); g.Use(A)` inside a `Group` |

net/http has no chains. A route's middleware is the direct wrapper (section 3) or nothing.

## 5. Normalization

To validate a constant pattern, the analyzer registers it on a scratch router of the pinned version (`chi.NewRouter()` or `http.NewServeMux()`) under `recover`. If the real router panics, the result is `unsupported_router_form` and no route fact is emitted, so Bedrock's grammar can never drift from the router's. A **bounded subset** then narrows what the analyzer accepts beyond what the router accepts; each bounded-out pattern also gives `unsupported_router_form`.

Fixtures: `testdata/routerfixtures/paths`. Each case records the registration and the normalized result or diagnostic. Probe requests pin the router behavior that each rule relies on.

### net/http

- **Splitting.** The pattern splits as Go does: `METHOD`, then spaces or tabs, then the rest. `"GET \t /x"` gives `GET` and `/x`.
- **Method.** The method is kept **exactly as written**; it is case-sensitive, as in Go. `"get /x"` is method `get`, which Go serves only to requests with method `get`, while a `GET` request gets 405.
  - Bounded subset: the method must match `^[A-Za-z]+$`. Go also accepts other tokens such as `M-SEARCH` or `*`, which are bounded out.
  - No method means any method: `method: null`, written `*` in route IDs.
  - A `GET` pattern also serves `HEAD`, but it is still recorded as `GET`.
- **Host.** Bounded subset: no host. `"example.com/x"` is bounded out.
- **Path.** The path is kept as written: `{name}`, `{name...}`, a trailing `/` (subtree) and `{$}` (exact) all stay. `/files/` and `/files/{$}` are different routes.
  - Bounded subset: no empty, `.` or `..` segments (`//`, `/./`, `/../`). Go accepts them but redirects cleaned requests away from them.
- **Duplicates and conflicts on one mux.** The analyzer keeps **registration state per mux identity**. It replays each constant pattern, in source order, onto one scratch `http.ServeMux` per identity, under `recover`. A registration that panics there gives `unsupported_router_form` and **no route fact**. That covers an invalid pattern, an identical pattern, and a pattern that conflicts with an earlier one, such as `GET /x/{a}` then `GET /x/{b}`. Patterns Go accepts together, such as `GET /x` and `/x`, are separate facts.

  **Earlier facts survive.** Valid registrations before the rejected one keep their route facts and are evaluated normally. At run time, Go panics at the conflicting registration, while the earlier route is already in place and serving (fixture `nethttp-same-mux-identical`). Keeping the earlier facts never hides a missing middleware. The rejected registration still produces a diagnostic under the "known registration site" row in section 2.

### chi

- **Method.**
  - `Get`, `Post`, `Put` and `Delete` give `GET`, `POST`, `PUT` and `DELETE`.
  - `Method(m, ...)` gives `strings.ToUpper(m)`, as chi does. A method chi does not know is `unsupported_router_form`, because chi panics.
- **Path.** The full path is the `Route` prefixes and the pattern joined **without hiding trailing slashes**:
  - `join(prefix, pattern)` drops one trailing `/` from the prefix, then concatenates.
  - `Group` adds no prefix.
  - So `/api` and `/api/` stay distinct, as chi keeps them.

  | Registration | Normalized |
  | --- | --- |
  | `r.Get("/api", h)` | `GET /api` (serves `/api` only) |
  | `r.Get("/api/", h)` | `GET /api/` (serves `/api/` only) |
  | `r.Route("/api", func(s){ s.Get("/", h) })` | `GET /api/` |
  | `r.Route("/api/", func(s){ s.Get("/x", h) })` | `GET /api/x` |
  | `r.Route("/api", func(s){ s.Get("/x/", h) })` | `GET /api/x/` |
  | `r.Get("/items/{id:[0-9]+}", h)` | `GET /items/{id}` (regexp kept in `pattern_raw`) |

  In the `Route(...)` plus `Get("/")` case, chi's mount stub also serves the bare prefix `/api`. The fact still records `/api/`. So `except_routes = ["GET /api"]` does not except it (the route stays governed), and `GET /api/` does.
- **Bounded subset.** No empty, `.` or `..` segments (chi accepts `//x`).

`except_routes` patterns compare the method case-sensitively against these normalized methods (policy contract, section 6.2).

### Duplicate normalized keys across routers

Two **valid** registrations with the same normalized method and path in one unit are both kept as route facts. They get `duplicate_index` 0, 1, ... in (file, line, column) order, and the later ones get a `duplicate_route` warning. This happens in three cases:

- on **distinct** ServeMux values, which Go accepts;
- on chi values, including the same chi router, which does not panic. At run time the later registration wins, so keeping both is conservative: both are checked;
- on a chi router and a ServeMux.

Each copy must satisfy every rule that governs it. A same-mux net/http duplicate never reaches this step, because it has no fact.

Fixtures: `testdata/routerfixtures/paths/dups.go` (`DupCases`). `dups_test.go` checks each case against the real router: whether it panics, and which handler serves. The cases:

| Fixture | Facts |
| --- | --- |
| `nethttp-same-mux-identical` | `GET /a#0`, then `unsupported_router_form` (the earlier route still serves) |
| `nethttp-same-mux-conflicting-wildcards` | `GET /x/{a}#0`, then `unsupported_router_form` |
| `nethttp-same-mux-method-and-any` | `GET /x#0` and `* /x#0`, both kept, no conflict |
| `nethttp-separate-muxes-identical` | `GET /a#0`, `GET /a#1` plus a `duplicate_route` warning |
| `chi-same-mux-identical` | `GET /a#0`, `GET /a#1` plus a `duplicate_route` warning (the later one serves) |

## 6. Accepted and rejected code (summary)

Accepted: the demo's forms (`demo/clean/internal/server/server.go`), the fixtures in `chains.Accepted`, and the accepted cases in `paths.Cases`.

```go
r := chi.NewRouter()
r.Get("/healthz", h.Healthz)
r.Route("/api/v1", func(r chi.Router) {
	r.Use(auth.RequireAuth)
	r.Get("/items", h.ListItems)
})
mux := http.NewServeMux()
mux.Handle("GET /internal/export", auth.RequireAuth(http.HandlerFunc(h.Export)))
```

Rejected: every annotated call in `surface/unsupported.go`, `chains.Rejected`, the `diag:` cases in `paths.Cases`, and these:

```go
func register(r chi.Router) { r.Get("/x", h.X) }      // receiver is a parameter: unsupported_router_form
register(r)                                           // router escapes into a helper: unsupported_router_form
for _, p := range paths { r.Get(p, h.X) }             // loop: unsupported_router_form
r.Get(prefix+"/x", h.X)                               // non-constant path: unresolved_dynamic_route
r.Get("/x", makeHandler())                            // handler is a call result: unresolved_dynamic_route
r.Use(middleware.Timeout(5 * time.Second))            // middleware is a call result: unsupported_middleware_form
if cfg.Auth { r.Use(auth.RequireAuth) }               // conditional middleware: unsupported_middleware_form
var r chi.Router
if prod { r = chi.NewRouter() } else { r = devRouter() }
r.Get("/x", h.X)                                      // receiver merged at a phi: unsupported_router_form
```

## 7. Analyzer conformance (step 3)

`internal/analyze/routes.go` implements this matrix on SSA. It builds SSA only for well-typed packages whose imports are all well typed, and it extracts routes from module packages. Routes and `route_middleware` facts go into the shards, and diagnostics carry the reach set out in section 2. The fixtures pin the behavior:

| Fixture | Checked by | What it shows |
| --- | --- | --- |
| `chains.Accepted` | `internal/analyze` `TestAcceptedChainsStatic` | Every accepted case yields exactly the section 4 chain, the same chains real chi produces (`chains_test.go`). |
| `chains.Rejected` | `TestRejectedLateUseStatic` | Every late `Use` yields `unsupported_middleware_form`. The middleware is credited to no route, and every route of the router tree is uncertain. |
| `surface/unsupported.go`, `conditional/conditional.go` | `TestAnnotatedDiagnostics` | Every `// want <diagnostic>` line gets exactly that diagnostic, and no other line gets one. The annotated lines cover unsupported chi APIs, wrapped handlers, conditional and looped `Use` and registration, phi receivers, helper receivers and escapes, computed middleware, non-constant paths, call-result handlers, named `Group` functions, and same-mux conflicts. Routes with uncertain chains never count as authenticated. |
| `conditional.SeparateMuxes` | `TestSeparateMuxDuplicatesKept` | Identical patterns on two muxes are both kept, plus a `duplicate_route` warning. |
| `testdata/routerrules` | `cmd/bedrock` `TestRouterRulesTwoRules` | One direct-wrapper attachment satisfies one rule and violates another. An `except_routes` entry that matches nothing is a warning, and the real unprotected route is still a violation. |
| synthetic routes | `internal/check` `TestRouteDiagProjection`, `TestMatchRoute` | Known-route chain uncertainty is projected per rule scope and exceptions, while unknown identity reaches every rule. Exception patterns follow the policy contract. |

**Analyzer-side limits (conservative):**

- A registration must run on every normal path of its function. Its block must dominate every return and must not sit in a cycle. Otherwise the registration gets `unsupported_router_form`, the same treatment as "under a condition or in a loop". That includes a registration after an early `return`.
- A middleware concept may bind only functions of type `func(http.Handler) http.Handler`, and no packs. Anything else is `policy_invalid` (policy contract, section 5). This is checked after loading, because it needs types.

**Fail-closed rules added after the step 3 review.** Each has a probe in `cmd/bedrock/probes_test.go`:

- **Aliases.** Type aliases are resolved before matching, so `type R = chi.Router` is a chi router and `type H = http.Handler` is a valid middleware type.
- **Foreign interfaces.** A tracked router may flow into an interface only for a recognized chi operation, the read-only `ServeHTTP`, a return, `ListenAndServe` and its variants, or an `http.Server.Handler` field. Anything else, such as `var reg interface{ Handle(string, http.Handler) } = mux`, is an escape: `unsupported_router_form` with module-wide reach.
- **Registration-shaped calls.** A call through a function value or a non-chi interface whose signature is (string pattern, handler) cannot be resolved to a router. It is `unsupported_router_form`, module-wide.
- **Registration APIs used as values.** `http.Handle`/`HandleFunc`, the `ServeMux` methods, and chi's registration methods (including method expressions and thunks) are allowed only as the callee of a direct call. Used as a value (`var register = http.HandleFunc`, passed as an argument, stored), they give `unsupported_router_form`, module-wide. A local alias called in the same function (`f := http.HandleFunc; f(...)`) compiles to a direct call and is recognized precisely.
- **Middleware lists** are credited only from the compiler's in-place varargs array. Each element must be stored exactly once, in the call's block before the call, and the array and slice may have no other use. A user-created slice (`ms := []func(...){Auth}; r.Use(ms...)`), an overwritten element (conditional or not), or a spread or computed slice gives `unsupported_middleware_form`, so the chain is uncertain and nothing is credited. This applies to `Use` and `With`.
- **Owning symbols.** Every function named by `registered_in` or by a diagnostic is an emitted symbol of its unit. That includes the synthesized `pkg.init` and declared `pkg.init#n`, since go/types does not declare them (fixture `testdata/routerinit`).

Run all fixtures:

```sh
cd testdata/routerfixtures && go vet ./... && go test ./...
```
