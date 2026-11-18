# Bedrock (Go proof of concept)

Bedrock checks a Go service against architectural rules and reports which rules failed and why. The report is precise enough for a coding agent, or the person driving one, to act on. It reports only: it does not run agents, retry or score them.

This repository is the proof of concept described in `docs/superpowers/specs/2026-11-17-bedrock-poc-plan.md`. It covers one Go module, one pinned build profile, three rule kinds and a small router subset. It is not a general-purpose analyzer.

## Build

Requires the pinned toolchain **go1.26.11**. Analysis runs with `GOTOOLCHAIN=local` and refuses any other toolchain or standard library.

```sh
go build -o bedrock ./cmd/bedrock
go vet ./...
go test -timeout 30m ./...   # cmd/bedrock alone takes about 8.5 minutes
```

## Usage

```sh
bedrock check [--policy FILE] [--format json] [--out FILE] [--facts-dir DIR]
              [--timeout D] [--mem-limit-mib N] [MODULE_DIR]
```

| Flag | Meaning |
| --- | --- |
| `MODULE_DIR` | The module root to analyze (default `.`). |
| `--policy` | Policy file (default `MODULE_DIR/.bedrock/policy.toml`). |
| `--out` | Where to write the JSON report (default stdout). |
| `--facts-dir` | Also write the unit shards (`DIR/shards/`) and the analysis manifest (`DIR/analysis.json`). |
| `--timeout`, `--mem-limit-mib` | Analyzer limits (defaults 120 s and 4096 MiB). Crossing one is a run-level diagnostic. |

Example, on the demo service:

```sh
./bedrock check --policy .bedrock/policy.toml demo/clean       # exit 0
./bedrock check --policy .bedrock/policy.toml demo/violating   # exit 1, three violations
```

## Exit codes

| Exit | Status | Meaning |
| --- | --- | --- |
| 0 | `PASS` | Every rule passed. |
| 1 | `VIOLATION` | At least one violation. A violation wins over any uncertainty, and all diagnostics are still listed. |
| 2 | `INDETERMINATE` | Bedrock could not decide. Causes include a missing or invalid policy, a governed package that does not load, an unsupported router or middleware form, reflection, `unsafe` or bodyless calls the rules depend on, or an analyzer crash, timeout or memory limit. **Exit 2 means uncertainty, not a pass.** The git hook and CI treat it as a failure, exactly like exit 1. |

The JSON report is described by `docs/superpowers/specs/schema/` (schema 0.4). Each violation carries a location, an evidence chain and the policy's remediation. Each diagnostic carries its relevance (`global`, `indeterminate` or `warning`) and the rules it affects.

## Supported scope

- **Language and build.** Go only: one explicit module, test files excluded, and the pinned profile linux/amd64, `CGO_ENABLED=0`, no build tags, `-mod=readonly`, `GOWORK=off`.
- **Policy.** A single `.bedrock/policy.toml`, contract in `docs/superpowers/specs/policy/README.md`.
- **Rule kinds:**
  - `forbid_import`: package import restrictions.
  - `require_middleware`: every governed route has the approved middleware in its effective chain. It covers a bounded subset of `chi` v5 and `net/http`, in the supported router form (`docs/superpowers/specs/routers/README.md`).
  - `restrict_concept_to_boundary`: calls bound to a concept, for example the 26 `database/sql` query, exec and prepare methods of pack `go-stdlib-sql`, may only be reached through the approved boundary. It uses SSA with CHA-seeded VTA over the whole program and `reachable_from` from the graph-derived `Entry` set.
- **Integration examples** live in `docs/integration/`:
  - a git pre-commit hook and a `pull_request_target` GitHub Actions workflow, which are the **authoritative** gates;
  - Claude Code and Cursor hooks, which are repair aids that feed findings back to the agent;
  - the by-hand agent demo kit.

## Known limits

- **`require_middleware` is advisory against adversarial code.** It is sound for module code in the supported router form. A dependency or `//go:linkname` that deliberately mutates a module-created router can evade it: known limits L1 and L2 in `docs/superpowers/specs/routers/README.md`. Any enforcement claim must say so.
- **Structural check only.** Bedrock checks that approved middleware is attached, not what it does. Pair it with behavior tests, as the demo's auth tests do, and protect the middleware with CODEOWNERS.
- **Trusted standard library (owner decision A).** `restrict_concept_to_boundary` does not count unknown calls inside the pinned go1.26.11 standard library. It also does not count calls with no possible target. Every report that evaluates the rule lists both assumptions with their counts.
- **`Entry` comes from the call graph.** It is the set of functions with no incoming edge, plus route handlers. Over-approximation is therefore not monotonically safe (plan section 4).
- **No baselines or waivers.** Other drivers or ORMs are covered only when bound explicitly. Real-world accuracy beyond the declared fixtures and profile is untested.
- **Size and speed.** A demo run takes about 2 s and about 1 GiB, and writes about 41 MB of facts.

The trust boundary for CI is in plan section 9: policy from the base branch, and a pinned, checksummed bedrock binary that is never built from the pull request.

## Specs

- Plan: `docs/superpowers/specs/2026-11-17-bedrock-poc-plan.md` (parent design: `2026-11-15-bedrock-design.md`).
- Policy contract: `docs/superpowers/specs/policy/README.md`.
- Router support matrix, supported form and known limits: `docs/superpowers/specs/routers/README.md`.
- Fact and report schema: `docs/superpowers/specs/schema/README.md`.
- Evidence per step: `docs/superpowers/specs/step{0..4}/`.
