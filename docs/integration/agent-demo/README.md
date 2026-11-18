# By-hand agent demo

Plan section 8, step 5, and decision 1: a human runs a coding agent against the demo service and checks whether the bedrock report is enough for the agent to fix every violation. Nothing here runs an agent; you do.

## Prepare

From the repository root, with go1.26.11 (to build bedrock), `git`, `perl` and `jq` on `PATH`:

```sh
docs/integration/agent-demo/prepare.sh                 # new temporary directory
docs/integration/agent-demo/prepare.sh demo-run        # or a new, relative or absolute OUT_DIR
BEDROCK=path/to/bedrock docs/integration/agent-demo/prepare.sh   # use an existing binary
```

All paths are resolved to absolute paths before the script changes directory. The script:

1. builds bedrock into `OUT_DIR/bedrock`, unless `BEDROCK` is given;
2. copies `demo/violating` to `OUT_DIR/demo`, plus the repository policy as `.bedrock/policy.toml`;
3. strips the `// VIOLATION ...` marker comments, without changing any line numbers;
4. commits the copy as a git baseline;
5. runs bedrock and writes `OUT_DIR/report.json`;
6. **verifies the baseline**: bedrock must exit 1, the report must be readable JSON with `exit_code` 1, and its violations must be exactly these:
   - `auth-private-import`, `internal/billing/billing.go:9`;
   - `db-in-repo`, `internal/handler/handler.go:95`;
   - `routes-require-auth`, `internal/server/server.go:18`.
7. writes `OUT_DIR/prompt.txt`: the prompt below, with the absolute checker command filled in, followed by the report.

**Exit status of `prepare.sh`.** 0 means *the violating baseline is ready*: the checker returned 1, as expected. It does not mean the checker passed. Any other analyzer exit (0, 2 or other), a missing or malformed report, or different violations make `prepare.sh` exit 2 with an error naming the problem.

On success it prints the absolute paths and two ready-to-use lines:

- `checker: export BEDROCK="/abs/path/bedrock"`;
- `re-check: cd "/abs/OUT_DIR/demo" && "/abs/path/bedrock" check --policy .bedrock/policy.toml --out "/abs/OUT_DIR/report.json" .`

## Prompt

Open the agent in `OUT_DIR/demo` and paste the whole of `OUT_DIR/prompt.txt`. It reads:

```text
This Go service is checked by bedrock, an architecture-rule checker. Below is its JSON report.
Fix every violation and every INDETERMINATE diagnostic so that this command, run in this
directory (/abs/OUT_DIR/demo), exits 0:

    "/abs/path/bedrock" check --policy .bedrock/policy.toml --out "/abs/OUT_DIR/report.json" .

Rules:
- Do not edit anything under .bedrock/ and do not weaken or delete tests.
- Exit 1 means a violation; exit 2 means bedrock is uncertain, which is not a pass.
- After each change, run the command above and `go test ./...`, and keep going until bedrock exits 0 and the tests pass.

<the baseline report.json>
```

If the agent cannot run the checker itself, run the printed `re-check` line for it and paste the new `report.json` back. The agent hook examples in `../claude-code` and `../cursor` can automate that loop as repair aids. The judgement of the demo is the human re-check below, not the hooks.

## Checklist (for the human)

- [ ] `prepare.sh` exited 0 and printed "Baseline ready" with the three violations above, and `grep -rn VIOLATION` over the copy finds nothing.
- [ ] Run the printed `re-check` line yourself after the agent finishes, and repeat the loop until it **exits 0**. Exit 2 (INDETERMINATE) is uncertainty, not success.
- [ ] `git diff --stat -- .bedrock` in the copy is empty: the agent did not touch the policy.
- [ ] `go test ./...` passes in the copy, including `internal/auth` and `internal/server`. The auth behavior tests reject a no-op `RequireAuth`, since bedrock checks only that the middleware is attached, not what it does.
- [ ] Review `git diff` for each fix:
  - the query moved into `internal/repo`;
  - the admin route is registered under the authenticated group;
  - billing uses the public auth API instead of `auth/private`.
- [ ] Note how many rounds the agent needed, and whether any report message was unclear. That is the demo's result (plan decision 1).
