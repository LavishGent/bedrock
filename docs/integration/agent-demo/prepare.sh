#!/usr/bin/env bash
# By-hand agent demo (plan section 8, step 5), run by a human.
#
# Copies demo/violating and the repository policy into an output directory,
# strips the "// VIOLATION ..." marker comments (so the agent only has the
# report to go on), puts the copy under git so you can review the agent's
# diff, runs bedrock check, VERIFIES the baseline, and writes:
#   OUT_DIR/demo         the copy the agent works in
#   OUT_DIR/report.json  the baseline report
#   OUT_DIR/prompt.txt   the prompt, with the absolute checker command, then the report
#   OUT_DIR/bedrock      the checker (unless BEDROCK is given)
# It never runs an agent.
#
# Exit status of THIS script: 0 means the violating baseline is ready, which
# requires bedrock to have exited 1 with exactly the three expected
# violations. It does not mean the checker passed. Any other analyzer exit
# (0, 2, ...), a missing or malformed report, or different violations make
# it exit 2 with an error.
#
# Usage: docs/integration/agent-demo/prepare.sh [OUT_DIR]   (default: a new temp dir)
# Env:   BEDROCK  path to a bedrock binary (default: built from this checkout)
# Needs: go1.26.11 (to build bedrock), git, perl, jq.
set -euo pipefail

die() {
	echo "prepare.sh: $*" >&2
	exit 2
}
abs() { # absolute path of $1, relative to the invocation directory
	case "$1" in
	/*) printf '%s\n' "$1" ;;
	*) printf '%s/%s\n' "$PWD" "$1" ;;
	esac
}

for tool in git perl jq; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
repo=$(cd "$(dirname "$0")/../../.." && pwd)

# Resolve every path before any cd.
if [ $# -ge 1 ]; then
	out=$(abs "$1")
	mkdir -p "$out"
else
	out=$(mktemp -d "${TMPDIR:-/tmp}/bedrock-agent-demo.XXXXXX")
fi
out=$(cd "$out" && pwd)
work="$out/demo"
[ ! -e "$work" ] || die "$work already exists; choose an empty OUT_DIR"

if [ -n "${BEDROCK:-}" ]; then
	bin=$(abs "$BEDROCK")
	[ -x "$bin" ] || die "BEDROCK=$BEDROCK is not an executable file"
else
	bin="$out/bedrock"
	(cd "$repo" && go build -o "$bin" ./cmd/bedrock) || die "building bedrock failed"
fi

cp -R "$repo/demo/violating" "$work"
mkdir -p "$work/.bedrock"
cp "$repo/.bedrock/policy.toml" "$work/.bedrock/policy.toml"

# Strip the marker comments only; code and line numbers stay unchanged.
find "$work" -name '*.go' -type f -exec perl -pi -e 's{[ \t]*// VIOLATION\b.*$}{}' {} +
if grep -rn 'VIOLATION' --include='*.go' "$work" >/dev/null; then
	die "VIOLATION markers remain in $work"
fi

(
	cd "$work"
	git init -q
	git add -A
	git -c user.name=bedrock-demo -c user.email=demo@example.invalid commit -q -m "demo baseline (violating, markers stripped)"
) || die "creating the git baseline failed"

report="$out/report.json"
check="\"$bin\" check --policy .bedrock/policy.toml --out \"$report\" ."
set +e
(cd "$work" && "$bin" check --policy .bedrock/policy.toml --out "$report" .)
code=$?
set -e

# Verify the baseline: exit 1, a readable report, exactly these violations.
expected='auth-private-import internal/billing/billing.go:9
db-in-repo internal/handler/handler.go:95
routes-require-auth internal/server/server.go:18'
[ "$code" -eq 1 ] || die "bedrock exited $code on the violating baseline, want 1 (0 PASS, 2 INDETERMINATE = uncertainty). Report: $report"
[ -s "$report" ] || die "bedrock wrote no report at $report"
got=$(jq -r '.violations[] | "\(.rule_id) \(.location.file):\(.location.line)"' "$report" 2>/dev/null | LC_ALL=C sort) ||
	die "the report at $report is not readable JSON with a violations array"
[ "$(jq -r '.exit_code' "$report")" = 1 ] || die "the report's exit_code is not 1: $report"
if [ "$got" != "$expected" ]; then
	die "unexpected baseline violations in $report:
$got
want:
$expected"
fi

{
	cat <<PROMPT
This Go service is checked by bedrock, an architecture-rule checker. Below is its JSON report.
Fix every violation and every INDETERMINATE diagnostic so that this command, run in this
directory ($work), exits 0:

    $check

Rules:
- Do not edit anything under .bedrock/ and do not weaken or delete tests.
- Exit 1 means a violation; exit 2 means bedrock is uncertain, which is not a pass.
- After each change, run the command above and \`go test ./...\`, and keep going until bedrock exits 0 and the tests pass.

PROMPT
	cat "$report"
} >"$out/prompt.txt"

cat <<DONE
Baseline ready (bedrock exited 1 with the three expected violations):
$got

demo copy:  $work
report:     $report
prompt:     $out/prompt.txt   (paste this into the agent, opened in the demo copy)
checker:    export BEDROCK="$bin"
re-check:   cd "$work" && $check
Next: follow docs/integration/agent-demo/README.md.
DONE
