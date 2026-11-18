# Example generators

These scripts regenerate the hand-written examples in `../examples/` and the negative fixtures in `../examples/invalid/`. Run them from the repository root:

```sh
python3 -I docs/superpowers/specs/schema/tools/gen.py docs/superpowers/specs/schema/examples   # illustrative and policy-failure examples
python3 -I docs/superpowers/specs/schema/tools/neg.py docs/superpowers/specs/schema/examples   # invalid/*.json and invalid/expect.json
go test ./spike/schemacheck
```

`hashes.json` holds the real file and content hashes of `spike/target`, captured by `hashes.py spike/target` under the pinned profile, for the spike shard example.

Real analyzer outputs are copied, not generated:

- `report.demo-clean-step2.json` comes from `bedrock check --policy .bedrock/policy.toml demo/clean`.
- `shard.unsafe-builtin.json` and `shard.regressmissing-absent-missing.json` come from `bedrock check --facts-dir` on `testdata/regress` and `testdata/regress-missing`.
