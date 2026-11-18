# Bedrock POC Plan (Go)

**Date:** 2026-11-17
**Status:** Revised after Codex design review (approve-with-changes)
**Parent spec:** `2026-11-15-bedrock-design.md`

## 1. Goal

Prove that Bedrock can report, for a Go service, which architectural rules failed and why, precisely enough that a coding agent (or the person driving one) can act on the report.

Bedrock reports. It does not run agents, retry, or score them. Agent integration is documentation and example hook config around the CLI.

## 2. Scope

In scope:

- Go only, one module, one pinned build profile (see section 7).
- Three rules, referred to by kind, not number: `forbid_import`, `require_middleware`, `restrict_concept_to_boundary`. Build order: `forbid_import`, then `require_middleware`, then `restrict_concept_to_boundary`.
- Routers: a bounded subset of `chi` and `net/http` (see section 6).
- A mini Go service in `demo/` with one deliberate violation per rule.
- Trust-boundary protections from the parent spec (see section 9).

Out of scope:

- TypeScript/JS analyzer and the frontend rule.
- Rust engine, DSL, Datalog IR, incremental cache.
- Agent harness, retry loops, benchmarking.
- Testing against a real open-source Go service (deferred). Conclusions apply only to the declared fixtures and build profile.
- Baselines and waivers.

## 3. Architecture

A single Go program contains the analyzer and the rule checks, with a hard boundary between them: the analyzer writes facts as versioned JSON shards, and the evaluator reads only those shards. Rule evaluation is written directly in Go and is the throwaway part.

Before the analyzer is built, pin a minimal versioned JSON schema and an example shard and report (see section 8, step 0). The schema must define:

- Deterministic IDs and ordering, including anonymous functions, generic instances, method wrappers and duplicate route registrations.
- Nullable locations, and external (dependency) symbols.
- Shard ownership and referential integrity.
- The unit's source file list and content hash, plus dependency hashes.
- Function-entry metadata, so the policy `Entry` set can be recovered from facts alone.

## 4. Decisions

| # | Decision |
| --- | --- |
| 1 | Success is shown by a demo: run a real agent against the mini service by hand and see whether the report is enough to fix the violations. No benchmark. An automated analyzer test matrix (section 10) is separate and required. |
| 2 | Single Go program; analyzer and evaluator separated by the JSON shard format. |
| 3 | Rule kinds are built in the order `forbid_import`, `require_middleware`, `restrict_concept_to_boundary`. |
| 4 | Call graph: CHA seeds VTA. Go and `golang.org/x/tools` versions are pinned. **Analysis universe and policy `Entry` set are separate.** Universe: all exported functions, `main`, `init`, registered handlers, plus closures, methods and wrappers needed for a sound graph, with instantiated generics. **`Entry` (policy): symbols with no incoming call edges, plus registered route handlers, as in the parent spec.** Exported functions are analyzed but are not entries on their own. `Entry` does not change based on violation rates. |
| 5 | Unit = Go package by import path, test files excluded. Symbol IDs use Go's fully qualified format, e.g. `example.com/app/internal/repo.(*Store).Save`. Locations are repo-relative. Each unit records its source file list and a content hash. |
| 6 | Config is one `.bedrock/policy.toml` using `[boundary.*]` and `[concept.*]` tables plus the three rule kinds. Boundaries match import paths with `**` globs. See section 5 for the schema requirements. |
| 7 | Verdict table (intentional clarification of the parent spec): any violation gives `1`; otherwise any relevant indeterminacy gives `2`; otherwise `0`. The report always lists every diagnostic. |
| 8 | Agent integration: `bedrock check` with JSON and compact text output, plus example Cursor / Claude Code hook config and CI usage in docs. |

### `reachable_from` (decision 4 detail)

For callsite caller `c` and approved boundary `B`: the guard holds iff `c` is in `B`, or removing every symbol in `B` from the graph leaves no path from any `Entry` symbol to `c`. A caller unreachable from every `Entry` fails the guard, **unless it is inside `B`**: membership in `B` is checked first and passes, so an unreachable caller inside `B` passes.

`Entry` is derived once from the final merged graph and held fixed while testing the graph with `B` removed. The guarantee is relative to that graph-derived `Entry` set. Over-approximation is **not** monotonically safe when `Entry` comes from indegree: a spurious edge can remove a symbol from `Entry` and turn a fail into a pass. Counterexample fixture: `B={b}`, edges `b->c`, `u->c`, `c->sql`, no handlers. `Entry={b,u}`, so `c` fails. Add a spurious `b->u` edge and `Entry={b}`, so `c` passes. Fixtures must cover this case, shared helpers, cycles, exported helpers that are not entries, closures and interface dispatch.

Step 0 pins the analysis universe: the transitive closure of function bodies from the loaded module packages, including dependency bodies, with the policy for bodyless functions stated. Step 0 also records the diagnostics that standard-library and dependency bodies produce on the clean demo. Libraries are not silently exempted to obtain a `PASS`.

## 5. Policy contract

The plan must ship one complete `.bedrock/policy.toml` covering all three rules, with field types, defaults and validation. Required behavior:

- Each rule has an ID, scope, boundary or concept references, message and remediation.
- `require_middleware` defines route exceptions and their matching semantics. Route scope is projected from package scope explicitly.
- Exact glob semantics, including `**` at a package root.
- Unknown keys are rejected. A missing policy file, or a policy with zero rules, fails closed (`2`).
- Empty-match validation applies to every boundary and concept referenced by a rule.
- SQL binding: the `go-stdlib-sql` pack binds query, exec and prepare methods of `database/sql` `DB`, `Tx`, `Stmt` and `Conn`. Commit, rollback and similar control methods are not bound. Other drivers and ORMs are unsupported unless bound explicitly. Fixtures must test a `Stmt` or `Conn` escaping the repository.

## 6. Router support matrix

Publish a small versioned matrix before implementing `require_middleware`. First version:

- Literal routes with direct handler references.
- `chi`: `Get`, `Post`, `Put`, `Delete`, `Method`, `Use`, and statically nested `Group`, `Route` and `With`. Effective middleware is computed per individual route.
- `net/http`: `HandleFunc` and `Handle` on `http.DefaultServeMux` or a locally constructed `ServeMux`, with pattern normalization defined for method, host and path forms (to be written before step 3, with accepted and rejected code examples).
- One direct auth wrapper form is supported: `mux.Handle(pattern, auth.RequireAuth(handler))` where `RequireAuth` is the approved middleware and `handler` is a direct reference. Any other wrapper yields a diagnostic. If the demo's `net/http` route does not use this form, it must be a documented policy exception.
- Deferred: `Mount`, aliased router values, helper-function registration, conditional middleware, loops, and general interprocedural router interpretation.

A recognized but unsupported registration, handler wrapper or middleware form yields a diagnostic that affects `require_middleware`. It never disappears and never counts as authenticated. A router registered in a helper package outside the rule's package scope also yields this diagnostic.

## 7. INDETERMINATE triggers

Conditions that make the run indeterminate (verdict `2` unless a violation exists, per the verdict table in section 4):

- A package in a governed rule's scope fails to load or type-check.
- Invalid or missing `policy.toml`, a referenced boundary or concept that matches nothing, or zero rules.
- Unsupported module or workspace layout: the POC accepts one explicit module and one pinned build profile (GOOS, GOARCH, build tags, cgo setting). The report lists included and excluded packages and files.
- Analyzer crash, timeout or memory limit.

### Pinned toolchain and build profile (step 0)

| Item | Pin |
| --- | --- |
| Go | `go1.26.11` (root `go.mod`: `go 1.26.0`, `toolchain go1.26.11`; analysis runs with `GOTOOLCHAIN=local`) |
| `golang.org/x/tools` | `v0.52.0` (root `go.mod`). `v0.53.0` was released the day of step 0 and is not used. |
| `GOOS` / `GOARCH` | `linux` / `amd64`, applied when loading the target on any host |
| cgo | `CGO_ENABLED=0` |
| Build tags | none |
| Module mode | `GOFLAGS=-mod=readonly`, `GOWORK=off`, one explicit module |
| Analyzer limits | 120 s wall clock, 4096 MiB Go runtime memory; crossing either is a global `analyzer_timeout` / `analyzer_memory_limit` diagnostic |

The profile is recorded in every manifest and report as `build_profile` and bound to shards through `build_profile_digest` (`schema/README.md`).

Relevance for `restrict_concept_to_boundary` is conservative and independent of resolved sink reachability:

- Any unknown call (unresolved function value, reflection, `unsafe`, bodyless function, unresolved interface call) that is reachable from a governed `Entry` or sits in a governed unit gives `2`, unless it is irrelevant under one of the two criteria below (owner decision A, 2026-11-17).
  - **Trusted standard library.** Unknown calls whose body is in the pinned Go standard library (go1.26.11) are not counted. This is an explicit assumption: standard-library code reaches user code only through calls the analysis already models, and it never executes a database query on its own. Third-party dependencies (for example `chi`) and module code get no such trust. The report must list the assumption and the count of exempted calls.
  - **No possible target.** A call that the whole-program analysis shows has no possible target in the analyzed universe is not an unknown call.
  - Neither criterion applies to calls reached through user-supplied callbacks, reflection or `unsafe` in module or third-party code.
- Uncertainty carries through helper packages and dependency boundaries. A governed handler calling an out-of-scope helper that has an unknown call gives `2`.
- Multiple known VTA targets are over-approximation, not missing coverage, and do not give `2` on their own.
- The plan lists the unsupported cases explicitly (reflection, `unsafe`, bodyless functions, dynamic calls) and each has a fixture.

`require_middleware`: unresolved route path or handler, unresolved or conditional middleware chain, and unsupported registration forms (section 6).

`forbid_import`: none expected once packages load.

Warning only: unknown constructs outside every governed scope and unreachable from governed entries.

## 8. Build order

0. **Schema and spikes.** Pin the JSON shard and report schema with examples (section 3). Pin Go and `x/tools` versions and the build profile. Create a minimal compilable spike module under `spike/` (a repo package, a handler, a `database/sql` call, one `chi` route). The full demo states stay in step 1. Run a bounded SSA, CHA and VTA spike on it with explicit timeout and memory expectations, and record the results and the standard-library diagnostics.
   *Delivered:* schema 0.1 and examples in `schema/` (validated by `spike/schemacheck`), pins in section 7, spike module `spike/target`, bounded spike `spike/ssaspike`, results and open issues in `step0/2026-11-17-ssa-spike.md`.
1. Repo scaffold (Go module, `.bedrock/policy.toml`, `demo/` service in all states).
   *Delivered:* `.bedrock/policy.toml`, policy contract `policy/README.md` (S1), router matrix `routers/README.md` (B4), `demo/clean`, `demo/violating`, `testdata/broken`, `.github/CODEOWNERS`, and schema 0.2 (report `assumptions`). Evidence in `step1/2026-11-17-step1-evidence.md`.
2. Analyzer: package loading, unit, symbol and import facts. `forbid_import` and the report.
   *Delivered:* `cmd/bedrock`, `internal/{analyze,policy,glob,check,facts}`. Evidence in `step2/2026-11-17-step2-evidence.md`. `require_middleware` and `restrict_concept_to_boundary` are validated but not evaluated (schema 0.3 cannot mark a rule "not evaluated"; reported).
3. Route and middleware extraction per the support matrix. `require_middleware`.
4. SSA, CHA, VTA call facts. `restrict_concept_to_boundary` with `reachable_from`.
5. Hook and CI examples, plus the by-hand agent demo.

## 9. Trust boundary

The POC keeps the parent spec's protections (invariant 2 and the CI section):

- CODEOWNERS covers `.bedrock/**`, `packs/**`, the CI workflow and the approved middleware implementation.
- CI loads policy and bindings from the base branch, never from the PR.
- The `bedrock` binary comes from a pinned, checksummed release, never from the PR. Go toolchain and dependency versions are pinned, and the package driver and build environment are controlled.
- Bedrock checks only that approved middleware is structurally attached. Replacing `RequireAuth` with a no-op wrapper keeps the facts unchanged, so an auth behavior test in the demo must fail on a no-op wrapper, and the middleware implementation is protected by CODEOWNERS.

## 10. Demo service and test matrix

Mini service (`demo/`):

- A few `chi` routes and one plain `net/http` route.
- One repo package (approved boundary), one auth middleware, an `internal/auth/private` subpackage that policy forbids other packages from importing, and a billing package. The `clean` and `violating` fixtures compile, and Go's own `internal` rule is never violated in them.
- Behavior tests, including an auth test that rejects a no-op middleware.
- States: `violating` (one violation per rule), `clean` (passes), and a separate `broken` fixture with an illegal Go import to exercise `INDETERMINATE` from a load failure. The `broken` fixture lives in its own module under `testdata/broken/`, so ordinary package loads of the demo never see it.

Automated acceptance matrix:

- Each rule kind passes and fails independently.
- Compile error gives `2`.
- Unsupported dynamic call, route and middleware each give `2`.
- Shared-helper bypass, exported helper that is not an entry, `Stmt` escaping the repository.
- Empty binding gives `2`.
- Violation plus indeterminate diagnostic gives `1`, with diagnostics listed.
- JSON output is stable and deterministic across runs.

## 11. Coordination

- `[w] ticket gate waived by operator (2026-11-17): no tickets for this POC.` Work is tracked by the build order in section 8 and this file. No `coordinator.json` exists, so defaults apply.
- Design review: Codex, 2026-11-17, approve-with-changes. Re-review at `f695d76`: approve-with-changes, step 0 may start.
  - Resolved decisions: B1, B3, S3, S4, S5, S6.
  - Pending artifacts, gated: B2 (graph-derived `Entry` caveat now documented; universe pinned in step 0), B4 (normalization rules and accepted/rejected examples before step 3), S1 (complete `policy.toml` example and glob contract before step 1 parser work), S2 (schema and examples in step 0), N1 (precedence clarified in section 7 and the parent spec).
  - S7 (cache exactness) is recorded in the parent spec as a limitation to resolve before the cache is built. Operator decisions: keep the parent spec's `Entry` definition, and keep the trust-boundary protections.

## 12. Open questions

- VTA run time and memory on the demo with the full analysis universe. Step 0 measured the spike: about 1 s and about 0.75 GiB for 16,966 functions, 99% of them standard library (`step0/2026-11-17-ssa-spike.md`). The demo is re-measured in step 4.
- ~~How a reachable unknown call is "proven irrelevant"~~ Decided 2026-11-17 (owner decision A, section 7): trusted pinned standard library plus no-possible-target. The contract is in `policy/README.md`.
- Real-world accuracy is untested until a real Go service is added as a target.
