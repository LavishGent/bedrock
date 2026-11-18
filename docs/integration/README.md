# Integration examples

Examples only: none of these is enabled in this repository, and none adds a component. They all run

```sh
bedrock check --policy .bedrock/policy.toml <module>
```

and feed the JSON report back to the agent or the developer.

| Exit | Status | Meaning | Hooks and CI |
| --- | --- | --- | --- |
| 0 | `PASS` | Every rule passed. | Allow. |
| 1 | `VIOLATION` | At least one rule was violated. | Block. |
| 2 | `INDETERMINATE` | Bedrock could not decide: a load failure, an unsupported construct, reflection, a policy problem, or an analyzer crash, timeout or memory limit. **Uncertainty, not a pass.** | Block. |

| Path | What it is |
| --- | --- |
| `hooks/bedrock-check.sh` | Shared runner: writes the report to `.bedrock-report.json` (add it to `.gitignore`), prints violations and relevant diagnostics to stderr, and exits with bedrock's code. Set `BEDROCK` to the pinned binary and `BEDROCK_MODULE` to the module directory. |
| `claude-code/settings.json`, `claude-code/bedrock-hook.sh` | Claude Code `Stop` hook. It maps bedrock exit 1 or 2 to hook exit 2, which blocks: the findings go to the agent and it keeps working. It skips when `stop_hook_active` is set, to avoid an endless loop. Merge `settings.json` into `.claude/settings.json`. |
| `cursor/hooks.json`, `cursor/bedrock-stop.sh` | Cursor `stop` hook. On exit 1 or 2 it returns a `followup_message` with the findings, at most `BEDROCK_MAX_LOOPS` (default 5) times. Copy `hooks.json` to `.cursor/hooks.json`. The Cursor hook API is newer than the others, so check field names against Cursor's current hooks documentation. |
| `git/pre-commit` | git pre-commit hook. It blocks the commit on exit 1 or 2. |
| `github-actions/bedrock.yml` | CI example implementing plan section 9: policy from the base branch, a pinned checksummed binary, pinned go1.26.11, upload of the report. Copy it to `.github/workflows/` and make it a required check. |
| `agent-demo/` | Kit for the by-hand agent demo: `prepare.sh`, the prompt and a checklist. |

These examples need `bash` and `jq`; the hooks degrade to the plain report path without `jq`.

**What a passing check does and does not establish.** `require_middleware` is **advisory against adversarial code**. A dependency or `//go:linkname` that deliberately mutates a module-created router can evade it: known limits L1 and L2 in `../superpowers/specs/routers/README.md`. Any enforcement claim for that rule must say so. `restrict_concept_to_boundary` trusts the pinned standard library (owner decision A); the report lists that assumption and how many calls it exempted.

Validation of these files: `shellcheck` 0.11.0 on every script, `jq empty` on both JSON files, and `actionlint` 1.7.12 (with shellcheck) plus Ruby's YAML parser on the workflow.
