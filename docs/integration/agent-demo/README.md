# By-hand agent demo

Plan section 8, step 5, and decision 1: a human runs a coding agent against the demo service and checks whether the bedrock report is enough for the agent to fix every violation. Nothing here runs an agent; you do.

## Prepare

From the repository root, with go1.26.11 on `PATH`:

```sh
docs/integration/agent-demo/prepare.sh            # or: prepare.sh /some/empty/dir
```

The script:

1. copies `demo/violating` to a fresh temporary directory, plus the repository policy as `.bedrock/policy.toml`;
2. strips the `// VIOLATION ...` marker comments, without changing any line numbers;
3. commits the copy as a git baseline;
4. runs `bedrock check --policy .bedrock/policy.toml .` and writes the JSON report to `report.json`, next to the copy.

Expect exit 1 and exactly three violations:

- `auth-private-import`, `internal/billing/billing.go:9`;
- `db-in-repo`, `internal/handler/handler.go:95`;
- `routes-require-auth`, `internal/server/server.go:18`.

## Prompt

Open the agent in the copied `demo` directory and paste the following, with the full contents of `report.json` after it:

```text
This Go service is checked by bedrock, an architecture-rule checker. Below is its JSON report.
Fix every violation and every INDETERMINATE diagnostic so that this command exits 0:

    bedrock check --policy .bedrock/policy.toml .

Rules:
- Do not edit anything under .bedrock/ and do not weaken or delete tests.
- Exit 1 means a violation; exit 2 means bedrock is uncertain, which is not a pass.
- After each change, run the command above and `go test ./...`, and keep going until bedrock exits 0 and the tests pass.

<paste report.json here>
```

If the agent cannot run `bedrock` itself, re-run it for the agent: `"$BEDROCK" check --policy .bedrock/policy.toml --out ../report.json .` from the copy, then paste the new report back. The hook examples in `../claude-code` and `../cursor` automate this loop.

## Checklist (for the human)

- [ ] `prepare.sh` printed exit 1 and the three violations above, and `grep -rn VIOLATION` over the copy finds nothing.
- [ ] Re-run bedrock after the agent finishes, and repeat the loop until it **exits 0**. Exit 2 (INDETERMINATE) is uncertainty, not success.
- [ ] `git diff --stat -- .bedrock` in the copy is empty: the agent did not touch the policy.
- [ ] `go test ./...` passes in the copy, including `internal/auth` and `internal/server`. The auth behavior tests reject a no-op `RequireAuth`, since bedrock checks only that the middleware is attached, not what it does.
- [ ] Review `git diff` for each fix:
  - the query moved into `internal/repo`;
  - the admin route is registered under the authenticated group;
  - billing uses the public auth API instead of `auth/private`.
- [ ] Note how many rounds the agent needed, and whether any report message was unclear. That is the demo's result (plan decision 1).
