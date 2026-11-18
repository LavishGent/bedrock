# Bedrock POC Plan (Go)

**Date:** 2026-11-17
**Status:** Draft for Review
**Parent spec:** `2026-11-15-bedrock-design.md`

## 1. Goal

Prove that Bedrock can report, for a Go service, which architectural rules failed and why, precisely enough that a coding agent (or the person driving one) can act on the report.

Bedrock reports. It does not run agents, retry, or score them. Agent integration is documentation and example hook config around the CLI.

## 2. Scope

In scope:

- Go only.
- Three rules, built in this order: internal-import boundary, route auth middleware, DB access via approved repository.
- Routers: `chi` and `net/http`.
- A mini Go service in `demo/` with one deliberate violation per rule.

Out of scope:

- TypeScript/JS analyzer and the frontend rule.
- Rust engine, DSL, Datalog IR, incremental cache.
- Agent harness, retry loops, benchmarking.
- Testing against a real open-source Go service (deferred).
- Baselines and waivers.

## 3. Architecture

A single Go program contains the analyzer and the rule checks. The analyzer writes facts in the parent spec's JSON shard format (`unit`, `symbol`, `import`, `call`, `route`, `route_middleware`, `diagnostic`), so it can be reused when a Rust engine is added. Rule evaluation is written directly in Go and is the throwaway part.

## 4. Decisions

| # | Decision |
| --- | --- |
| 1 | Success is shown by a demo: run a real agent against the mini service by hand and see whether the report is enough to fix the violations. No benchmark. |
| 2 | Single Go program; facts still emitted as spec-format JSON shards. |
| 3 | Rules 2, 3, 1 in that order; `chi` and `net/http`. |
| 4 | Call graph: VTA (seeded from CHA). Entry points are all exported functions, plus `main`, `init`, and registered route handlers. |
| 5 | Unit = Go package by import path, test files excluded. Symbol IDs use Go's fully qualified format, e.g. `example.com/app/internal/repo.(*Store).Save`. Locations are repo-relative. Each unit records its source file list and a content hash. |
| 6 | Config is one `.bedrock/policy.toml` using `[boundary.*]` and `[concept.*]` tables plus three fixed rule kinds: `forbid_import`, `require_middleware`, `restrict_concept_to_boundary`. Boundaries match import paths with `**` globs. A built-in default binds `database/sql` methods to `database.query`. |
| 7 | Exit codes: `0` PASS, `1` VIOLATION, `2` INDETERMINATE. If both violations and diagnostics exist, `1` wins; the report always lists all diagnostics. |
| 8 | Agent integration: `bedrock check` with JSON and compact text output, plus example Cursor / Claude Code hook config and CI usage in docs. |

## 5. INDETERMINATE triggers

Always `2`:

- A package in a rule's scope fails to load or type-check.
- Invalid `policy.toml`, or a boundary/concept that matches nothing.
- Analyzer crash or timeout.

`2` only when it touches a rule:

- Rule 1: unresolved dynamic call on a path that could reach `database.query`, including interface calls VTA cannot narrow.
- Rule 3: route registration whose path or handler cannot be resolved statically, in a package in scope.
- Rule 2: none expected once packages load.

Warning only: dynamic constructs in packages outside every rule's scope.

## 6. Report contents

Each violation includes: rule ID, location, message, evidence chain, and remediation hint. Each diagnostic includes: kind, location, reason, and which rule it affects. Diagnostics are grouped by kind so the `INDETERMINATE` rate is easy to read.

## 7. Mini service (`demo/`)

- A few `chi` routes and one plain `net/http` route.
- One repo package (approved boundary), one auth middleware, an internal auth subpackage, a billing package.
- A small test suite, so a "fix" that breaks behavior is caught by a human running the tests.
- Two states: `violating` (one violation per rule) and `clean` (passes).

## 8. Build order

1. Repo scaffold (Go module, `.bedrock/policy.toml`, `demo/` service in both states).
2. Analyzer: package loading, unit and symbol facts, import facts. Rule 2 and the report.
3. Route extraction for `chi` and `net/http`, and middleware facts. Rule 3.
4. SSA, CHA, VTA call facts. Rule 1 with `reachable_from`.
5. Hook examples and the by-hand agent demo.

## 9. Coordination

- `[w] ticket gate waived by operator (2026-11-17): no tickets for this POC.` Work is tracked by the build order in section 8 and this file. No `coordinator.json` exists, so defaults apply.

## 10. Open questions

- VTA run time with all exported functions as roots. Record it on the demo service and revisit if it is too slow.
- Share of violations that come only from non-handler exported roots. If high, switch to handlers plus `main` as roots.
- Untested on real-world code until a B target is added.
