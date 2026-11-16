# Bedrock — Architecture & MVP Design Specification

**Date:** 2026-11-15
**Status:** Draft for Review
**Tagline:** Let agents build. Let invariants govern.

---

## 1. Purpose & Design Goals

Bedrock is an executable architecture and security governance engine for agentic software development. Organizations express invariants in a language-independent DSL; Bedrock compiles those rules into a typed relational Intermediate Representation (IR), resolves abstract concepts to concrete code via protected semantic binding manifests, evaluates codebases through native language analyzer workers, and enforces policies deterministically in local CLIs and CI/CD.

### Core Invariants of the Design

1. **Language Independence via Semantic Bindings:** Policies target abstract concepts (`database.query`, `auth.middleware`) and architectural boundaries (`approved_repository`, `internal_endpoint`), never hardcoding language-specific AST idioms inside policy files.
2. **Independent Enforcement:** Coding agents are treated as untrusted actors. Policies, bindings, and CI workflows are protected by repository ownership controls and evaluated outside agent authority.
3. **Fail Safely:** Analysis errors, worker failures, or unresolved dynamic constructs emit explicit uncertainty diagnostics and yield `INDETERMINATE` (exit `2`), never a silent `PASS`.
4. **Strict Zero-Tolerance (MVP):** Any rule violation fails the check (`VIOLATION`, exit `1`). Baselines and waiver files are explicitly out of scope for the MVP.
5. **Explainability & Agent Remediation:** Every violation includes a deterministic derivation witness chain and structured remediation hints with approved alternatives.

---

## 2. System Architecture & Repository Layout

Bedrock uses a **Rust core engine** coordinating **native language analyzer workers** (Go and TypeScript) over a versioned JSON IPC protocol.

```text
+-----------------------------------------------------------------------+
|                         Enforcement Interfaces                        |
|         CLI (`bedrock`)  |  GitHub Actions  |  JSON Agent Report      |
+-----------------------------------+-----------------------------------+
                                    |
+-----------------------------------v-----------------------------------+
|                        Rust Core Engine                               |
|  - DSL Lexer / Parser / Typechecker -> Typed Relational (Datalog) IR  |
|  - Semantic Binding & Pack Resolver                                   |
|  - Incremental Module-Sharded Fact Cache                              |
|  - Semi-Naive Fixed-Point Evaluator + Provenance Witness Tracer       |
+-------------------+-------------------------------+-------------------+
                    | JSON IPC (stdin/stdout)       |
        +-----------v-----------+       +-----------v-----------+
        | bedrock-analyzer-go   |       | bedrock-analyzer-ts   |
        | go/packages +         |       | TypeScript Compiler   |
        | callgraph + AST       |       | API (ts.createProgram)|
        | route/middleware      |       | + HTTP client extract |
        +-----------------------+       +-----------------------+
```

### Target Monorepo Structure (`/Users/lavishgent/Desktop/bedrock`)

- `crates/bedrock-dsl/` — Lexer, parser, AST, semantic validation, typechecker, and Datalog IR compiler.
- `crates/bedrock-facts/` — Shared Semantic Fact Graph types, JSON IPC schema, shard hashing, and serialization.
- `crates/bedrock-engine/` — Binding & pack loader, incremental shard cache manager, semi-naive fixed-point evaluator, witness builder, and tri-state verdict resolver.
- `crates/bedrock-cli/` — `bedrock` binary (`compile`, `check`, `graph`), human terminal formatter, and agent-consumable JSON reporter.
- `analyzers/go/` — `bedrock-analyzer-go` Go module using `go/packages`, `golang.org/x/tools/go/ssa` + `callgraph/vta` (with `cha` fallback when entrypoints are absent), and AST inspection for HTTP routers/middleware.
- `analyzers/ts/` — `bedrock-analyzer-ts` Node/TypeScript package using `typescript` Compiler API (`ts.createProgram`) and AST inspection for outbound HTTP calls (`fetch`, `axios`, typed client wrappers).
- `packs/` — Built-in semantic binding and framework adapter packs (`go-stdlib-sql`, `go-net-http`, `go-chi`, `ts-fetch`, `ts-axios`).
- `demo/` — Sample full-stack repository (Go backend + TypeScript frontend) exercising all four MVP invariants, deliberate violations, and verified agent remediation.
- `.github/workflows/bedrock.yml` — Reference CI enforcement workflow with base-branch policy protection.

---

## 3. Semantic Fact Graph Contract (Worker IPC)

Language analyzers do not evaluate policies. Their sole responsibility is translating source units into a deterministic, language-independent **Semantic Fact Graph** serialized as JSON shards.

### Worker Invocation Protocol

The Rust core invokes each worker with a JSON request on `stdin`:

- `repo_root`: absolute repository path
- `units`: list of target package/project roots or changed unit paths to analyze
- `enabled_adapters`: active framework adapter IDs from loaded bindings
- `schema_version`: `"1.0"`

The worker writes a JSON response on `stdout` containing one `UnitShard` per analyzed unit (Go package or TypeScript `tsconfig`/module unit):

### Canonical Fact Relations

Each `UnitShard` contains:

| Relation | Fields | Purpose |
| --- | --- | --- |
| `unit` | `id`, `lang` (`go` \| `ts`), `path`, `content_hash`, `dep_hashes[]` | Identifies a compilable package or module unit |
| `symbol` | `id`, `unit_id`, `kind` (`func` \| `method` \| `type` \| `var`), `qname`, `loc` | Globally unique qualified symbol definition |
| `import` | `from_unit`, `to_unit`, `to_path`, `loc` | Direct module/package import edge |
| `call` | `caller_sym`, `callee_sym`, `loc` | Resolved static/interface call edge |
| `route` | `id`, `unit_id`, `method`, `path`, `handler_sym`, `visibility` (`public` \| `internal`), `loc` | Registered HTTP endpoint and handler |
| `route_middleware` | `route_id`, `middleware_sym`, `order`, `loc` | Middleware wrapping a route in execution order |
| `http_call` | `caller_sym`, `method`, `url_pattern`, `resolution` (`literal` \| `dynamic`), `loc` | Outbound HTTP request from client/service code |
| `diagnostic` | `unit_id`, `severity` (`error` \| `warning`), `kind`, `message`, `loc` | Parse errors, typecheck failures, or dynamic ambiguity |

### Fail-Safe Analyzer Diagnostics

Analyzers must emit `diagnostic` records whenever precision is compromised:
- `parse_or_type_error`: source unit failed to parse or typecheck.
- `unresolved_dynamic_call`: reflection, `eval`, or unresolvable function pointer inside a governed unit.
- `unresolved_dynamic_url`: `http_call` whose URL path cannot be statically resolved (e.g., `fetch(base + dynamicPath)`).

If a governed unit has any `error`-severity diagnostic—or an `unresolved_dynamic_*` diagnostic affecting an active invariant scope—the engine marks the run `INDETERMINATE` (exit `2`).

---

## 4. Semantic Binding Layer & Framework Packs

Policies reference abstract **Concepts** and **Boundaries**. Concrete mapping lives outside policy rules in `.bedrock/bindings.toml` plus versioned built-in `packs/`.

### Concepts vs. Boundaries

- **Concept (`concept`):** A semantic capability or role attached to symbols (e.g., `database.query`, `auth.middleware`).
- **Boundary (`boundary`):** A named set of units, symbols, or endpoints (e.g., `approved_repository`, `frontend`, `internal_endpoint`).

### Example Binding Manifest (`.bedrock/bindings.toml`)

```toml
version = "1"
packs = ["go-stdlib-sql", "go-chi", "ts-fetch"]

[boundary.approved_repository]
units = ["backend/internal/repo/**"]

[boundary.frontend]
units = ["frontend/src/**"]

[boundary.internal_endpoint]
route_paths = ["/internal/**"]
route_visibility = ["internal"]

[concept."database.query"]
extends_pack = "go-stdlib-sql.query"
symbols = ["backend/internal/db.(*Client).Exec*"]

[concept."auth.middleware"]
symbols = ["backend/internal/auth.RequireAuth"]
```

### Built-In Framework Adapter Behavior

- **`go-stdlib-sql`:** Binds `database/sql.(*DB).Query*`, `Exec*`, `Prepare*`, and `(*Tx).*` to `database.query`.
- **`go-chi` / `go-net-http`:** Extracts `r.Get/Post/Put/Delete/Route/Group` and `r.Use(...)` middleware chains, emitting `route` and `route_middleware` facts. Route groups mounted under `/internal` or annotated via binding `route_paths` receive `visibility = "internal"`.
- **`ts-fetch` / `ts-axios`:** Extracts `fetch(...)` and `axios.*` calls. Constant template literals and known string constants resolve to `resolution = "literal"` with normalized paths; non-constant expressions emit `resolution = "dynamic"` plus a diagnostic.

---

## 5. Bedrock DSL & Typed Relational IR

### MVP DSL Syntax

The DSL is declarative, line-and-block structured, and supports four rule patterns required for architectural and security governance:

```text
invariant "database access requires repository boundary" {
    scope: repository("**")

    prohibit:
        calls(concept("database.query"))
        unless reachable_from:
            boundary("approved_repository")

    remediation:
        message: "Database queries must execute inside approved repository packages."
        suggest: boundary("approved_repository")
}

invariant "no cross-module internal billing import" {
    scope: boundary("backend_modules")

    prohibit:
        imports(boundary("auth_internal"))
        from: boundary("billing")

    remediation:
        message: "Use public auth interfaces in backend/internal/auth instead of internal helpers."
        suggest: "backend/internal/auth"
}

invariant "routes require approved auth middleware" {
    scope: boundary("backend_routes")

    require:
        route_handler
        wrapped_by: concept("auth.middleware")
        except_routes: ["/healthz", "/public/**"]

    remediation:
        message: "Attach RequireAuth middleware to every non-public HTTP route."
        suggest: concept("auth.middleware")
}

invariant "frontend cannot call internal backend endpoints" {
    scope: boundary("frontend")

    prohibit:
        http_calls(boundary("internal_endpoint"))

    remediation:
        message: "Call public BFF/API endpoints instead of /internal/* routes."
        suggest: "/api/v1/**"
}
```

### Compiler Pipeline (`crates/bedrock-dsl`)

1. **Lexer & Recursive-Descent Parser:** Produces source-spanned AST nodes with clear syntax error diagnostics.
2. **Symbol & Binding Validation:** Verifies every `concept(...)` and `boundary(...)` identifier exists in the resolved manifest/packs; fails compilation on undefined references or duplicate invariant names.
3. **Typechecking:** Verifies predicate arities, enforces typed sorts (`Unit`, `Symbol`, `Route`, `Endpoint`) across predicate arguments and `unless` clauses, and accepts `boundary(...)`, `concept(...)`, or string literals in `remediation.suggest`.
4. **IR Lowering:** Compiles AST invariants into a **Typed Relational / Datalog IR**:
   - Base EDB relations populated from `UnitShard` facts + materialized bindings (`in_boundary(UnitOrRoute, Boundary)`, `has_concept(Sym, Concept)`).
   - Derived IDB rules for call-path reachability (`call_reach(Caller, Callee)` is reflexive for symbols inside a boundary and transitive along `call` edges). A callsite `(Caller, Callee)` satisfies `unless reachable_from: B` when `Caller` is inside boundary `B` or every non-`B` call path reaching `Caller` traverses `B` (preventing bypass via shared helpers).
   - Violation heads: `violation(RuleId, PrimaryLoc, RemediationId)` derived deterministically when prohibited patterns hold and exception/reachability guards fail.

---

## 6. Evaluation Engine, Incremental Cache & Output Schema

### Semi-Naive Fixed-Point Evaluator (`crates/bedrock-engine`)

- Materializes EDB relations and evaluates stratified Datalog rules to a deterministic fixed point.
- Maintains **provenance witness pointers** for each derived tuple so every `violation` reconstructs an ordered `evidence_chain` (e.g., `route POST /admin -> handler AdminExport -> calls db.Query` or `frontend/src/admin.ts:14 -> http_call POST /internal/export -> matches route /internal/export`).
- Sorts final violations deterministically by `(invariant_id, file, line, column)`.

### Incremental Module-Sharded Cache

- Cache directory: `.bedrock/cache/` (gitignored, keyed by `analyzer_version + schema_version + unit_id`).
- Cache key per unit: `sha256(unit_content_hash + sorted(direct_dep_hashes) + active_adapters)`.
- On `bedrock check --diff <base>..<head>` (or `--changed <paths>`):
  1. Compute modified files via `git diff --name-only` (or explicit path list).
  2. Map changed files to owning `unit` IDs, then compute transitive reverse-dependent units from the cached `import` graph.
  3. Re-run `bedrock-analyzer-go` / `bedrock-analyzer-ts` **only** on dirty units + reverse dependents; read unmodified units from cached `UnitShard` JSON files.
  4. Merge all shards into the full repository Semantic Fact Graph and run the fixed-point Datalog evaluator—guaranteeing exactness for global `reachable_from` and cross-service rules.
- If cache metadata is missing, corrupt, or `bindings.toml`/pack versions changed, fall back cleanly to full analysis.

### Exit Codes & JSON Report Format

- Exit `0`: `PASS` — zero violations and zero fail-safe indeterminacies.
- Exit `1`: `VIOLATION` — one or more policy violations detected.
- Exit `2`: `INDETERMINATE` — parse/type failure, worker crash, invalid policy/binding config, or unresolved dynamic construct in governed scope.

JSON output (`bedrock check --format json`):

```json
{
  "schema_version": "1.0",
  "status": "VIOLATION",
  "summary": {
    "violations": 1,
    "indeterminate_diagnostics": 0,
    "units_analyzed": 12,
    "units_cached": 9
  },
  "violations": [
    {
      "invariant_id": "database access requires repository boundary",
      "severity": "error",
      "location": { "file": "backend/internal/handler/orders.go", "line": 42, "column": 15 },
      "message": "Symbol `OrderHandler.Create` calls `database.query` outside `approved_repository`.",
      "evidence_chain": [
        { "kind": "call", "from": "handler.OrderHandler.Create", "to": "(*sql.DB).ExecContext", "file": "backend/internal/handler/orders.go", "line": 42 }
      ],
      "remediation": {
        "message": "Database queries must execute inside approved repository packages.",
        "suggest": ["backend/internal/repo/**"]
      }
    }
  ],
  "diagnostics": []
}
```

---

## 7. Enforcement Interfaces & Tamper Resistance

### CLI Commands (`crates/bedrock-cli`)

- `bedrock compile [--policy-dir .bedrock] [--emit-ir]` — Parses, typechecks, resolves bindings/packs, and outputs compiled IR or validation errors.
- `bedrock check [--policy-dir .bedrock] [--diff <base>..<head>] [--format human|json]` — Runs incremental/full analysis, evaluates IR, and emits report + exit code `0|1|2`.
- `bedrock graph [--format json]` — Dumps the merged Semantic Fact Graph for debugging and inspection.

### GitHub Actions & Policy Integrity

- `.github/workflows/bedrock.yml` triggers on `pull_request`.
- **Tamper Resistance:**
  1. `.bedrock/**` and `.github/workflows/bedrock.yml` are protected in `.github/CODEOWNERS` requiring security/architecture maintainer review.
  2. In CI, the workflow checks out the **base branch's** `.bedrock/` directory into a temporary policy bundle directory and runs `bedrock check --policy-dir <base-policy-dir>` against the PR workspace, ensuring an untrusted agent PR cannot modify rules or bindings to self-approve.
  3. On non-zero exit (`1` or `2`), CI uploads the JSON report artifact, renders violations in the GitHub Job Summary, and fails the required status check.

---

## 8. MVP Sample Demo (`demo/`)

The `demo/` workspace proves end-to-end multi-language governance across a realistic Go backend and TypeScript frontend:

- **Backend (`demo/backend/`):** Go HTTP service (`chi` + `database/sql` / SQLite or stub driver) with:
  - `internal/repo/` (approved repository boundary)
  - `internal/auth/` (approved `RequireAuth` middleware + `internal/auth/internal/` private subpackage)
  - `internal/billing/`, `internal/handler/`, and route setup (`public`, `/api/v1/**`, `/internal/**`)
- **Frontend (`demo/frontend/`):** TypeScript module calling backend endpoints via `fetch`.
- **Four Deliberate Demo Violations (verified on a `demo-violating` fixture/state):**
  1. Handler executes `db.QueryContext(...)` directly instead of calling `internal/repo`.
  2. `internal/billing` imports `internal/auth/internal` across a prohibited module boundary.
  3. `/api/v1/admin/users` route registered without `auth.RequireAuth` middleware.
  4. `demo/frontend/src/client.ts` invokes `fetch("/internal/export")` directly.
- **Verification Loop:** Running `bedrock check --format json` detects all 4 violations with exact witness chains and remediation hints; applying the four corresponding fixes (moving query to `internal/repo`, using public `auth` API, wrapping route with `RequireAuth`, switching frontend to `/api/v1/export`) transitions `bedrock check` to `PASS` (`exit 0`).

---

## 9. Testing Strategy

1. **Rust Compiler & Engine Unit/Golden Tests:**
   - Lexer/parser snapshot tests for valid DSL rules and syntax error recovery.
   - Typechecker tests rejecting undefined concepts/boundaries and type mismatches.
   - Datalog evaluator tests verifying transitive `reachable_from`, negation/guards, deterministic sorting, and provenance witness construction.
   - Incremental cache tests verifying dirty-unit + reverse-dependent invalidation produces identical output to a cold full analysis.
2. **Analyzer Worker Tests (`go test` & TypeScript test suite):**
   - Fixture packages validating exact `unit`, `symbol`, `import`, `call`, `route`, `route_middleware`, and `http_call` emission.
   - Fail-safe tests confirming syntax errors, missing imports, and dynamic `fetch(var)` emit `diagnostic` records that force `INDETERMINATE` (`exit 2`).
3. **End-to-End Integration Tests:**
   - Execute `bedrock check --format json` against `demo/` in three states: deliberate violations (`exit 1`, 4 expected violations), broken syntax/dynamic call (`exit 2`), and remediated codebase (`exit 0`).
