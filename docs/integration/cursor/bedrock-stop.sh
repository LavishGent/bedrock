#!/usr/bin/env bash
# Example only. Cursor "stop" hook adapter for bedrock (see ../README.md).
#
# It ALWAYS runs the check. On a non-pass it answers with a followup_message
# carrying the findings, which Cursor submits to the agent. Cursor stops
# auto-submitting follow-ups after the hook's loop_limit (set to 5 in
# hooks.json; keep BEDROCK_LOOP_LIMIT equal to it). When that cap is reached
# and the check still fails, the hook emits a visible FAILED message with the
# latest report path and exits 1: never an empty, success-shaped result.
#
# jq is a hard prerequisite. Without it the hook cannot read the report or
# serialize its answer safely, so it emits a hand-written follow-up saying the
# check could not be parsed, with the report path, and exits 2.
#
# Exit 2 (INDETERMINATE) from bedrock is uncertainty, not a pass, and is fed
# back exactly like exit 1. This is a repair aid; the authoritative gates are
# the git pre-commit hook and CI.
set -u
input=$(cat)
here=$(cd "$(dirname "$0")" && pwd)
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
report=${BEDROCK_REPORT:-.bedrock-report.json}
case "$report" in
/*) ;;
*) report="$root/$report" ;;
esac
limit=${BEDROCK_LOOP_LIMIT:-5}

findings=$(BEDROCK_REPORT="$report" "${BEDROCK_CHECK:-$here/../hooks/bedrock-check.sh}" 2>&1 >/dev/null)
code=$?

if ! command -v jq >/dev/null 2>&1; then
	# Escape the path for a JSON string by hand (backslash and double quote).
	esc=$(printf '%s' "$report" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')
	printf '{"followup_message":"bedrock hook FAILED: jq is not installed, so the bedrock result (exit %s) could not be parsed. Treat this as a failing check. Report: %s"}\n' "$code" "$esc"
	echo "bedrock-stop.sh: jq is required; bedrock exit $code, report $report" >&2
	exit 2
fi

if [ "$code" -eq 0 ]; then
	echo '{}'
	exit 0
fi

loops=$(printf '%s' "$input" | jq -r '.loop_count // 0' 2>/dev/null)
case "$loops" in '' | *[!0-9]*) loops=0 ;; esac
if [ "$loops" -ge "$limit" ]; then
	msg="bedrock check FAILED (exit $code: 1 = violation, 2 = indeterminate, uncertainty not a pass) and the follow-up limit ($limit) is exhausted. The code is NOT passing. Latest report: $report"
	jq -n --arg m "$msg" --arg f "$findings" '{followup_message: ($m + "\n\n" + $f)}'
	echo "$msg" >&2
	exit 1
fi
jq -n --arg f "$findings" --argjson c "$code" --arg r "$report" \
	'{followup_message: ("bedrock check exited \($c) (1 = violation, 2 = indeterminate: uncertainty, not a pass). Fix these findings without editing .bedrock/, then stop again; the check re-runs every time. Report: \($r)\n\n" + $f)}'
