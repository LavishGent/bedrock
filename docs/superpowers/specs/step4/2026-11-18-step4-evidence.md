# Step 4 evidence: call facts and restrict_concept_to_boundary

**Date:** 2026-11-18
**Plan:** `../2026-11-17-bedrock-poc-plan.md`, sections 4, 7, 8 (step 4) and 10
**Branch:** `LavishGent/poc-step4`, cut from `1b4dc73` (step 3 known limits) on `LavishGent/poc-step3`
**Code commit:** `9a70d44`

## What was built

- **Call facts** (`internal/analyze/callgraph.go`). The SSA program covers every analyzed package, built with `ssa.InstantiateGenerics`. `cha.CallGraph` seeds `vta.CallGraph` over `ssautil.AllFunctions`, which is the pinned universe `all_functions_transitive_closure_v1`. The universe must be closed under call targets: a target outside it is a global `analyzer_crash`, which never happened.
  - Each shard gets call sites (static, invoke or dynamic, with VTA targets) for the symbols it owns.
  - It also gets the universe symbols that types do not declare: closures, instances, wrappers and package initializers.
  - Unknown calls become diagnostics:
    - `unresolved_interface_call` / `unresolved_dynamic_call` for a non-static site with **zero** targets only;
    - `bodyless_function` for a site with a bodyless target;
    - `reflection_call` for calls to `reflect.Value.Call/CallSlice/Method/MethodByName` or `reflect.MakeFunc`;
    - `unsafe_pointer`, one per function that converts to or from `unsafe.Pointer`.
- **Symbol IDs.** Before step 4 these were go/ssa's strings. Real runs showed two problems, both now fixed:
  - **Collisions:** 98 on the demo without instantiation and 70 with it.
    - C2 merges equivalent wrapper copies (for example `(crypto.Hash).New$bound` ×21), with union targets and OR-ed `address_taken`.
    - C1 qualifies type parameters as `Name@<owner ID>`, for example `cmp.Less[E@slices.pdqsortOrdered]`.
    - Result: zero collisions on every run here, so C3 never fired.
  - **Unstable spelling.** go/ssa names an instance the way it first met its type arguments. `slices.SortFunc[[]os.DirEntry, os.DirEntry]` and `...[[]io/fs.DirEntry, io/fs.DirEntry]` are the same function, and packages build in parallel, so shards differed between runs. Names that depend on instantiated or literal types are now written with aliases resolved and full package paths. Plain declared functions and methods keep go/ssa's string, so `(*database/sql.DB).QueryContext` and every policy-bindable ID are unchanged.
- **Wrapper origins.** A wrapper's `origin_id` is the method it calls. When that is an interface method, the analyzer adds an abstract method symbol to the interface's unit, with `has_body: false` and `in_universe: false`. Examples: the bound or thunk of `(net/http.Handler).ServeHTTP`, and promotion through an embedded interface. The predeclared `(error).Error` has no package, so the smallest unit ID that refers to it owns it.
- **Manifest.** `--facts-dir DIR` now also writes `DIR/analysis.json`. It contains:
  - units with canonical shard hashes;
  - universe counts;
  - the Entry count and digest;
  - global diagnostics;
  - the limits actually in force.

  Its `status` is `load_error` when a package fails to load; call facts are then not built at all. The report's `inputs.manifest_sha256` is the manifest's canonical hash and is no longer null.
- **Evaluator** (`internal/check/boundary.go`). It reads only shards and the manifest.
  - **Merge.** The shards are merged with three of the merge checks: `symbol_id_collision`, `unresolved_external_symbol` and `entry_mismatch`.
  - **Entry.** Entry is recomputed once from the merged graph: in-universe function symbols with no incoming edge, plus route handlers.
  - **`reachable_from`** follows plan decision 4:
    - a caller in `In(B)` passes;
    - otherwise it fails if a multi-source BFS from Entry with `In(B)` removed reaches it;
    - it also fails if no Entry reaches it at all (for example a cycle no Entry enters);
    - otherwise it passes.
  - **Concept set.** The `go-stdlib-sql` pack binds the 26 symbols of policy contract 5.1, restricted to universe functions and closed over wrappers and instances (contract 5). Calls made inside such a wrapper are not reported again.
  - **Evidence.** A violation's evidence runs `entry` → `call`… → `call` (the bound callee) → `concept_binding`.
- **Decision A relevance** (plan section 7, policy contract 7), unchanged in substance:
  - `trusted_stdlib` exempts every unknown call owned by a standard-library unit (loader provenance plus the pinned inventory).
  - `no_possible_target` exempts zero-target unresolved sites in module or dependency code.
  - Both assumptions are listed with counts. Each exempted diagnostic carries `exempted_by` and stays a warning.
  - Any other unknown call is relevant (`indeterminate`) with reason `unit_in_scope` when its unit is governed. It gets `reachable_from_governed_entry` when an Entry owned by a governed unit reaches its symbol. Otherwise it is a warning.
  - With no call facts (load error), every load failure affects each `restrict_concept_to_boundary` rule.
- **`rule_not_evaluated`.** `restrict_concept_to_boundary` is now evaluated, so it no longer gets this diagnostic. The mechanism itself is unchanged; this build simply has no unevaluated kind left.
- **Schema.** Still 0.4: no JSON shape changed. The README records the ID and ownership clarifications above. `spike/schemacheck` gains a **run-level cross-check**: given a manifest with its shards (and reports), it checks:
  - one shard per unit, with a matching canonical hash and profile digest;
  - unique symbols;
  - resolved external symbols;
  - the Entry recovered from shards against `manifest.entry`;
  - `inputs.manifest_sha256` and `shard_count`.

## Results

From the repository root, with go1.26.11. Each line is followed by the schemacheck run check over every shard, the manifest and the report:

```
demo/clean: exit 0 PASS | rules [('auth-private-import', 'PASS'), ('db-in-repo', 'PASS'), ('routes-require-auth', 'PASS')] | violations [] | relevant [] | assumptions [('no_possible_target', 3), ('trusted_stdlib', 4438)] | manifest complete, 17205 functions (549 bodyless), Entry 4637
ok   run (manifest + 198 shards, 1 reports)
demo/violating: exit 1 VIOLATION | rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'VIOLATION'), ('routes-require-auth', 'VIOLATION')] | violations [('auth-private-import', 'internal/billing/billing.go:9'), ('db-in-repo', 'internal/handler/handler.go:95'), ('routes-require-auth', 'internal/server/server.go:18')] | relevant [] | assumptions [('no_possible_target', 3), ('trusted_stdlib', 4438)] | manifest complete, 17205 functions (549 bodyless), Entry 4638
ok   run (manifest + 198 shards, 1 reports)
testdata/broken: exit 2 INDETERMINATE | rules [('no-a-root-import', 'INDETERMINATE')] | violations [] | relevant [('package_load_error', 'example.com/broken/b')] | assumptions [] | manifest load_error, 0 functions (0 bodyless), Entry 0
ok   run (manifest + 3 shards, 1 reports)
testdata/regress: exit 1 VIOLATION | rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] | violations [('no-forbidden-import', 'lined/lined.go:7'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5')] | relevant [] | assumptions [] | manifest complete, 5 functions (0 bodyless), Entry 3
ok   run (manifest + 4 shards, 1 reports)
testdata/regress-missing: exit 2 INDETERMINATE | rules [('no-user-import', 'INDETERMINATE')] | violations [] | relevant [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')] | assumptions [] | manifest load_error, 0 functions (0 bodyless), Entry 0
ok   run (manifest + 2 shards, 1 reports)
testdata/regress-underscore: exit 1 VIOLATION | rules [('no-helper-import', 'VIOLATION')] | violations [('no-helper-import', 'app.go:6')] | relevant [] | assumptions [] | manifest complete, 2 functions (0 bodyless), Entry 1
ok   run (manifest + 2 shards, 1 reports)
testdata/routerrules: exit 1 VIOLATION | rules [('needs-audit', 'VIOLATION'), ('needs-auth', 'VIOLATION')] | violations [('needs-audit', 'server/server.go:25'), ('needs-audit', 'server/server.go:32'), ('needs-auth', 'server/server.go:32')] | relevant [] | assumptions [] | manifest complete, 16640 functions (549 bodyless), Entry 4535
ok   run (manifest + 188 shards, 1 reports)
testdata/routerinit: exit 1 VIOLATION | rules [('needs-auth', 'VIOLATION')] | violations [('needs-auth', 'routerinit.go:19')] | relevant [('unsupported_router_form', 'example.com/routerinit'), ('unsupported_router_form', 'example.com/routerinit'), ('unsupported_router_form', 'example.com/routerinit')] | assumptions [] | manifest complete, 16252 functions (549 bodyless), Entry 4452
ok   run (manifest + 185 shards, 1 reports)
```

- **demo/clean exits 0 under decision A.** Every remaining unknown call is exempt:
  - the 4,438 standard-library diagnostics (`trusted_stdlib`);
  - three zero-target dynamic calls in chi (`no_possible_target`).

  Module code has no reflection, `unsafe` or bodyless call.
- **demo/violating** has exactly the three seeded violations. The `db-in-repo` evidence is:
  - `entry` `(*example.com/demo/internal/handler.Handlers).Export` (a route handler);
  - `call` to `(*database/sql.DB).QueryContext` at `internal/handler/handler.go:95:32`;
  - `concept_binding` to `database.query`.
- **broken and regress are unchanged.** For broken, regress, regress-missing, regress-underscore, routerinit and routerrules, the step 4 report equals the step 3 report (a binary built from `1b4dc73`) after dropping `inputs` and the new warning-only unknown-call diagnostics. Status, exit code, rules, violations and every other diagnostic are identical. No fixture except the demo has a `restrict_concept_to_boundary` rule.

## Fixture matrix (plan section 10, `testdata/step4`)

There is one module per case. The policy restricts `database.query` to the module's `repo/**`, with scope the whole module unless noted. Expected violations are the source lines marked `// FAIL`. `cmd/bedrock/step4_test.go` asserts the exit code, the exact violation lines, the relevant diagnostics with their reasons, and the presence of a `no_possible_target` exemption. It also runs schemacheck on the whole run.

```
testdata/step4/sharedhelper:    exit 1 | violations util/util.go:12                    (Shared is reached from handler; OnlyFromRepo, reached only via repo, passes)
testdata/step4/cycles:          exit 1 | violations cyc/cyc.go:22                      (A/B cycle entered only via repo passes; X/Y cycle no Entry reaches fails) | no_possible_target 1 (Call's f() in module code)
testdata/step4/exportedhelper:  exit 1 | violations helper/helper.go:13                (exported Exec called only from repo is not an Entry: passes; Unused is an Entry: fails)
testdata/step4/closure:         exit 1 | violations handler/handler.go:12              (closure caller in handler fails; closure in repo passes)
testdata/step4/iface:           exit 1 | violations handler/handler.go:16              (interface dispatch: direct.Count fails, (*repo.Store).Count passes)
testdata/step4/cexfail:         exit 1 | violations mid/mid.go:7                       (Codex counterexample: B={b}, b->c, u->c, c->sql; Entry={b,u}; c fails)
testdata/step4/cexpass:         exit 0 | violations none                               (... plus b->u: Entry={b}; c passes, the documented non-monotonicity)
testdata/step4/escape:          exit 1 | violations handler/handler.go:13, :16         (escaped (*sql.Stmt).Query and (*sql.Conn).QueryContext outside repo)
testdata/step4/reflection:      exit 2 | relevant reflection_call/unit_in_scope
testdata/step4/unsafeptr:       exit 2 | relevant unsafe_pointer/unit_in_scope
testdata/step4/bodyless:        exit 2 | relevant bodyless_function/unit_in_scope     (go:linkname runtime.nanotime, no body)
testdata/step4/helperuncertain: exit 2 | relevant reflection_call/reachable_from_governed_entry (scope = handler only; the reflective call sits in out-of-scope ext, reached from handler.Serve; ext.Unreached's unsafe is a warning)
testdata/step4/mixed:           exit 1 | violations handler/handler.go:11 | relevant reflection_call/unit_in_scope (1+2 precedence: violation wins, the indeterminate diagnostic is listed)
testdata/step4/emptybinding:    exit 2 | relevant empty_match/policy_config            (concept symbol example.com/s4/emptybinding/repo.Missing binds no universe function)
```

`internal/check/boundary_test.go` checks `reachable_from` on synthetic shards:

- Codex's counterexample in both forms; the evidence starts at Entry `u`.
- Membership in B passes; an unreachable cycle fails.
- The merge checks (`entry_mismatch`, `unresolved_external_symbol`, `symbol_id_collision`) fire on tampered shards.

`spike/schemacheck` `TestCrossCheck` rejects a tampered Entry digest, shard hash and report manifest hash.

**Plan section 10 "unsupported dynamic call gives 2".** Under decision A, a dynamic or interface call with no possible target is exempt, and one with targets is resolved. The dynamic call that still gives 2 is reflection (`reflection`, `helperuncertain`, `mixed`), together with `unsafe` and bodyless functions in module code.

## Timing and memory (limits 120 s and 4096 MiB)

These were measured on an Apple M5 Pro (18 cores, 48 GiB) with go1.26.11 darwin/arm64 and the pinned linux/amd64 load profile. The child is the supervised `__analyze` process: load, SSA, CHA, VTA, evaluation and payload. The CLI figure includes the supervisor, payload decoding and writing ~41 MB of shards.

| Run | Child wall | Child peak RSS | CLI wall |
| --- | --- | --- | --- |
| demo/clean ×3 | 1.57–1.60 s | 925–961 MiB | 1.86–2.05 s |
| demo/violating ×3 | 1.56–1.66 s | 940–973 MiB | 1.88–1.94 s |
| step 3 child (`1b4dc73`), demo/clean ×2, for comparison | 0.62–0.64 s | 424–430 MiB | |

That is 1.3% of the time limit and 24% of the memory limit. In-process, `analyze.Load` on demo/violating takes 1.16 s. The manifest's canonical shard hashing takes 0.30 s. The universe is 17,205 functions (549 bodyless), with 56,792 call sites.

## Determinism

- **CLI.** demo/violating was run 20 times consecutively. Every report, all 198 shards and the manifest are byte-identical.
- **Tests.** `TestDeterministic` (report, shards and manifest) passed six times in a row.
- **Found and fixed during the step.** C1 owners were taken from closures of generic functions in map order. Instance names also depended on the alias spelling go/ssa met first. Both are described under Symbol IDs above.

## Verification

- **Root.** `go build ./...`, `go vet ./...` and `go test -count=1 -timeout 30m ./...` all pass. `cmd/bedrock` took 462 s. The only skips are the three `TestKnownLimits` subtests for the documented known limits (L1 ×2, L2).
- **Demos and routerfixtures.** `demo/clean`, `demo/violating` and `testdata/routerfixtures` pass `go build ./...`, `go vet ./...` and `go test ./...`. Every `testdata/step4` module builds under the pinned profile and is gofmt-clean.
- **Diff hygiene.** `git diff --check` is clean.

## Known costs and limits of this step

- **Test time.** `cmd/bedrock` now takes about 7.5 minutes instead of about 2.8. Every CLI run builds the whole-universe call graph (~1 s more per run, many runs in parallel). This is close to `go test`'s default 10-minute per-package timeout. Run it with `-timeout 30m`, as above.
- **Facts size.** A demo run writes ~41 MB of shards (`runtime` alone is 9.7 MB), and the report lists ~4,441 warning diagnostics (2.8 MB), almost all exempted standard-library unknown calls. This is as the contract requires: every exempted diagnostic stays listed.
- **All or nothing.** Call facts are built only when every package loads and type-checks. Otherwise the manifest says `load_error`, and each `restrict_concept_to_boundary` rule is `INDETERMINATE` through the load-failure diagnostics. Those carry reason `unit_in_scope`, or `reachable_from_governed_entry` for out-of-scope failures, conservatively, since no graph exists to test reachability.
- **Merge check 1.** The shard hash against the manifest is computed and enforced by the schemacheck run check. The in-process evaluator does not re-hash the shards it was just handed by the same process.
- **Narrow `unsafe` and reflection detection.** `unsafe` is detected through `unsafe.Pointer` conversions. Reflection is detected through the five entry points above. Standard-library reflection (for example `text/template` calling methods) is trusted. A module function reached only that way has no incoming edge, so it is an Entry itself (policy contract 7.4).
- **Package initializers.** Call sites in a synthesized package initializer `pkg.init` have `loc: null` (schema README, Locations). A bound call in a package-level variable initializer would therefore be reported without a location.
- **`require_middleware`** is unchanged and still advisory against adversarial code (known limits L1/L2, `../routers/README.md`).
