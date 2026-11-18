# Integration examples

Examples only: none of these is enabled in this repository, and none adds a component. They all run

```sh
bedrock check --policy .bedrock/policy.toml <module>
```

and feed the JSON report back to the agent or the developer.

| Exit | Status | Meaning | Effect |
| --- | --- | --- | --- |
| 0 | `PASS` | Every rule passed. | git hook and CI pass; agent hooks let the agent stop. |
| 1 | `VIOLATION` | At least one rule was violated. | git hook and CI fail; agent hooks feed the findings back. |
| 2 | `INDETERMINATE` | Bedrock could not decide: a load failure, an unsupported construct, reflection, a policy problem, or an analyzer crash, timeout or memory limit. **Uncertainty, not a pass.** | Same as exit 1. |

**Authoritative enforcement is the git pre-commit hook and CI.** The agent hooks are repair aids. They keep feeding findings back to the agent, but the agent host decides how long that loop runs.

| Path | What it does, exactly |
| --- | --- |
| `hooks/bedrock-check.sh` | Shared runner. It writes the report to `.bedrock-report.json` (add that to `.gitignore`), prints violations and relevant diagnostics to stderr (using `jq` when present; otherwise only the report path), and exits with bedrock's code. Set `BEDROCK` to the pinned binary and `BEDROCK_MODULE` to the module directory. |
| `claude-code/settings.json`, `claude-code/bedrock-hook.sh` | Claude Code `Stop` hook. **Every** stop runs the check; there is no early exit on `stop_hook_active`. bedrock 0 gives hook exit 0. bedrock 1, 2 or any runner failure gives hook exit 2, which blocks the stop and hands the findings to the agent. No bedrock-side cap exists: the agent cannot finish while the check fails, and a human interrupts a loop that does not converge. Merge `settings.json` into `.claude/settings.json`. |
| `cursor/hooks.json`, `cursor/bedrock-stop.sh` | Cursor `stop` hook. **Every** stop runs the check. bedrock 0 prints `{}`. bedrock 1 or 2 prints a `followup_message` with the findings and the report path, which Cursor submits to the agent. `hooks.json` sets `loop_limit: 5` explicitly; keep `BEDROCK_LOOP_LIMIT` equal to it. Once `loop_count` reaches that limit and the check still fails, the hook prints a visible "FAILED ... limit exhausted ... NOT passing" follow-up with the latest report path and exits 1, never an empty result. **`jq` is required.** Without it the hook still runs the check, prints a **constant** JSON follow-up saying the result could not be parsed, and exits 2. The exit code and exact report path go to stderr only, so the JSON stays valid whatever the path contains. Copy `hooks.json` to `.cursor/hooks.json`. The Cursor hook API is newer than the others, so check field names against Cursor's current hooks documentation. |
| `git/pre-commit` | git pre-commit hook. It blocks the commit (exit 1) when bedrock exits 1, 2 or fails to run. It checks the working tree, so stage or stash first. |
| `github-actions/bedrock.yml` | CI example implementing plan section 9 as a `pull_request_target` workflow. The workflow definition, the pinned binary URL and checksum, the go1.26.11 pin, the build environment and the policy selection all come from the base branch. The PR head is checked out only as analysis input and nothing from it is executed. The file must live in a CODEOWNERS-protected `.github/workflows/`, and the job `bedrock-check` must be a required status check. The release URL and checksum are placeholders until a bedrock release exists. |
| `agent-demo/` | Kit for the by-hand agent demo: `prepare.sh`, which verifies the baseline and writes a ready-to-paste `prompt.txt`, plus a checklist. Every path in a generated command (re-check, export, prompt) is a single-quoted POSIX shell literal, valid in sh, bash and zsh, so paths containing spaces, quotes, `$`, backticks or newlines are never executed. |
| `tests/probes.sh` | Failure-path probes for the scripts above, using a stub checker and a git shim. Everything runs in a temp dir. They cover: generated commands with hostile paths (no command substitution runs, the checker gets its literal arguments under bash, sh and zsh); the no-`jq` Cursor answer with a newline and tab in the path, parsed as JSON; and a git failure at each baseline step. |

The shared runner, the git hook and the Claude Code hook work without `jq` (with less detail); the Cursor hook and `prepare.sh` require it.

**What a passing check does and does not establish.** `require_middleware` is **advisory against adversarial code**. A dependency or `//go:linkname` that deliberately mutates a module-created router can evade it: known limits L1 and L2 in `../superpowers/specs/routers/README.md`. Any enforcement claim for that rule must say so. `restrict_concept_to_boundary` trusts the pinned standard library (owner decision A); the report lists that assumption and how many calls it exempted.

Validation of these files: `shellcheck` 0.11.0 and `bash -n` on every script, `jq empty` on both JSON files, `actionlint` 1.7.12 (with shellcheck) and Ruby's YAML parser on the workflow. The failure paths are exercised by `tests/probes.sh` (results in `../superpowers/specs/step5/2026-11-18-step5-evidence.md`).
