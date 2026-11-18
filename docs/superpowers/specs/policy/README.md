# Bedrock POC policy contract, version 1

**Status:** Normative for POC step 1 and later (plan `../2026-11-17-bedrock-poc-plan.md`, section 5; review finding S1). The parser is built in step 2. This document plus `.bedrock/policy.toml` is the contract it must implement.

**Related documents:**

- Fact and report schema: `../schema/README.md`.
- Router support matrix (route extraction, normalization, middleware chains): `../routers/README.md`.
- Verdict table: plan decision 7.
- `Entry` and `reachable_from`: plan decision 4.

None of those are changed here.

## 1. File and loading

- **Location**: the policy is the single file `.bedrock/policy.toml` at the repository root. It is TOML 1.0, UTF-8. In CI it is read from the base branch only (plan section 9).
- **Fail closed**: any problem in section 8 stops evaluation or marks rules indeterminate. Bedrock never falls back to defaults or a partial policy.
- **Strict keys**: every table is closed. A key not listed in this document, at any depth, is an error.

## 2. Top level

| Key | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `version` | integer | yes | none | Must be `1`. |
| `module` | string | yes | none | Path of the one analyzed module. It must equal the module path of the analyzed directory's `go.mod`. |
| `boundary` | table of boundary tables | no | `{}` | Named package sets (section 4). |
| `concept` | table of concept tables | no | `{}` | Named symbol sets (section 5). |
| `rule` | array of rule tables (`[[rule]]`) | yes, at least 1 | none | The rules (section 6). |

**Names:**

- A boundary name matches `^[a-z][a-z0-9_]*$`.
- A concept name matches `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`. Concept names containing dots are written as quoted keys, as in `[concept."database.query"]`.
- Rules refer to boundaries and concepts by bare name, for example `scope = "app"` or `concept = "database.query"`.

## 3. Import-path globs

Boundary `packages` entries are import-path patterns. A pattern is split on `/` into segments, and each segment is one of three kinds:

| Segment | Matches |
| --- | --- |
| a literal, e.g. `internal` | exactly that segment |
| `*` | exactly one segment, any non-empty text |
| `**` | zero or more whole segments |

Matching rules:

- Matching is on the whole import path, bytewise and case-sensitive. There is no prefix matching: `example.com/demo/internal/repo` matches only that package, not its subpackages.
- **`**` matches the package root itself.** `example.com/demo/internal/repo/**` matches `example.com/demo/internal/repo`, `.../repo/sql` and `.../repo/sql/x`.
- To match only descendants, write `example.com/demo/internal/repo/*/**`.
- `**` may appear in the middle: `example.com/**/private` matches `example.com/private`, `example.com/a/private` and `example.com/a/b/private`.
- **Invalid patterns** (each is `policy_invalid`):
  - a wildcard that is only part of a segment (`repo*`, `*_test`, `a**`);
  - any other metacharacter (`?`, `[`, `]`, `{`, `}`, `\`);
  - an empty segment, or a leading or trailing `/`;
  - a `.` or `..` segment;
  - whitespace;
  - an empty pattern.
- A boundary's set is the union of its patterns' matches over **all units in the analysis manifest**: module, dependency and standard library.

How the set is used depends on the role (section 6):

| Role | Units used |
| --- | --- |
| `scope` (every rule kind) | Only the **module** units in the set. Dependency and standard-library packages are never governed. |
| `forbid_import.target`, `forbid_import.except_from` | All units in the set. A rule may forbid importing a third-party package. |
| `restrict_concept_to_boundary.boundary` | All units in the set. `In(B)` is every universe symbol owned by those units, including their closures, wrappers and instances (owner rules in `../schema/README.md`). |

## 4. `[boundary.<name>]`

| Key | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `packages` | array of strings, non-empty | yes | none | Import-path patterns (section 3). |

Route sets are not boundaries in version 1. Routes are scoped by projecting a package scope (section 6.2) and narrowed with `except_routes`.

## 5. `[concept.<name>]`

| Key | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `packs` | array of strings | no | `[]` | Built-in packs whose bindings for this concept are included. Version 1 knows one pack, `go-stdlib-sql`, which binds only `database.query` (section 5.1). Naming an unknown pack, or a pack that has no binding for this concept, is `policy_invalid`. |
| `symbols` | array of strings | no | `[]` | Canonical function or method symbol IDs (`../schema/README.md`, Symbol IDs), matched exactly. No globs. Every entry must match the grammar below, whether or not a rule uses the concept; anything else is `policy_invalid`. |

- **Symbol ID grammar.** An entry has one of three forms:

  ```
  importpath.Func  |  (importpath.Type).Method  |  (*importpath.Type).Method
  ```

  `importpath` is one or more `/`-separated elements made of letters, digits, `_`, `.`, `~`, `+` and `-`. `Func`, `Type` and `Method` are Go identifiers. Closures (`$1`), wrappers (`$bound`, `$thunk`), generic instances (`[...]`), surrounding whitespace and other text are rejected. So are malformed strings such as `???` or a bare `RequireAuth`.
- At least one of `packs` and `symbols` must be non-empty.
- **Concept set**: the union of the pack bindings and `symbols`, restricted to function symbols present in the analysis universe.
- **Wrapper closure**: a call whose target is a wrapper or instance of a bound symbol counts as a call to that symbol. This covers `$bound`, `$thunk`, pointer and promotion wrappers, and generic instances whose `origin_id` is bound. Without this, `f := db.QueryContext; f(...)`, or a struct embedding `*sql.DB`, would slip through. Only the forwarding call of a synthetic wrapper (`kind: wrapper`) to its bound origin is not reported a second time; the call into the wrapper is the reported site. Every call in the body of an ordinary bound function, method or closure, and in a real generic instance, is checked like any other: binding a function never exempts its own calls (step 4 review B1).
- **Middleware concepts**: a concept used as a middleware (`require_middleware.middleware`) must bind only functions of type `func(http.Handler) http.Handler`, and no packs. Any other signature, or a pack, is `policy_invalid`. This is checked after loading, because it needs types; the report is then a policy failure with `rules: []`.

### 5.1 Pack `go-stdlib-sql`, concept `database.query`, for go1.26.11

The pack binds exactly these 26 symbols:

| Receiver | Bound methods |
| --- | --- |
| `(*database/sql.DB)` | `Exec`, `ExecContext`, `Query`, `QueryContext`, `QueryRow`, `QueryRowContext`, `Prepare`, `PrepareContext` |
| `(*database/sql.Tx)` | `Exec`, `ExecContext`, `Query`, `QueryContext`, `QueryRow`, `QueryRowContext`, `Prepare`, `PrepareContext` |
| `(*database/sql.Stmt)` | `Exec`, `ExecContext`, `Query`, `QueryContext`, `QueryRow`, `QueryRowContext` |
| `(*database/sql.Conn)` | `ExecContext`, `QueryContext`, `QueryRowContext`, `PrepareContext` |

Each symbol ID is `(*database/sql.<Receiver>).<Method>`, for example `(*database/sql.Stmt).QueryContext`.

**Not bound, deliberately:**

| Methods | Reason |
| --- | --- |
| `Begin`, `BeginTx`, `Commit`, `Rollback`, `Close`, `Ping`, `PingContext`, `Conn`, `Driver`, `SetConnMaxIdleTime`, `SetConnMaxLifetime`, `SetMaxIdleConns`, `SetMaxOpenConns`, `Stats` | Control and lifecycle, not queries. |
| `(*Tx).Stmt`, `(*Tx).StmtContext` | They re-bind an existing statement to the transaction and run no SQL. Executions on the returned `*Stmt` are bound. |
| `(*Conn).Raw` | Gives a callback the raw driver connection. Queries made through driver interfaces (`database/sql/driver`) inside it are **not** `database.query` in version 1. This is a known gap; to close it, bind the driver's methods explicitly in `symbols`. |
| `Rows`, `Row` and `Result` methods (`Next`, `Scan`, `Err`, ...) | They read results; the query already happened at a bound call. |

Other drivers and ORMs are unsupported unless bound explicitly in `symbols` (plan section 5).

The plan section 10 fixture for a `*Stmt` or `*Conn` escaping the repository is covered by this set. A `Stmt` prepared inside `repo` and executed outside it is a bound `(*Stmt).Exec*`/`Query*` call whose caller is outside the boundary.

## 6. `[[rule]]`

### Common fields (every kind)

| Key | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `id` | string matching `^[a-z0-9][a-z0-9_.-]*$` | yes | none | Unique among rules. Appears in the report as `rule_id`. |
| `kind` | string | yes | none | One of `forbid_import`, `require_middleware`, `restrict_concept_to_boundary`. Any other value is `policy_invalid`. |
| `scope` | boundary name | yes | none | The governed packages: the module units of the boundary (section 3). |
| `message` | non-empty string | yes | none | One-line statement of the rule. Each violation's report message is this text plus the evaluator's specifics in parentheses. |
| `remediation.message` | non-empty string | yes | none | Copied to `violations[].remediation.message`. |
| `remediation.suggest` | array of strings | no | `[]` | Copied verbatim to `violations[].remediation.suggest`. By convention these are import-path patterns, symbol IDs or route patterns. |

`remediation` is a table and may be written as dotted keys (`remediation.message = "..."`) or as an inline table. It has no other keys.

Kind-specific keys are listed below. A key that belongs to a different kind is an unknown key and gives `policy_invalid`.

### 6.1 `forbid_import`

| Key | Type | Required | Default |
| --- | --- | --- | --- |
| `target` | boundary name | yes | none |
| `except_from` | array of boundary names | no | `[]` |

There is one violation for each import declaration (one per importing file) of unit `U` on unit `T` such that all of the following hold:

- `U` is in `scope` (module units only);
- `U` is in no `except_from` boundary;
- `T` is in `target`;
- `U` is not itself in `target`. A boundary may import itself, so packages inside `auth/private/**` may import each other.

Further details:

- Only direct imports from non-test files count. Transitive imports are not violations.
- The location is the import spec. The evidence is one `import` step.
- `target` is matched against all units, so third-party and standard-library packages can be forbidden.

### 6.2 `require_middleware`

> **Advisory against adversarial code.** `require_middleware` is sound for module code written in the supported router form (router matrix, section 2) when no adversarial dependency is present. Against adversarial code, meaning dependencies or `//go:linkname` that deliberately obtain and mutate module-created routers, it is **advisory**: the two known limits L1 and L2 in the router matrix ("Known limits") can let such code register routes the analyzer does not see. CI enforcement claims for this rule must state this. Dependency changes should be reviewed under the trust-boundary controls (plan section 9).

| Key | Type | Required | Default |
| --- | --- | --- | --- |
| `middleware` | concept name | yes | none |
| `except_routes` | array of route patterns | no | `[]` |

- **Scope projection.** A route is governed iff the package containing its registration call is in `scope`. That package is `route.unit_id`, the owner of `registered_in`. The handler's package and the router's type are irrelevant. Package scope is the only way to scope routes in version 1.
- **Requirement.** A governed route is satisfied iff its effective middleware chain, computed per the router support matrix, contains at least one symbol in the `middleware` concept set, at any position.
- **Violations.** Each unsatisfied, non-excepted governed route is one violation. This includes each duplicate registration, since duplicates are separate routes. The location is the registration call.
- **Indeterminate cases** (router matrix, section 2, "Which rules a diagnostic affects"):
  - **Unknown registration identity:** a registration whose router or route Bedrock cannot identify. It affects every `require_middleware` rule, module-wide.
  - **Known registration site, no route fact** (an invalid, bounded-out or same-mux conflicting pattern). It affects each rule whose scope contains the registering package.
  - **Known route, uncertain chain.** It affects a rule only if that route is governed by the rule (in its scope, and matching none of its `except_routes`). For any other rule it is a warning.
  - **Known route, unresolved path.** It affects each rule whose scope contains the registering package, because the exceptions cannot be checked.

**Route patterns** (`except_routes`) have the form `[METHOD ]PATH`:

- `METHOD` matches `^[A-Za-z]+$` and is compared **case-sensitively** with the route's normalized method (router matrix, section 5). It may be omitted. chi methods are always upper case; net/http keeps the case as written, so `get /x` is a different route from `GET /x`.
- `PATH` starts with `/` and is split on `/` into segments. The segments are literal text, `*` or `**`, with the same meaning as in section 3. A trailing `/` is significant.
- Patterns match the **normalized** route path (router matrix, Normalization). Parameter segments such as `{id}` are compared as literal text.
- **Method matching:**
  - A pattern with no method matches every route.
  - A pattern with a method matches only routes with that exact method.
  - A route registered for any method (`method: null`) matches only method-less patterns.
  - Go's `net/http` `GET` patterns also serve `HEAD`. Bedrock still records the route as `GET`, so `HEAD /x` does not except it.
- Examples: `GET /healthz`, `/public/**` (any method, `/public` and everything under it), `GET /api/v1/items/*`.
- **Unmatched exceptions.** An exception that matches no governed route is a **warning** (`empty_match`, relevance `warning`), not `2`. An exception can only narrow a rule. An exception that matches nothing narrows nothing, so it cannot silently disable the rule. Boundaries and concepts are different: an empty one disables the rule silently, so it gives `2`.

### 6.3 `restrict_concept_to_boundary`

| Key | Type | Required | Default |
| --- | --- | --- | --- |
| `concept` | concept name | yes | none |
| `boundary` | boundary name | yes | none |

- **Governed call sites.** A call site is governed when its caller symbol is owned by a module unit in `scope`, and one of its targets is in the concept set (with the wrapper closure, section 5).
- **Guard.** Each governed call site is checked with `reachable_from: boundary`. It passes if the caller is in `In(B)`, or if every path from every `Entry` to the caller passes through `In(B)`. This is plan decision 4, unchanged. As the plan states, a caller outside `In(B)` that no `Entry` reaches at all fails: an empty set of paths guards nothing. `Entry` is derived once from the merged graph (`../schema/README.md`, Recovering Entry) and held fixed while `In(B)` is removed.
- **Violations.** Each failing call site is one violation, located at the call. The evidence chain has these steps:
  - `entry`: the `Entry` the breadth-first search reached the caller from (absent when no `Entry` reaches it);
  - `call` steps along a shortest path to the caller, and the `call` to the bound callee;
  - `concept_binding`: the bound symbol and the concept.
- **Relevance** of unknown calls follows plan section 7 with owner decision A (section 7 below). A non-exempt unknown call is relevant to the rule:
  - with reason `unit_in_scope` when its unit is in `scope`;
  - with reason `reachable_from_governed_entry` when an `Entry` owned by a unit in `scope` reaches its function.

  Otherwise it is a warning. When call facts could not be built because a package failed to load, every load failure makes the rule `INDETERMINATE`.
- **Evaluated since step 4** (`../step4/2026-11-18-step4-evidence.md`). Before step 4 this kind was listed with `rule_not_evaluated`.

## 7. Documented assumption: trusted standard library (owner decision A, 2026-11-17)

`restrict_concept_to_boundary` treats unknown calls conservatively. An unknown call is an unresolved interface or dynamic call, a bodyless function, reflection or `unsafe`. Per plan section 7, such a call that is reachable from a governed `Entry`, or sitting in a governed unit, makes the rule `INDETERMINATE`, except where one of two criteria applies. These criteria are **assumptions**, not facts proven by the analysis:

1. **Trusted standard library (`trusted_stdlib`).** A diagnostic whose call site is owned by a standard-library unit of the pinned toolchain is not counted. Standard library here means `go1.26.11`, `unit.origin = stdlib` from the loader, and listed in the pinned inventory.
   - **The assumption:** standard-library code reaches user code only through calls the analysis models (resolved interface, function-value and VTA edges), and never runs a database query on its own.
   - **Scope:** it covers only standard-library code at the pinned version. Module code and third-party code (including `chi`) get no trust.
   - **Example:** on the clean step 0 spike, this exempts the 4,410 standard-library diagnostics.
2. **No possible target (`no_possible_target`).** A non-static call site with an empty target set is not an unknown call. Empty means VTA over the closed whole-program universe (module, dependencies and standard library, built with cgo disabled) finds no possible target.
   - **Example:** this exempts the six `chi` sites in the spike, such as `(*Mux).Group` calling `fn`, which the spike never supplies.
3. **Limits.** Neither criterion applies to calls reached through user-supplied callbacks, reflection or `unsafe` in module or third-party code. So `reflection_call` and `unsafe_pointer` diagnostics in module or dependency code, and `bodyless_function` calls whose caller is in module or dependency code (for example assembly in a dependency), stay relevant.
4. **Functions reached only by reflection.** A module function called only by reflection, for example from `text/template`, has no incoming call edge. It is therefore an `Entry` itself. Paths from it are checked like any other, so the trust does not hide them.

| Diagnostic kind | Call site in the standard library | Call site in module or dependency code |
| --- | --- | --- |
| `unresolved_interface_call`, `unresolved_dynamic_call` (zero targets) | exempt (`trusted_stdlib`) | exempt (`no_possible_target`) |
| `bodyless_function` | exempt (`trusted_stdlib`) | **relevant** |
| `reflection_call` | exempt (`trusted_stdlib`) | **relevant** |
| `unsafe_pointer` | exempt (`trusted_stdlib`) | **relevant** |

Every report that evaluates a `restrict_concept_to_boundary` rule lists **both** assumptions in `assumptions` (`../schema/README.md`, schema 0.3), sorted and unique, each with its `exempted_count`. Both are listed even when a count is 0.

Each exempted diagnostic remains in `diagnostics` with relevance `warning` and `exempted_by` naming the criterion. `exempted_count` is exactly the number of such diagnostics. Attribution must be checkable from the diagnostic:

- **Standard-library membership is provenance, not path shape.** A unit is standard library only if the loader reports it as part of GOROOT (`unit.origin = stdlib`: no owning module) **and** it appears in the pinned inventory `../schema/stdlib-go1.26.11.txt`. A dotless module path such as `app/internal/helper` is module code and gets no trust.
- `trusted_stdlib` applies only to an unknown-call diagnostic whose `unit_origin` is `stdlib` and whose `unit_id` is in that inventory.
- `no_possible_target` applies only to an `unresolved_interface_call` or `unresolved_dynamic_call` site whose `unit_origin` is `module` or `dependency`.
- A standard-library zero-target site satisfies both criteria and is attributed to `trusted_stdlib`.

`spike/schemacheck` enforces all of this. Changing the pinned Go version invalidates the trust until it is re-reviewed.

The assumption is unsound if a standard-library function queries a database without a modeled edge. In the pinned version, `database/sql` runs queries only through `database/sql/driver` interfaces, whose implementations are third-party or module code and are therefore analyzed without trust.

## 8. Validation (fail closed)

| Condition | Diagnostic (scope) | Effect |
| --- | --- | --- |
| `.bedrock/policy.toml` missing | `policy_missing` (global) | No rule is evaluated, exit `2`. |
| TOML syntax error, wrong type, missing required key | `policy_invalid` (global) | No rule is evaluated, exit `2`. |
| Unknown key at any depth, including a key belonging to another rule kind | `policy_invalid` (global) | No rule is evaluated, exit `2`. |
| `version` is not `1`, or `module` differs from the analyzed module | `policy_invalid` (global) | No rule is evaluated, exit `2`. |
| `rule` absent, empty, or written as an empty array (`rule = []`) | `policy_no_rules` (global) | Exit `2`. |
| Unknown `kind`, duplicate or malformed `id` | `policy_invalid` (global) | No rule is evaluated, exit `2`. |
| Reference to an undefined boundary or concept, unknown pack, malformed glob, route pattern or symbol ID, or a middleware concept bound to a function with the wrong signature | `policy_invalid` (global) | No rule is evaluated, exit `2`. |
| A boundary referenced by a rule matches nothing in its role (`scope`: no module unit; `target`, `except_from`, `boundary`: no unit) | `empty_match` (global; `affects_rules` lists each referencing rule, reason `policy_config`) | Those rules are `INDETERMINATE`. Other rules are evaluated, and the verdict table applies: a violation elsewhere still gives `1`. |
| A concept referenced by a rule matches no universe function | `empty_match`, as above | as above |
| An `except_routes` pattern matches no governed route | `empty_match` (global, relevance `warning`) | Listed only, no verdict effect (section 6.2). |
| A boundary or concept defined but referenced by no rule | none | Allowed. It is not checked for empty match. |

In every case all diagnostics are listed in the report (plan decision 7).

**Rules a build cannot evaluate.** A valid rule whose kind the running build does not implement (in step 2, `require_middleware` and `restrict_concept_to_boundary`) is still listed in the report. It is `INDETERMINATE`, through a `rule_not_evaluated` diagnostic (schema 0.4). Bedrock fails closed here: an unchecked rule can never pass, and a release or CI gate is not a substitute.

**Report form of a policy failure.** For `policy_missing`, `policy_invalid` and `policy_no_rules`, the diagnostic has relevance **`global`**, run-level, with empty `affects_rules`. It drives the overall verdict to `2` directly. The report has `rules: []`, `violations: []` and `assumptions: []`, status `INDETERMINATE` and exit code `2`. Examples: `../schema/examples/report.policy-{missing,no-rules,invalid}.json`.

`empty_match` on a referenced boundary or concept is different. It is **rule-specific**, so it has relevance `indeterminate` and names the affected rules in `affects_rules`. Other rules are still evaluated.

## 9. The demo policy

`.bedrock/policy.toml` defines:

- boundaries `app`, `repo`, `auth`, `auth_private` and `routes`;
- concepts `database.query` (pack `go-stdlib-sql`) and `auth.middleware` (`example.com/demo/internal/auth.RequireAuth`);
- one rule of each kind.

Both `demo/clean` and `demo/violating` are the module `example.com/demo`, so this one file governs both.

| Rule | Kind | Clean | Violating (the one seeded violation) |
| --- | --- | --- | --- |
| `auth-private-import` | `forbid_import` | `auth` imports `auth/private` (excepted) | `demo/violating/internal/billing/billing.go:9` imports `example.com/demo/internal/auth/private` |
| `routes-require-auth` | `require_middleware` | All `/api/v1/**` routes sit under `r.Use(auth.RequireAuth)`. `GET /internal/export` uses `mux.Handle(p, auth.RequireAuth(...))`. `GET /healthz` is excepted. | `demo/violating/internal/server/server.go:18` registers `GET /api/v1/admin/users` outside the authenticated group |
| `db-in-repo` | `restrict_concept_to_boundary` | All queries are in `internal/repo` | `demo/violating/internal/handler/handler.go:95` `h.DB.QueryContext` in `(*Handlers).Export` |
