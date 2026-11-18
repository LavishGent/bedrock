#!/usr/bin/env bash
# Example only. Cursor "stop" hook adapter for bedrock (see README.md here).
# When bedrock does not exit 0, it asks Cursor to send a follow-up message
# to the agent containing the findings, so the agent keeps fixing.
# bedrock exit 2 (INDETERMINATE) is uncertainty, not a pass, and is fed back
# exactly like exit 1.
set -u
input=$(cat)
# Bound the loop: Cursor reports how many times this stop hook already fired.
loops=$(printf '%s' "$input" | jq -r '.loop_count // 0' 2>/dev/null || echo 0)
if [ "${loops:-0}" -ge "${BEDROCK_MAX_LOOPS:-5}" ]; then
	echo '{}'
	exit 0
fi
here=$(cd "$(dirname "$0")" && pwd)
findings=$("${BEDROCK_CHECK:-$here/../hooks/bedrock-check.sh}" 2>&1 >/dev/null)
code=$?
if [ "$code" -eq 0 ]; then
	echo '{}'
	exit 0
fi
jq -n --arg f "$findings" --argjson c "$code" '{followup_message: ("bedrock check exited \($c) (1 = violation, 2 = indeterminate: uncertainty, not a pass). Fix these findings without editing .bedrock/, then stop again:\n\n" + $f)}'
