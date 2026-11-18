# Analyzer regression fixtures (step 2 review)

`testdata/regress` (module `example.com/regress`, policy in `.bedrock/policy.toml`):

| Package | Expected |
| --- | --- |
| `unsafeuse` | Imports `unsafe`, a builtin unit with no compiled files. Rule `no-unsafe` gives a violation at `unsafeuse/unsafeuse.go:5:8`. Before the fix, the `unsafe` unit was dropped and the run passed. |
| `lined` | Imports `forbidden` after `//line /outside/the/repo/generated.go:1`. Rule `no-forbidden-import` gives a violation at the physical location `lined/lined.go:7:8`. Before the fix, the import was dropped because its adjusted position was outside the repository. |

`bedrock check testdata/regress` exits 1 with exactly these two violations.

`testdata/regress-missing` (module `example.com/regressmissing`): `user` imports the in-module package `absent`, which does not exist. `absent` is a governed `module` unit with `source_status: missing` and a `package_load_error`. `bedrock check testdata/regress-missing` exits 2, with rule `no-user-import` `INDETERMINATE` (reason `unit_in_scope`).

`cmd/bedrock` tests assert all of this.

Excluded coverage (S4). These directories hold Go files but are not analyzed, and the report must list each one in `packages.excluded`:

| Directory | Reason |
| --- | --- |
| `constrained` | `build_constraint` (its file is also listed in `excluded_files`) |
| `onlytests` | `test_only` |
| `nested` | `outside_module` |
| `testdata` | `ignored_by_go` |
