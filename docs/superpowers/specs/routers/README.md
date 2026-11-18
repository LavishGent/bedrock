# Router support matrix, version 1

**Status:** Normative for POC step 3 (plan `../2026-11-17-bedrock-poc-plan.md`, section 6; review finding B4). This is a specification only; there is no extraction code yet.

**Pinned versions:** `github.com/go-chi/chi/v5 v5.5.0` and `net/http` from go1.26.11, with ServeMux pattern semantics as of Go 1.22 or later. `GODEBUG=httpmuxgo121=1` is not supported. Route facts use the shard schema (`../schema/README.md`): `routes[]` and `route_middleware[]`.

The analyzer recognizes route registrations in **module** code only. "Recognized" means the call matches a row below. A recognized call in an unsupported form never disappears, and never counts as authenticated. It yields a diagnostic that affects `require_middleware` (policy contract, section 6.2).

## 1. Matrix

| Router | Construct | v1 | Facts or diagnostic |
| --- | --- | --- | --- |
| chi | `chi.NewRouter()`, `chi.NewMux()` | supported | Creates a router identity (section 2). |
| chi | `Get`, `Post`, `Put`, `Delete` (pattern, handler) | supported | One route with the fixed method. |
| chi | `Method(m, pattern, handler)` | supported if `m` is a constant | Method upper-cased. A non-constant `m` gives `unresolved_dynamic_route`. |
| chi | `Use(mw...)` | supported | Adds middleware to the router's chain (section 4). |
| chi | `With(mw...)` | supported | Returns a derived router with extra inline middleware. |
| chi | `Group(fn)`, `Route(pattern, fn)` with `fn` a function literal written in place | supported (static nesting) | A sub-router whose identity is `fn`'s first parameter. |
| chi | `Group`/`Route` with `fn` not a literal (named func, variable) | unsupported | `unsupported_router_form` |
| chi | `Patch`, `Head`, `Options`, `Connect`, `Trace`, `Handle`, `HandleFunc`, `MethodFunc` | unsupported in v1 (not in the plan's list) | `unsupported_router_form` |
| chi | `Mount`, `NotFound`, `MethodNotAllowed` | deferred | `unsupported_router_form` |
| net/http | `mux.Handle(p, h)`, `mux.HandleFunc(p, f)` on a local `http.NewServeMux()` | supported | One route. |
| net/http | `http.Handle`, `http.HandleFunc`, `http.DefaultServeMux.Handle/HandleFunc` | supported | One route on the identity `http.DefaultServeMux`. |
| net/http | `mux.Handle(p, auth.RequireAuth(h))` with `RequireAuth` in the rule's middleware concept and `h` a direct handler | supported (the one wrapper form) | Route plus one `route_middleware` (`source: direct_wrapper`). |
| net/http | any other wrapper call around the handler | unsupported | `unsupported_middleware_form` |
| net/http | host-qualified pattern (`"example.com/x"`) | unsupported in v1 | `unsupported_router_form` |
| both | registration on a receiver that fails the identity rules | unsupported | `unsupported_router_form` |
| both | non-constant pattern, or a handler that is not a direct handler | unresolved | `unresolved_dynamic_route` (route kept with `path: null` or `handler_sym: null`) |
| both | registration in a loop, or under a condition, inside the registering function | unsupported | `unsupported_router_form` |

## 2. Router-receiver identity

A registration is analyzed only when its receiver is a **supported router value** in the function that makes the registration. Identity is decided on SSA values, after peeling `ChangeType` and `MakeInterface`. Supported values:

1. The result of exactly one `chi.NewRouter()`/`chi.NewMux()` or `http.NewServeMux()` call in the same function. A Go local variable assigned once from such a call is this SSA value.
2. The first parameter of a function literal passed directly as `fn` to `Group`/`Route` on a supported chi value. Nesting recurses.
3. The result of `With(...)` called on a supported chi value, whether chained (`r.With(mw).Get(...)`) or held in a local variable assigned once.
4. A load of the package variable `http.DefaultServeMux`, or the package functions `http.Handle`/`http.HandleFunc`, which use it implicitly.

Anything else is `unsupported_router_form`:

- a function parameter, other than case 2;
- a struct field, global (other than `DefaultServeMux`), map, slice or channel element;
- the result of any other call, including your own `newRouter()` helper;
- a value merged at an SSA phi, for example a variable reassigned on different branches.

**Router escape.** A supported value created in a governed package may be used as a registration receiver, returned, passed as the `handler` of `http.ListenAndServe`/`ListenAndServeTLS`/`Serve`, or assigned to the `Handler` field of an `http.Server` literal. Passing it to any other function gives `unsupported_router_form` in the creating package. Such a function could register routes on it, which is exactly the "router registered in a helper package" case of plan section 6.

**Which rules the diagnostic affects.** Bedrock cannot tell which router an unsupported registration belongs to. So an `unsupported_router_form` or `unresolved_dynamic_route` diagnostic in **any** module package affects every `require_middleware` rule (reason `route_registration`), not only rules whose scope contains that package.

## 3. Handlers

A **direct handler** is one of the following:

- a reference to a top-level function: `pkg.F`, or `http.HandlerFunc(pkg.F)`;
- a method value on any receiver: `h.List`, or `http.HandlerFunc(h.List)`. Its `$bound` wrapper is `handler_value_sym`, and the method is `handler_sym`;
- a function literal written in place, which becomes `handler_sym` (`Register$1`).

`handler_sym` is the policy `Entry` for the route (plan decision 4).

Any other handler expression (a call result, a variable holding a function or an `http.Handler`, an interface value) is `unresolved_dynamic_route` with `handler_sym: null`. The one exception is the wrapper form in section 1.

## 4. Effective middleware (chi)

Each route records its chain, outermost first:

1. the `Use` middleware of the root router, in call order;
2. then, for each enclosing `Route`/`Group` from outer to inner, that sub-router's `Use` middleware;
3. then the `With` middleware of the registration's receiver, if it is a `With` value, in call order;
4. for net/http, the one `direct_wrapper` middleware.

`route_middleware[].source` records which step contributed each entry (`use`, `group`, `route`, `with`, `direct_wrapper`). chi requires `Use` before any route on the same router, and panics otherwise. Bedrock does not model that panic: a `Use` after a route still counts for the routes registered after it in source order, and not for earlier ones.

A middleware argument must be a direct reference to a function, `pkg.Mw`, or a method value. Anything else gives `unsupported_middleware_form` on every route of that router and its sub-routers:

- a call result, such as `middleware.Timeout(5*time.Second)` or `cors.Handler(opts)`;
- a variable;
- a function literal;
- a conditional `Use` (inside `if`).

Under the policy contract, any unresolved middleware on a governed, non-excepted route makes `require_middleware` indeterminate.

## 5. Normalization

### Method

- chi `Get`/`Post`/`Put`/`Delete` give `GET`/`POST`/`PUT`/`DELETE`.
- chi `Method(m, ...)` gives `strings.ToUpper(m)`, which must be a token (`^[A-Z]+$`). Otherwise `unresolved_dynamic_route`.
- net/http: the method token at the start of the pattern, as written. Go requires it to be upper case. A pattern without a method gives `method: null` (any method), written `*` in route IDs.
- `GET` in net/http also serves `HEAD`, but the route is recorded as `GET`.

### Path (chi)

- The pattern must be a constant string starting with `/`.
- **Full path**: the concatenation of each enclosing `Route` prefix and the pattern.
  - A prefix's trailing `/` is dropped before joining.
  - A pattern of exactly `/` inside a `Route` normalizes to the prefix itself, because chi serves `Route("/api", ...)` + `Get("/")` at `/api`.
  - `Group` adds no prefix.
- **Parameters**: `{name}` stays `{name}`, and `{name:regexp}` becomes `{name}`. The regexp is kept in `pattern_raw`. A trailing `*` stays `*`.
- No other rewriting: no case folding, no `//` collapsing, no `.` or `..` cleaning. A pattern containing `//`, `/./` or `/../` gives `unsupported_router_form`.

### Path (net/http)

- The pattern is `[METHOD ][HOST]/[PATH]`.
- A non-empty `HOST` gives `unsupported_router_form` in v1.
- The path is kept as written:
  - wildcards `{name}` and `{name...}` stay;
  - a trailing `/` (subtree match) stays;
  - `{$}` (exact match) stays.
- So `/files/` and `/files/{$}` are different normalized paths.

**Examples:**

| Registration | Normalized method and path |
| --- | --- |
| `r.Route("/api/v1", func(r chi.Router){ r.Get("/items/{id:[0-9]+}", h.Get) })` | `GET /api/v1/items/{id}` |
| `r.Route("/api/", func(r chi.Router){ r.Get("/", h.Root) })` | `GET /api` |
| `r.Method("patch", "/x", h.X)` | `PATCH /x` |
| `mux.Handle("GET /internal/export", ...)` | `GET /internal/export` |
| `mux.HandleFunc("/static/", h.Static)` | `* /static/` |

## 6. Accepted and rejected code

Accepted. These are the demo's forms (`demo/clean/internal/server/server.go`) plus `With` and `DefaultServeMux`:

```go
r := chi.NewRouter()
r.Get("/healthz", h.Healthz)                 // GET /healthz, no middleware
r.Route("/api/v1", func(r chi.Router) {       // sub-router = closure param
	r.Use(auth.RequireAuth)                  // chain: [RequireAuth (route)]
	r.Get("/items", h.ListItems)             // GET /api/v1/items
	r.With(audit.Log).Post("/items", h.Make) // chain: [RequireAuth (route), audit.Log (with)]
})

mux := http.NewServeMux()
mux.Handle("GET /internal/export", auth.RequireAuth(http.HandlerFunc(h.Export))) // direct_wrapper
http.HandleFunc("GET /ping", h.Ping)          // DefaultServeMux, no middleware
```

Rejected, with the diagnostic each one gets:

```go
func register(r chi.Router) { r.Get("/x", h.X) }      // receiver is a parameter: unsupported_router_form
register(r)                                           // router escapes into a helper: unsupported_router_form

r.Mount("/admin", adminRouter())                      // deferred: unsupported_router_form
r.Patch("/items/{id}", h.Patch)                       // not in v1 list: unsupported_router_form

for _, p := range paths { r.Get(p, h.X) }             // loop: unsupported_router_form
r.Get(prefix+"/x", h.X)                               // non-constant path: unresolved_dynamic_route
r.Get("/x", makeHandler())                            // handler is a call result: unresolved_dynamic_route

r.Use(middleware.Timeout(5 * time.Second))            // middleware is a call result: unsupported_middleware_form
if cfg.Auth { r.Use(auth.RequireAuth) }               // conditional middleware: unsupported_middleware_form

mux.Handle("/x", logging(auth.RequireAuth(h.X)))      // wrapper other than the one direct form: unsupported_middleware_form
mux.Handle("api.example.com/x", h.X)                  // host pattern: unsupported_router_form

var r chi.Router
if prod { r = chi.NewRouter() } else { r = devRouter() }
r.Get("/x", h.X)                                      // receiver merged at a phi: unsupported_router_form
```

If the demo's net/http route did not use the supported wrapper form, it would have to be a documented policy exception (plan section 6). It does use the form.
