#!/usr/bin/env bash
# Example only. Claude Code hook adapter for bedrock (see README.md here).
#
# Claude Code treats a hook's exit status 2 as "blocking": stderr is shown to
# the model and, for a Stop hook, the agent keeps working. bedrock's own exit
# codes are mapped onto that:
#   bedrock 0 (PASS)                     -> hook exit 0 (allow)
#   bedrock 1 (VIOLATION)                -> hook exit 2 (block, report on stderr)
#   bedrock 2 (INDETERMINATE, uncertain) -> hook exit 2 (block, report on stderr)
# INDETERMINATE is uncertainty, not a pass, so it blocks too.
set -u
input=$(cat)
# Stop hooks: if Claude is already continuing because of this hook, let it
# stop rather than loop forever; the human sees the last report.
if command -v jq >/dev/null 2>&1 &&
	[ "$(printf '%s' "$input" | jq -r '.stop_hook_active // false' 2>/dev/null)" = "true" ]; then
	exit 0
fi
here=$(cd "$(dirname "$0")" && pwd)
"${BEDROCK_CHECK:-$here/../hooks/bedrock-check.sh}"
code=$?
if [ "$code" -eq 0 ]; then
	exit 0
fi
echo "Fix every VIOLATION and INDETERMINATE item above, then re-run until bedrock exits 0. Do not edit .bedrock/." >&2
exit 2
