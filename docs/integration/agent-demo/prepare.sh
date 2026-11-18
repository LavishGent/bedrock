#!/usr/bin/env bash
# By-hand agent demo (plan section 8, step 5), run by a human.
#
# Copies demo/violating and the repository policy into a fresh temporary
# directory, strips the "// VIOLATION ..." marker comments (so the agent only
# has the report to go on), puts the copy under git so you can review the
# agent's diff, runs bedrock check, and writes the JSON report to a file you
# paste into the agent prompt. It never runs an agent.
#
# Usage: docs/integration/agent-demo/prepare.sh [OUT_DIR]
# Env:   BEDROCK  path to a bedrock binary (default: built from this checkout)
set -euo pipefail

repo=$(cd "$(dirname "$0")/../../.." && pwd)
out=${1:-$(mktemp -d "${TMPDIR:-/tmp}/bedrock-agent-demo.XXXXXX")}
mkdir -p "$out"
work="$out/demo"
if [ -e "$work" ]; then
	echo "prepare.sh: $work already exists; choose an empty OUT_DIR" >&2
	exit 2
fi

bin=${BEDROCK:-}
if [ -z "$bin" ]; then
	bin="$out/bedrock"
	(cd "$repo" && go build -o "$bin" ./cmd/bedrock)
fi

cp -R "$repo/demo/violating" "$work"
mkdir -p "$work/.bedrock"
cp "$repo/.bedrock/policy.toml" "$work/.bedrock/policy.toml"

# Strip the marker comments only; code and line numbers stay unchanged.
find "$work" -name '*.go' -type f -exec perl -pi -e 's{[ \t]*// VIOLATION\b.*$}{}' {} +
if grep -rn 'VIOLATION' --include='*.go' "$work" >/dev/null; then
	echo "prepare.sh: VIOLATION markers remain in $work" >&2
	exit 2
fi

(
	cd "$work"
	git init -q
	git add -A
	git -c user.name=bedrock-demo -c user.email=demo@example.invalid commit -q -m "demo baseline (violating, markers stripped)"
)

set +e
(cd "$work" && "$bin" check --policy .bedrock/policy.toml --out "$out/report.json" .)
code=$?
set -e

echo "demo copy:   $work"
echo "report:      $out/report.json (bedrock exit $code; 0 PASS, 1 VIOLATION, 2 INDETERMINATE = uncertainty, not a pass)"
echo "bedrock:     $bin"
if command -v jq >/dev/null 2>&1; then
	jq -r '.violations[] | "  \(.rule_id)  \(.location.file):\(.location.line)"' "$out/report.json"
fi
echo "Next: follow docs/integration/agent-demo/README.md."
