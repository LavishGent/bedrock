# Bedrock POC fact and report schema, version 0.3

**Status:** Pinned in POC step 0 (plan `../2026-11-17-bedrock-poc-plan.md`, sections 3 and 8).
**Scope:** The Go POC only. The parent spec's multi-language IPC (`../2026-11-15-bedrock-design.md`, section 3) stays the long-term target. This schema is the POC's concrete form of it.

This README is normative. The JSON Schemas check structure. The rules below cover what JSON Schema cannot express: ordering, ID construction, ownership and referential integrity, hash recomputation, verdict consistency and assumption attribution. `spike/schemacheck` checks both.

## Files

| File | Validates |
| --- | --- |
| `common.schema.json` | Shared types: IDs, hashes, locations, build profile, diagnostics. |
| `shard.schema.json` | One `unit_shard`: all facts for one Go package. |
| `analysis.schema.json` | One `analysis_manifest` per analyzer run: the shard set, the build profile, the universe, the Entry digest and global diagnostics. |
| `report.schema.json` | The `bedrock check --format json` report. |
| `examples/shard.spike-cmd-server.json` | A real shard for `spike/target/cmd/server`. File hashes, content hash, dependency hashes and call-site ordinals come from the spike under the pinned profile. |
| `examples/shard.illustrative-api.json` | A hand-written shard that exercises closures, generic instances, bound and pointer wrappers, duplicate routes, middleware, excluded files, null locations and a reflection call in module code. Hashes are placeholders. |
| `examples/analysis.illustrative.json` | A manifest with a global diagnostic. The unit list is abbreviated. |
| `examples/report.illustrative.json` | A `VIOLATION` report that also carries an indeterminate diagnostic, plain warnings and three exempted diagnostics. It shows precedence, affected-rule reporting, and assumption counts that reconcile with `exempted_by` attributions. |
| `examples/report.policy-missing.json`, `report.policy-no-rules.json`, `report.policy-invalid.json` | Policy failures: one diagnostic with relevance `global`, `rules: []`, `INDETERMINATE`, exit 2. |
| `stdlib-go1.26.11.txt` | The pinned standard-library package inventory (`go list std`, linux/amd64, cgo off), used to check `stdlib` provenance. |
| `examples/invalid/*.json` | Negative fixtures. Each must be rejected with the error text recorded in `examples/invalid/expect.json`. |

Validate everything (from the repo root). The test accepts every `examples/*.json` and rejects every `examples/invalid/*.json` for its recorded reason:

```sh
go test ./spike/schemacheck
go run ./spike/schemacheck docs/superpowers/specs/schema docs/superpowers/specs/schema/examples/*.json   # one-off, schema chosen by kind
```

## Versioning

Every document has `"schema_version": "0.3"` and a `kind`. While the POC is `0.x`, any incompatible change bumps the minor version. Readers reject versions they do not know; there is no best-effort parsing. Schema `$id`s live under `https://schemas.bedrock.invalid/poc/0.3/`. They are identifiers, not fetchable URLs.

Changelog:

- **0.3** (step 1 review fixes). These changes are incompatible with 0.2:
  - **B1, run-level indeterminacy.** Report diagnostics gain relevance `global` (with `scope: global`, empty `affects_rules`), which drives the verdict to 2 directly. `summary.global_diagnostics` is new. A policy failure means `rules: []`. The `global_failure` reason is removed from `affects_rules`.
  - **S1, assumption attribution.** Report diagnostics gain the required `exempted_by`. When a `restrict_concept_to_boundary` rule is evaluated, both assumptions are listed, even with count 0. Counts must reconcile with `exempted_by`.
  - **S1 follow-up (compatible):** report diagnostics gain optional `unit_origin`, required by semantics on exempted diagnostics; `stdlib-go1.26.11.txt` added.
  - **S4, method case.** Route methods may be lower case (net/http keeps the method as written).
- **0.2** (step 1): the report gains the required `assumptions` array (`trusted_stdlib`, `no_possible_target`, each with `exempted_count`). Plan section 7 (owner decision A) requires the report to list the trusted-standard-library assumption and the count of calls it exempted. The relevance criteria themselves are defined in `../policy/README.md`.
- **0.1** (step 0): initial version.

## Canonical JSON and hashes

- **Canonical JSON** means RFC 8785 (JCS) for the value shapes used here: object keys sorted, no insignificant whitespace, strings in UTF-8 with no HTML escaping, integers written without exponent or fraction.
- **Hashes** are written `sha256:<64 lowercase hex>`.
- **File hash**: sha256 of the file's raw bytes.
- **Unit `content_hash`**: sha256 of the concatenation, over `unit.files` in order, of `path`, NUL, the file hash's hex digits, LF. `path` is the file's location path (see Locations).
- **`dep_hashes`**: the `content_hash` of each direct import, one entry per imported unit. Changes deeper in the graph reach a unit through the reverse-dependency closure, as in the parent spec's cache design. They do not reach it through this field. The parent spec's S7 limitation still applies to call facts.
- **`build_profile_digest`**: sha256 of the canonical JSON of the manifest's `build_profile`. A shard merges only with a manifest whose digest matches.
- **Entry digest**: sha256 over the sorted Entry symbol IDs, each followed by LF.

## Locations

A location is `{root, module, file, line, column}`, or `null` when the fact has no source position.

| `root` | `file` is relative to | `module` |
| --- | --- | --- |
| `repo` | the analyzed repository root | `null` |
| `module` | the root of dependency module `module` | `path@version`, required |
| `goroot` | `$GOROOT/src` | `null` |

Paths use forward slashes and contain no `.` or `..` segments. `line` and `column` are 1-based, and `column` counts bytes (`go/token`). A call's location is go/ssa's call position, which is the opening parenthesis for ordinary calls.

`null` is required, not optional, for facts with no position: synthetic wrappers (`$bound`, `$thunk`, pointer and promotion wrappers), the synthesized package initializer `pkg.init`, and call sites inside those. In the real example, `example.com/spike/cmd/server.init` and its six call sites have `loc: null`. A generic instance uses its origin's location.

## Identifiers

### Unit IDs

A unit ID is the package's Go import path, for example `example.com/spike/internal/repo` or `net/http`. Test packages and `_test.go` files are never units. They appear in `excluded_files` with reason `test_file`.

### Symbol IDs

The base form is go/ssa's `(*ssa.Function).String()` under the pinned `golang.org/x/tools`:

| Construct | ID form | Example |
| --- | --- | --- |
| Function | `pkg.F` | `example.com/spike/cmd/server.main` |
| Method | `(pkg.T).M`, `(*pkg.T).M` | `(*example.com/spike/internal/repo.Store).SaveItem` |
| Anonymous function | `<enclosing ID>$<n>`, n from 1 in source order within the enclosing function; nested closures append another `$<n>` | `example.com/app/internal/api.Register$1` |
| Closure in a package-level initializer | `pkg.init$<n>` | |
| Synthesized package initializer | `pkg.init` (kind `func`, `entry_hints.is_init`) | `example.com/spike/cmd/server.init` |
| Declared `func init()` | `pkg.init#<n>`, n from 1 in source order | |
| Bound method value | `<method ID>$bound` | `(*example.com/spike/internal/handler.Items).Create$bound` |
| Interface or method expression thunk | `<method ID>$thunk` | `(*compress/flate.compressor).fillStore$thunk` |
| Pointer or promotion wrapper | the method's name with the wrapper's own receiver | `(*example.com/app/internal/api.Handler).Ping` wraps `(example.com/app/internal/api.Handler).Ping` |
| Generic instance | `<origin ID>[<type args>]`, written as go/ssa writes them (`, ` separated) | `example.com/app/internal/api.Map[int, string]` |
| Type, var | `pkg.T`, `pkg.V` | `example.com/app/internal/api.Handler` |

The bare go/ssa string is **not** unique. The step 0 spike found 69 colliding names in a universe of 16,966 functions (see `../step0/2026-11-17-ssa-spike.md`). Three canonicalization rules apply:

1. **C1, type-parameter arguments are qualified.** go/ssa builds instances inside generic bodies whose type arguments are still type parameters. The spike found 187 of these. In such an instance, each type parameter used as a type argument is written `Name@<owner ID>`, where the owner is the generic function or type that declares the parameter. For example, `cmp.Less[E]` instantiated inside `slices.pdqsortOrdered` becomes `cmp.Less[E@slices.pdqsortOrdered]`. The generic origin itself keeps its plain form (`cmp.Less`). C1 targets the 11 differing `instance`/`method` groups and the 38 equivalent instance groups seen in the spike. The spike has not yet shown that C1 removes them all; step 2 must re-run the collision check with C1 applied, and C3 catches any that remain.
2. **C2, identical wrappers are one symbol.** go/ssa may create several `Function` objects for one wrapper. The spike saw `(*database/sql.driverConn).releaseConn$bound` seven times. They share a canonical ID and are emitted as one symbol. Their bodies are the same instruction sequence, so call-site ordinals line up, and each site's `targets` is the union across copies. For 19 of the 20 wrapper groups the copies already had identical targets. For `(net/http.Handler).ServeHTTP$bound` (5 copies), VTA gave the copies different targets, so the union is an over-approximation. Merging does not change the Entry set, because every target already had an incoming edge. It only adds paths, so it can turn a `reachable_from` pass into a fail but never a fail into a pass. See open issue 2 in the spike note.
3. **C3, any remaining collision is a failure.** If two distinct functions still share an ID after C1 and C2, the analyzer emits a global `symbol_id_collision` error and the run is indeterminate. IDs are never disambiguated by creation order. go/ssa builds packages in parallel, so creation order is not stable.

**Owner unit of a symbol:**

- For a function or method, the package that declares it.
- For a closure, the package of its enclosing function.
- For an instance, the package of its origin.
- For a wrapper, the package that declares the receiver's named type. The spike found four wrappers (`(*net/http.requestBodyReadError).Error` and similar) that go/ssa leaves without a package; this rule assigns them to `net/http`.

### Call-site IDs

A call-site ID is `<caller ID>#<ordinal>`. The ordinal is the 0-based index of the call among the caller's non-builtin call instructions, in SSA block order and then instruction order. Ordinals are contiguous from 0 for each caller. They are stable for fixed source, fixed build profile and pinned `x/tools`, but not across `x/tools` upgrades, which is one reason the version is pinned.

### Route IDs

A route ID is `route:<unit ID>:<METHOD or *>:<normalized path>#<duplicate index>`.

- `*` means the registration matches any method.
- An unresolved path is written as the empty string.
- `duplicate_index` is 0 for the first registration of the same (unit, method, path) and increases in (file, line, column) order. Duplicate registrations are kept as separate routes, each with its own middleware and its own verdict, and the second and later ones also get a `duplicate_route` warning. See `route:example.com/app/internal/api:GET:/items#0` and `#1` in the illustrative shard.
- `handler_sym` is the user-level handler, with `$bound` unwrapped to the method. `handler_value_sym` is the wrapper value actually registered.

### Diagnostic and violation IDs

- **Diagnostic**: `diag:` followed by the first 16 hex digits of sha256(canonical JSON of `[kind, scope, unit_id, symbol_id, site_id, loc.root, loc.module, loc.file, loc.line, loc.column, message]`). A null `loc` contributes nulls. Two diagnostics with the same tuple are the same diagnostic and are emitted once.
- **Violation**: `viol:` followed by the first 16 hex digits of sha256(canonical JSON of `[rule_id, location.file, location.line, location.column, [[kind, from, to] for each evidence step]]`).

## Ordering

All arrays are sorted, and in an ID-keyed array the IDs are unique. Strings compare bytewise in UTF-8, integers compare numerically, and `null` sorts before any value.

| Array | Sort key |
| --- | --- |
| `unit.files`, `unit.excluded_files` | `path` |
| `unit.dep_hashes` | `unit_id` |
| `symbols`, `external_symbols` | `id` |
| `imports` | `to_unit`, `loc.file`, `loc.line`, `loc.column` |
| `call_sites` | `caller`, `ordinal` |
| `call_sites[].targets` | the ID itself |
| `routes` | `id` |
| `route_middleware` | `route_id`, `order` (0 = outermost) |
| shard `diagnostics`, manifest `global_diagnostics` | `id` |
| manifest `units`, `excluded_packages` | `id` |
| report `rules` | `id` |
| report `violations` | `rule_id`, `location.file`, `location.line`, `location.column`, `id` |
| report `diagnostics` | relevance (`global`, then `indeterminate`, then `warning`), then `id` |
| report `assumptions` | `id` |
| report `rules[].violation_ids`, `rules[].diagnostic_ids`, `packages.included` | the ID itself |
| report `diagnostics[].affects_rules` | `rule_id` |

## Shard ownership and referential integrity

A shard owns exactly one unit. It owns:

- the symbols whose owner unit is that unit;
- the call sites whose caller it owns;
- the imports whose `from_unit` is that unit;
- the routes registered by code in that unit, with their middleware;
- the `scope: unit` diagnostics for that unit.

Every other symbol a shard mentions (call targets, static callees, route handlers, middleware, parent and origin IDs) must appear in its `external_symbols` with the owner unit. Each external symbol must be referenced at least once, and owned and external sets are disjoint.

When merging, the evaluator checks four things:

1. Every unit in the manifest has exactly one shard, the shard's canonical-JSON hash matches the manifest entry, and its `build_profile_digest` matches the manifest's.
2. Each external symbol is defined by the shard of the unit it names. If not, the evaluator emits a global `unresolved_external_symbol` error.
3. Symbol IDs are unique across all shards. A duplicate is a `symbol_id_collision` error.
4. The recovered Entry set matches `manifest.entry`. A mismatch is an `entry_mismatch` error.

Any failure is a global error, and every rule becomes indeterminate unless a violation exists.

## Analysis universe and bodyless functions

`universe.definition = all_functions_transitive_closure_v1`. The universe is `ssautil.AllFunctions` over the SSA program built with `ssa.InstantiateGenerics` from every package in the transitive import closure of the module patterns. Every package is loaded with full syntax and types under the pinned build profile, tests excluded. This covers the module, its dependencies and the standard library.

The universe is a superset of the set listed in plan decision 4. It contains every package-level function and method of every runtime type, plus closures, wrappers, generic origin bodies (250 in the spike) and instances, including partially instantiated ones. `symbol.in_universe` marks membership.

`universe.bodyless_policy = leaf_with_diagnostic_v1`. A bodyless function (assembly, `go:linkname`, or a compiler intrinsic; 549 in the spike, all in the standard library) is a universe member with `has_body: false` and no call sites. Every call site whose target set includes a bodyless function carries a `bodyless_function` diagnostic owned by the caller's unit. Libraries are not exempted. Whether a diagnostic changes a verdict is decided by the evaluator (plan section 7), not by the analyzer.

## Recovering Entry

Policy `Entry` (plan decision 4, unchanged) is recoverable from shards alone:

```
Entry = { s in all shards' symbols : s.in_universe
                                     and s.kind in {func, method, closure, instance, wrapper}
                                     and no call site in any shard lists s in targets }
      ∪ { r.handler_sym : r in all shards' routes, r.handler_sym != null }
```

- Self-edges count as incoming edges, so a function called only by itself is not an Entry. This follows the parent spec's "no incoming call edges".
- `entry_hints` (`is_main`, `is_init`, `address_taken`) explain Entry membership but never decide it.
- Recovery is only defined when `manifest.status = complete` and the merge checks above pass. Otherwise Entry is unknown, and every rule that uses `reachable_from` is indeterminate.
- The evaluator recomputes Entry and compares its count and digest with `manifest.entry`.

## Diagnostics, severity and relevance

- **Unit diagnostics** (`scope: unit`, `unit_id` set) live in the owning shard.
- **Global diagnostics** (`scope: global`, `unit_id: null`) live in the manifest's `global_diagnostics`. They cover policy errors, unsupported layout, crash, timeout, memory limit and merge-integrity failures, and are never owned by a shard. Policy diagnostics come from the evaluator and appear only in the report.
- **`severity`** is the analyzer's statement. `error` means the facts for that scope are incomplete, for example a load or type error or an abort. `warning` means precision was lost locally, as with an unresolved call, reflection, `unsafe` or a bodyless callee.
- **`relevance`** appears only in the report and is decided by the evaluator under plan section 7. It takes one of three values:

  | Relevance | Meaning | Constraints |
  | --- | --- | --- |
  | `global` | **Run-level** indeterminacy: the whole run cannot be trusted. Counts toward verdict 2 directly. | `scope: global`, empty `affects_rules`. Required for the run-level kinds: `policy_missing`, `policy_invalid`, `policy_no_rules`, `analyzer_crash`, `analyzer_timeout`, `analyzer_memory_limit`, `symbol_id_collision`, `unresolved_external_symbol`, `entry_mismatch`. |
  | `indeterminate` | **Rule-specific** uncertainty. Counts toward verdict 2. | `affects_rules` names each affected rule. Example: `empty_match` on one rule's boundary. |
  | `warning` | Affects no rule, including exempted diagnostics. | Empty `affects_rules`. |

- **`affects_rules`** lists each affected rule with a reason: `policy_config`, `unit_in_scope`, `reachable_from_governed_entry`, `route_registration` or `middleware_chain`. It is non-empty exactly when relevance is `indeterminate`.
- **Policy failures.** With `policy_missing`, `policy_invalid` or `policy_no_rules`, no rule is evaluated. The report has `rules: []`, `violations: []` and `assumptions: []`, status `INDETERMINATE`, exit 2. See the three `report.policy-*.json` examples.
- **Rule results**: `rules[].diagnostic_ids` lists the `indeterminate` diagnostics that name the rule, including for rules that also have violations. Global diagnostics are not repeated per rule.
- **Verdict per rule**: `VIOLATION` if it has violations; else `INDETERMINATE` if it has `diagnostic_ids` **or any global diagnostic exists**; else `PASS`. No rule is `PASS` in a run with a global diagnostic.
- **Overall verdict**: `VIOLATION`/1 if any violation exists, else `INDETERMINATE`/2 if any `global` or `indeterminate` diagnostic exists, else `PASS`/0. A violation still wins over a global diagnostic, per the verdict table. All diagnostics are always listed. The illustrative report shows this: `VIOLATION` overall, with rule `db-in-repo` indeterminate.
- **Assumptions (S1)**:
  - **When listed.** `assumptions` is empty unless a `restrict_concept_to_boundary` rule is evaluated. Then it lists exactly `no_possible_target` and `trusted_stdlib`, sorted and unique, **even when a count is 0**.
  - **Counts.** Each exempted diagnostic stays in `diagnostics` with relevance `warning` and `exempted_by` naming its assumption. `exempted_count` must equal the number of diagnostics carrying that `exempted_by`.
  - **Attribution is based on provenance, never on the shape of an import path.** Go allows dotless module paths such as `app/internal/helper`. The evidence is `unit_origin`, which the analyzer copies from the owning unit's loader-derived `unit.origin`. It is required on every exempted diagnostic. A validator cross-checks it against the pinned standard-library inventory `stdlib-<go_version>.txt`, the output of `go list std` under the pinned profile (360 packages for go1.26.11):
    - `trusted_stdlib` requires an unknown-call kind, `unit_origin: stdlib`, and a `unit_id` in the inventory for the report's `build_profile.go_version`.
    - `no_possible_target` requires an `unresolved_interface_call` or `unresolved_dynamic_call` with a `site_id`, and `unit_origin` `module` or `dependency`. A standard-library zero-target site is attributed to `trusted_stdlib`.
    - Any diagnostic carrying `unit_origin` must agree with the inventory: `stdlib` iff the unit is in it. Shards are checked the same way: `unit.origin = stdlib` iff `unit.id` is in the inventory.
    - Negative fixtures `s1-dotless-module-trusted`, `s1-dotless-module-claims-stdlib`, `s1-trusted-without-provenance` and `s1-shard-module-unit-claims-stdlib` must be rejected.
- **Included and excluded scope**: the report lists included and excluded packages and files (plan section 7).

## Plan section 3 coverage

| Plan requirement | Where |
| --- | --- |
| Deterministic IDs: anonymous functions, generic instances, method wrappers, duplicate routes | Identifiers (C1–C3, route `duplicate_index`). Examples: `Register$1`, `Map[int, string]`, `List$bound`, pointer-wrapper `Ping`, `GET /items#0/#1`. |
| Deterministic ordering | Ordering table, enforced by `schemacheck`. |
| Nullable locations | `common#/$defs/nullableLocation`. Real example: `server.init` and its sites. |
| External (dependency) symbols | `external_symbols`, location roots `module` and `goroot`, `unit.origin`. |
| Shard ownership and referential integrity | Shard ownership section, enforced per shard by `schemacheck`. |
| Source file list, content hash, dependency hashes | `unit.files`, `unit.content_hash`, `unit.dep_hashes`, recomputed by `schemacheck`. |
| Function-entry metadata, recoverable `Entry` | `symbol.in_universe`, `kind`, `entry_hints`, `routes[].handler_sym`, `manifest.entry`. See Recovering Entry. |
| Global diagnostics | `manifest.global_diagnostics`, `scope: global`; report relevance `global` for run-level failures (B1). |
| Affected-rule reporting | Report `diagnostics[].relevance` and `affects_rules`, `rules[].diagnostic_ids`. |
| Included and excluded packages and files | Manifest `excluded_packages`, `unit.excluded_files`, report `packages`. |
