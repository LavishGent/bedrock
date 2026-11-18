# Broken fixture (load failure)

This is its own module (`example.com/broken`), so ordinary loads of `demo/` never see it (plan section 10).

| Package | Role |
| --- | --- |
| `a` | Loads cleanly. It is the fixture rule's target, and nothing imports it. |
| `a/internal/secret` | Loads cleanly. |
| `b` | Imports `a/internal/secret`, which Go's `internal` rule forbids. `b` fails to load. |

## Fixture policy

`.bedrock/policy.toml` (policy contract v1) declares `module = "example.com/broken"`, which matches this module, so the policy is valid. Its one rule, `no-a-root-import`, has `scope = "all"` (`example.com/broken/**`), so `b` is governed.

The rule cannot fire: nothing imports `a`. That leaves the load failure as the only thing that can decide the verdict. Without a governing rule, the load failure would be irrelevant and the run could `PASS`. Without a valid policy, the run would stop at `policy_invalid` before analysis. Both would make the assertion test the wrong thing.

## Invocation and expected result

From the repository root, with the step 2 CLI. The policy defaults to `MODULE_DIR/.bedrock/policy.toml`. `cmd/bedrock` `TestBrokenFixture` makes these assertions:

```sh
bedrock check testdata/broken
```

The test must assert all of the following, and not only the exit code:

1. Exit code `2`, `status: "INDETERMINATE"`, `violations: []`.
2. `diagnostics` contains a diagnostic with:
   - `kind: "package_load_error"`, `scope: "unit"`, `unit_id: "example.com/broken/b"`;
   - `loc: {"root": "repo", "module": null, "file": "b/b.go", "line": 6, "column": 8}`;
   - a `message` containing `use of internal package example.com/broken/a/internal/secret not allowed`;
   - `relevance: "indeterminate"`, `affects_rules: [{"rule_id": "no-a-root-import", "reason": "unit_in_scope"}]`.
3. Rule `no-a-root-import` has `status: "INDETERMINATE"`, and its `diagnostic_ids` contains that diagnostic's ID.
4. No `policy_*` diagnostic is present, which shows the fixture policy itself was accepted.

The same failure from the Go toolchain (go1.26.11, pinned profile), run from this directory:

```
$ go build ./...
package example.com/broken/b
	b/b.go:6:8: use of internal package example.com/broken/a/internal/secret not allowed
$ echo $?
1
```
