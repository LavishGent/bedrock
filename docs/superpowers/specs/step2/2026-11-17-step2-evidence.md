# Step 2 evidence: analyzer, policy parser, forbid_import, report

**Date:** 2026-11-17, revised 2026-11-18 after the Codex step 2 review. Owner decision: fail closed now; a release or CI gate is not a substitute.
**Plan:** `../2026-11-17-bedrock-poc-plan.md`, section 8, step 2

## What was built

The analyzer lives in the root module (`github.com/LavishGent/bedrock`).

| Path | Role |
| --- | --- |
| `cmd/bedrock` | The CLI (below). |
| `internal/analyze` | The pinned loader: environment control, provenance, units and shards. |
| `internal/policy` | Parses and validates the policy against contract v1, failing closed (below). |
| `internal/glob` | Import-path globs (contract section 3). `**` matches the package root. |
| `internal/check` | Evaluates `forbid_import`, decides relevance, assembles the report with deterministic ordering, and computes verdicts and exit codes. |

### `cmd/bedrock`

```
bedrock check [--policy FILE] [--format json] [--out FILE] [--facts-dir DIR] [--timeout D] [--mem-limit-mib N] [MODULE_DIR]
```

- **Supervised child process (S3).** Analysis runs in a child process of the same binary. The parent turns the child's failures into a run-level (`global`) report, exit 2, built from what the parent observed:
  - a panic in any goroutine, or a non-zero exit, becomes `analyzer_crash`, with the panic line taken from captured stderr;
  - passing the wall clock becomes `analyzer_timeout`, and the child is killed;
  - a memory breach becomes `analyzer_memory_limit`. The child's own watchdog exits with status 86, or the parent sees peak RSS from rusage above the limit.
- **Limits are validated:** `0 < timeout <= 1h` and `64 <= mem-limit-mib <= 1048576`.
- **A report is always written.**

### `internal/analyze`

- **Controlled environment (S1).** The loader uses only the caller's cache and proxy variables (`GOPATH`, `GOMODCACHE`, `GOCACHE`, `GOPROXY` and the like). It drops `GOROOT`, `GOFLAGS`, `GOEXPERIMENT`, `GOAMD64`, `CC` and every other `GO*` or `CGO_*` variable, and pins the rest: `GOOS=linux`, `GOARCH=amd64`, `GOAMD64=v1`, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, `GOWORK=off`, `GOTOOLCHAIN=local`, `GOENV=off`, `GOEXPERIMENT=`, `GOPACKAGESDRIVER=off`.
- **Toolchain check.** Each run verifies `go env GOVERSION = go1.26.11`. It also checks that live `go list std` output matches the embedded inventory, which a test keeps byte-identical to `schema/stdlib-go1.26.11.txt`.
- **Provenance (S1).** The origin of each unit is checked in the production loader:
  - `module` requires `Module.Main` with the module dir equal to the analyzed root, or a path inside the main module path;
  - `dependency` is any other module;
  - `stdlib` requires no module, an entry in the inventory, and a directory under GOROOT/src;
  - otherwise the unit is `unresolved`;
  - any contradiction is a global `unsupported_layout` error.
- **Every package is a unit (B2).** `unsafe` is a `builtin` unit. A package that cannot be loaded is a `missing` unit carrying its load error, and its origin is `module` if its path is inside the main module. Load errors are recorded before the source status is decided. Import edges are never dropped: a target that was not loaded becomes a missing unit.
- **Physical locations (B3).** Locations use `PositionFor(pos, false)`, so `//line` directives cannot move or drop evidence. An unmappable location gives `loc: null`, and the import is kept.
- **Excluded coverage (S4).** A walk of the module tree reports every directory with Go files that is not analyzed: `build_constraint`, `test_only`, `outside_module` (nested `go.mod`) or `ignored_by_go` (testdata, `.`, `_`). The non-analyzed files go into `excluded_files`. A walk error is a fatal `unsupported_layout`. A `vendor/` directory, a missing `go.mod` and a toolchain mismatch also fail closed.

### `internal/policy`

- **S2:** concept symbols must match the canonical symbol-ID grammar, whether or not a rule uses them. `???` is rejected.
- **N1:** `rule = []` gives `policy_no_rules`.

### Rules this build does not evaluate (B1, schema 0.4)

Every configured rule is listed. A rule of a kind this build does not evaluate (`require_middleware`, `restrict_concept_to_boundary`) gets a global `rule_not_evaluated` diagnostic: relevance `indeterminate`, reason `not_evaluated`. The rule is therefore `INDETERMINATE`, and the run exits 2 unless a real violation gives 1. So `demo/clean` exits 2 until steps 3 and 4 land, and `demo/violating` still exits 1.

No analysis manifest is emitted yet: its `universe` and `entry` fields need the step 4 call graph, so `inputs.manifest_sha256` is `null`.

## Results

From the repository root, with go1.26.11:

```
demo/clean: exit 2 INDETERMINATE rules [('auth-private-import', 'PASS'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'INDETERMINATE')] violations [] relevant diagnostics [('rule_not_evaluated', None), ('rule_not_evaluated', None)]
demo/violating: exit 1 VIOLATION rules [('auth-private-import', 'VIOLATION'), ('db-in-repo', 'INDETERMINATE'), ('routes-require-auth', 'INDETERMINATE')] violations [('auth-private-import', 'internal/billing/billing.go:9:2')] relevant diagnostics [('rule_not_evaluated', None), ('rule_not_evaluated', None)]
testdata/broken: exit 2 INDETERMINATE rules [('no-a-root-import', 'INDETERMINATE')] violations [] relevant diagnostics [('package_load_error', 'example.com/broken/b')]
testdata/regress: exit 1 VIOLATION rules [('no-forbidden-import', 'VIOLATION'), ('no-unsafe', 'VIOLATION')] violations [('no-forbidden-import', 'lined/lined.go:7:8'), ('no-unsafe', 'unsafeuse/unsafeuse.go:5:8')] relevant diagnostics []
testdata/regress-missing: exit 2 INDETERMINATE rules [('no-user-import', 'INDETERMINATE')] violations [] relevant diagnostics [('parse_or_type_error', 'example.com/regressmissing/user'), ('package_load_error', 'example.com/regressmissing/absent')]
two runs of demo/violating (report + 198 shards): byte-identical
```

The commands were:

- `bedrock check --policy .bedrock/policy.toml demo/clean`
- `bedrock check --policy .bedrock/policy.toml demo/violating`
- `bedrock check testdata/broken`
- `bedrock check testdata/regress`
- `bedrock check testdata/regress-missing`

Each fixture uses its own `.bedrock/policy.toml` unless `--policy` is given.

## Codex repros, now fixed

| Repro | Before | Now | Test |
| --- | --- | --- | --- |
| Direct `unsafe` import forbidden by a rule | PASS (`unsafe` unit dropped) | violation at `unsafeuse/unsafeuse.go:5:8` | `TestRegressUnsafeAndLineDirective` |
| Forbidden import after a `//line` directive pointing outside the repo | PASS (import dropped) | violation at the physical `lined/lined.go:7:8` | same |
| Governed package with a load error and no compiled files | could be dropped | `missing` module unit; `INDETERMINATE` with reason `unit_in_scope` | `TestRegressMissingGovernedPackage` |
| Malformed concept symbol `"???"` | accepted | `policy_invalid`, exit 2, `rules=[]` | `TestPolicyRepros`, `TestFailClosed`, `TestSymbolIDGrammar` |
| `rule = []` | `policy_invalid` | `policy_no_rules` | `TestPolicyRepros`, `TestFailClosed` |
| Every rule of an unsupported kind | no report, exit 2 | report with every rule `INDETERMINATE`, exit 2 | `TestAllRulesUnsupported` |

Other tests in `go test ./...`:

- `TestSupervisorFaults`: a goroutine panic, a hang (2 s timeout) and allocation past a 128 MiB limit each give a global report.
- `TestLimitValidation`.
- `TestHostileEnvironment`: a hostile `GOFLAGS`, `GOROOT`, `GOPACKAGESDRIVER`, `GOOS`, `GOWORK`, `GOTOOLCHAIN` and `CGO_ENABLED` leave the report byte-identical.
- `TestEnvIsControlled` and `TestInventoryMatchesDocs`.
- `TestDeterministic`.
- The demo, broken-fixture and policy-failure tests.

Every emitted shard and report is validated by `spike/schemacheck` (schema 0.4 plus semantic rules). `go test ./spike/schemacheck` accepts every example and rejects all 25 negatives for their recorded reasons.
