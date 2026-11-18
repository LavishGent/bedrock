# Step 2 evidence: analyzer, policy parser, forbid_import, report

**Date:** 2026-11-17
**Plan:** `../2026-11-17-bedrock-poc-plan.md`, section 8, step 2

## What was built

The analyzer lives in the root module (`github.com/LavishGent/bedrock`).

| Path | Role |
| --- | --- |
| `cmd/bedrock` | The CLI: `bedrock check [--policy FILE] [--format json] [--out FILE] [--facts-dir DIR] [--timeout 120s] [--mem-limit-mib 4096] [MODULE_DIR]`. It writes the JSON report to stdout (or `--out`). `--facts-dir` also writes one unit shard per unit under `DIR/shards/`. Exit codes follow the verdict table. A watchdog turns a timeout, a memory-limit breach or a panic into a run-level (`global`) `analyzer_timeout`, `analyzer_memory_limit` or `analyzer_crash` report, with exit 2. |
| `internal/analyze` | Loads one module with `go/packages` under the pinned profile: go1.26.11 (checked with `go env GOVERSION`), linux/amd64, `CGO_ENABLED=0`, no tags, `GOFLAGS=-mod=readonly`, `GOWORK=off`, `GOTOOLCHAIN=local`. It rejects unsupported layouts with a global `unsupported_layout` error: no `go.mod` at the module root, a vendor directory, or a toolchain mismatch. A `go.work` file is reported as a warning and ignored. Each unit in the import closure becomes a schema 0.3 shard, built as follows: |
| | **Origin** comes from loader provenance: no owning module means `stdlib`; the main module means `module`; anything else means `dependency`. It is never inferred from the path, and `spike/schemacheck` cross-checks it against `schema/stdlib-go1.26.11.txt`. |
| | **Files and hashes:** compiled files with sha256, excluded files with reasons, the content hash, and dependency hashes. |
| | **Symbols** from `go/types`: functions, methods, types and vars, with `has_body` read from syntax and `address_taken` computed across all packages. A duplicate symbol ID is a global `symbol_id_collision`. |
| | **Imports** with locations, and **load diagnostics** (`package_load_error`, `parse_or_type_error`). |
| `internal/policy` | Parses and validates `.bedrock/policy.toml` against policy contract v1, failing closed. Missing file, syntax error, wrong type, unknown key at any depth (including a key of another rule kind), unknown kind, malformed or duplicate ID, undefined reference, bad glob or route pattern, unknown pack, version other than 1, and module mismatch all give one global diagnostic: `policy_missing`, `policy_invalid` or `policy_no_rules`. The report then has `rules: []` and exit code 2. |
| `internal/glob` | Import-path globs (contract section 3). `**` matches the package root. |
| `internal/check` | Evaluates `forbid_import` (contract section 6.1): scope projected to module units, `except_from`, self-import of the target allowed, direct imports only, `empty_match` for empty referenced boundaries (rule-specific, `policy_config`). Load errors in governed units affect the rule (`unit_in_scope`). It assembles the report with deterministic ordering and verdicts. |

**Not in this step:** `require_middleware` and `restrict_concept_to_boundary` are validated (fail closed) but **not evaluated**. Schema 0.3 has no way to mark a rule "not evaluated": `rules[].status` is `PASS`, `VIOLATION` or `INDETERMINATE`, and no diagnostic kind fits. So, rather than inventing a status:

- such rules are left out of `rules[]`, and the CLI names each one on stderr;
- a policy with no evaluable rule fails closed, with exit 2 and no report, instead of reporting `PASS` for unchecked rules.

This gap is reported to the coordinator. Call sites, routes and middleware stay empty in shards until steps 3 and 4.

No analysis manifest is emitted. Its `universe` and `entry` fields need the SSA universe and call graph of step 4, so `inputs.manifest_sha256` is `null`.

## Results

Commands run from the repository root with go1.26.11:

```
demo/clean: exit 0 PASS rules [('auth-private-import', 'PASS')] violations [] shards 197
demo/violating: exit 1 VIOLATION rules [('auth-private-import', 'VIOLATION')] violations [('auth-private-import', 'internal/billing/billing.go:9:2')] shards 197
testdata/broken: exit 2 INDETERMINATE violations 0 [('package_load_error', 'example.com/broken/b', 'b/b.go:6:8', 'indeterminate', [{'rule_id': 'no-a-root-import', 'reason': 'unit_in_scope'}])]
bedrock: rule "routes-require-auth" (require_middleware) is valid but NOT EVALUATED: this build evaluates forbid_import only (plan step 2)
bedrock: rule "db-in-repo" (restrict_concept_to_boundary) is valid but NOT EVALUATED: this build evaluates forbid_import only (plan step 2)
```

The commands were:

- `bedrock check --policy .bedrock/policy.toml demo/clean`
- `bedrock check --policy .bedrock/policy.toml demo/violating`
- `bedrock check testdata/broken`, which uses that fixture's own policy

The billing import is the only `forbid_import` violation in `demo/violating`. The other two seeded violations belong to rules this step does not evaluate.

Tests (`go test ./...` in the root module) cover the following:

- **`cmd/bedrock`:**
  - demo/clean gives 0, demo/violating gives 1 with exactly `internal/billing/billing.go:9:2`, and testdata/broken gives 2 with the `package_load_error` at `b/b.go:6:8` affecting `no-a-root-import`;
  - a missing, empty or malformed policy gives 2, `rules=[]` and one `global` diagnostic;
  - a policy with no evaluable rule gives 2 and no report;
  - two runs produce byte-identical reports and shards;
  - every emitted shard (197 per demo run) and report passes `go run ./spike/schemacheck` (schema plus semantic rules, including the stdlib provenance cross-check).
- **`internal/policy`:** 18 fail-closed cases, and the repository policy is accepted.
- **`internal/check`:** `except_from`, self-import of the target, direct-only imports, a third-party target, `empty_match` (including a scope that matches only stdlib), and verdict precedence (a violation plus a global diagnostic gives 1, with the other rule `INDETERMINATE`).
- **`internal/glob`:** matching and validation.
