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
  - **Claude Code** `Stop` hook (`claude-code/settings.json`, `bedrock-hook.sh`): bedrock 1 or 2 becomes hook exit 2, which blocks with the findings on stderr. It runs the check on every stop (review fix B2).
  - **Cursor** `stop` hook (`cursor/hooks.json`, `bedrock-stop.sh`): on bedrock 1 or 2 it returns a `followup_message`. It runs the check on every stop, with `loop_limit: 5` and a visible failure when the limit is exhausted; it requires `jq` (review fixes B2, S3).
  - **git** `pre-commit`: blocks the commit on 1 or 2.
- **`docs/integration/github-actions/bedrock.yml`**: the section 9 example. It:
  - runs as `pull_request_target`, so the workflow and its pins come from the base branch (review fix B1);
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
  - verifies the baseline (review fix S1);
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

  At the time, the Claude Code hook exited 0 when given `stop_hook_active: true`. That guard was removed (review fix B2 below).
- **Validation:**
  - `shellcheck` 0.11.0 over all five scripts: clean.
  - `jq empty` over `claude-code/settings.json` and `cursor/hooks.json`: valid.
  - `actionlint` 1.7.12 with shellcheck over `github-actions/bedrock.yml`: clean, plus `ruby -ryaml` parse: valid.
  - `yamllint` is not installed here, so it was not run.
- **The Cursor hook API** (`version`, `stop`, `loop_count`, `followup_message`) is written from Cursor's hooks documentation as of this step and could not be exercised in Cursor here. The Claude Code `Stop` hook semantics (exit 2 blocks) are likewise untested inside Claude Code. Both are marked as examples.
- **Repository checks:**
  - `go build ./...` and `go vet ./...` at the root: pass.
  - `git diff --check`: clean.
  - No Go code changed in this step, so the test suite was not re-run.

## Review fixes (Codex review of ae102e9/2792e0a)

All changes are in `docs/integration`, `README.md`, the plan's section 9 and step 5 lines, and this file. No Go code changed.

**B1, CI trust anchor.** The example ran on `pull_request`, so a PR could edit the binary URL and checksum, the Go pin or the check command in its own copy of the workflow. It now runs on `pull_request_target`, so the workflow definition and every pin and policy selection come from the base branch.

- **Permissions:** `contents: read` only, no secrets, and `persist-credentials: false` on both checkouts.
- **PR head:** checked out only into `pr/`, by SHA, as analysis input. Nothing from it is executed: no scripts, tests, generators or local actions, and no PR-controlled string is interpolated into `run:`. The only program that reads it is the pinned, checksummed bedrock binary.
- **Build environment:** `GOTOOLCHAIN=local`, `GOFLAGS=-mod=readonly`, `GOWORK=off`, `GOENV=off` and `CGO_ENABLED=0`, set at job level.
- **Adoption:** the file states that it must live in a CODEOWNERS-protected `.github/workflows/`, and that the job `bedrock-check` must be a required status check.
- **Placeholders:** the release URL and checksum remain placeholders.
- **Plan section 9** now says the workflow definition runs from the base branch.

**B2, retry guards.** Both adapters now run the check on every stop.

- **Claude Code:** the `stop_hook_active` early exit is gone. A nonzero bedrock result, or a runner failure, is always hook exit 2 (blocking).
- **Cursor:** the `loop_count` early exit is gone, and `hooks.json` sets `loop_limit: 5` explicitly. When `loop_count` reaches the limit and the check still fails, the hook prints a visible "FAILED ... limit exhausted ... NOT passing" `followup_message` with the latest report path, writes the same text to stderr, and exits 1. It never prints `{}` for a failing check.
- **Docs:** the README files now describe each adapter exactly. They say the agent hooks are repair aids and that authoritative enforcement is the git pre-commit hook and CI.

**S1, baseline verification.** `prepare.sh` exits 2 with a named error unless all of these hold:

- bedrock exited 1;
- the report exists and is readable JSON with `exit_code` 1;
- the violations are exactly the three expected rule/location pairs.

Its own exit 0 is documented as "baseline ready", not "checker passed".

**S2, paths.** `OUT_DIR` and an explicit `BEDROCK` are resolved to absolute paths against the invocation directory before any `cd`. The script prints `export BEDROCK="..."` and an absolute `re-check` command, and writes `OUT_DIR/prompt.txt` with that command embedded, followed by the report. The demo README's prompt and fallback use it.

**S3, Cursor without `jq`.** `jq` is a hard prerequisite. Without it the hook still runs the check, prints a hand-escaped valid JSON `followup_message` saying the result could not be parsed, with the report path, and exits 2. `prepare.sh` also requires `jq`. The README states which scripts need it.

### Probes re-run

These used the review's stub checker, which records each invocation and exits with `STUB_CODE`, and wrote the report outside the repository:

```
claude stop_hook_active=true, stub 2   exit 2, checker ran, findings on stderr          (was: exit 0, checker not run)
cursor loop_count=5 (limit 5), stub 2  exit 1, checker ran, "FAILED ... exhausted" followup + stderr  (was: {} / 0, not run)
cursor loop_count=0, stub 2            exit 0, checker ran, followup_message with findings and report path
claude stub 0 / cursor loop_count=5 stub 0   exit 0 / {} exit 0, checker ran
cursor without jq (PATH=bash,cat,dirname,git,sed,sh), stub 2   exit 2, stdout is valid JSON (jq -e .followup_message succeeds) naming the report path   (was: exit 127, no stdout)
cursor without jq, stub 0             exit 2, same "could not be parsed" follow-up (jq is required)
prepare, analyzer exit 2               exit 2: "bedrock exited 2 on the violating baseline, want 1"   (was: exit 0)
prepare, analyzer exit 0 / 7           exit 2, same message with the actual exit
prepare, exit 1 + report exit_code 2   exit 2: "the report's exit_code is not 1"
prepare, exit 1 + no report            exit 2: "bedrock wrote no report"
prepare, exit 1 + malformed report     exit 2: "not readable JSON with a violations array"
prepare, exit 1 + one wrong violation  exit 2: "unexpected baseline violations", with got and want
prepare relative-out, BEDROCK=s5bin/bedrock (relative), real bedrock   exit 0, baseline ready   (was: report write failure)
prepare default (no args, builds bedrock)   exit 0, prints absolute export BEDROCK=... and re-check
```

**Relative-path run.** The baseline is exactly the three violations:

- `auth-private-import internal/billing/billing.go:9`;
- `db-in-repo internal/handler/handler.go:95`;
- `routes-require-auth internal/server/server.go:18`.

`grep -rn VIOLATION` over the copy finds nothing. `prompt.txt` names the absolute checker command. Re-running that command in the copy gives exit 1, `go test ./...` passes, and the git tree is clean.

**Validation:**
- `shellcheck` 0.11.0 and `bash -n` on all five scripts: clean.
- `jq empty` on `claude-code/settings.json` and `cursor/hooks.json`: valid.
- `actionlint` 1.7.12 with shellcheck, plus `ruby -ryaml`, on the workflow: clean.
- `git diff --check`: clean.
- Root `go build ./...` and `go vet ./...`: pass.
