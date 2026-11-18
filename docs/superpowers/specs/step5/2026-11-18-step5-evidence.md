# Step 5 evidence: hook and CI examples, agent demo kit

**Date:** 2026-11-18
**Plan:** `../2026-11-17-bedrock-poc-plan.md`, section 8, step 5 (and decisions 1 and 8, section 9)
**Branch:** `LavishGent/poc-step5`, cut from `b2264ee` on `LavishGent/poc-step4`

## What was added

These are examples only: no analyzer, evaluator or schema change, and nothing enabled under `.github/workflows/`.

- **`docs/integration/hooks/bedrock-check.sh`**: the shared runner. It:
  - runs `bedrock check --policy .bedrock/policy.toml <module>`;
  - writes the JSON report;
  - prints violations and relevant diagnostics to stderr;
  - exits with bedrock's code (0 PASS, 1 VIOLATION, 2 INDETERMINATE, which means uncertainty, not a pass).
- **Hooks:**
  - **Claude Code** `Stop` hook (`claude-code/settings.json`, `bedrock-hook.sh`): bedrock 1 or 2 becomes hook exit 2, which blocks with the findings on stderr. It has a `stop_hook_active` loop guard.
  - **Cursor** `stop` hook (`cursor/hooks.json`, `bedrock-stop.sh`): on bedrock 1 or 2 it returns a `followup_message`, at most `BEDROCK_MAX_LOOPS` times.
  - **git** `pre-commit`: blocks the commit on 1 or 2.
- **`docs/integration/github-actions/bedrock.yml`**: the section 9 example. It:
  - loads the policy from a sparse checkout of the base commit;
  - downloads a pinned release asset and verifies it with `sha256sum --check`;
  - pins go1.26.11 with `GOTOOLCHAIN=local` and `-mod=readonly`;
  - fails the job on 1 or 2 and uploads the report.

  The file also states:
  - the required-check setting;
  - `go test -timeout 30m` for bedrock's own suite;
  - that `require_middleware` is advisory against adversarial code (L1/L2).
- **`docs/integration/agent-demo/`**: `prepare.sh`, the prompt and a human checklist. `prepare.sh`:
  - copies `demo/violating` and the repository policy to a temp directory;
  - strips the `// VIOLATION` comments;
  - commits a git baseline;
  - runs bedrock and writes `report.json`.
- **`README.md`** at the repository root: purpose, build, usage, exit codes, supported scope, known limits and pointers to the specs.

## What was run

- **`docs/integration/agent-demo/prepare.sh /tmp/agentdemo`** (bedrock built from this checkout):
  - bedrock exit 1, `status` `VIOLATION`, exactly three violations: `auth-private-import internal/billing/billing.go:9`, `db-in-repo internal/handler/handler.go:95`, `routes-require-auth internal/server/server.go:18`;
  - `grep -rn VIOLATION` over the copy finds nothing;
  - the copy's git tree is clean after the baseline commit;
  - `go test ./...` passes in the copy.
- **The hooks**, with `BEDROCK_REPORT` outside the repository:

  | Target | Runner | Claude Code hook | Cursor hook | pre-commit |
  | --- | --- | --- | --- | --- |
  | `demo/clean` | exit 0 | exit 0 | `{}` | exit 0 |
  | `demo/violating` | exit 1 | exit 2, findings on stderr | `followup_message` with the findings | exit 1 |
  | `testdata/broken` (its own policy) | exit 2, printed as "INDETERMINATE: uncertainty, not a pass" | | follow-up with that finding | |

  With `stop_hook_active: true`, the Claude Code hook exits 0.
- **Validation:**
  - `shellcheck` 0.11.0 over all five scripts: clean.
  - `jq empty` over `claude-code/settings.json` and `cursor/hooks.json`: valid.
  - `actionlint` 1.7.12 with shellcheck over `github-actions/bedrock.yml`: clean, plus `ruby -ryaml` parse: valid.
  - `yamllint` is not installed here, so it was not run.
- **The Cursor hook API** (`version`, `stop`, `loop_count`, `followup_message`) is written from Cursor's hooks documentation as of this step and could not be exercised in Cursor here. The Claude Code `Stop` hook semantics (exit 2 blocks, `stop_hook_active`) are likewise untested inside Claude Code. Both are marked as examples.
- **Repository checks:**
  - `go build ./...` and `go vet ./...` at the root: pass.
  - `git diff --check`: clean.
  - No Go code changed in this step, so the test suite was not re-run.
