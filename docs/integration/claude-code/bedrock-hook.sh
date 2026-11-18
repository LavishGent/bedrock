#!/usr/bin/env bash
# Example only. Claude Code Stop hook adapter for bedrock (see ../README.md).
#
# It ALWAYS runs the check: there is no early exit on stop_hook_active or
# any other retry marker, because a previous attempt says nothing about the
# current code. Claude Code treats hook exit status 2 as blocking: stderr is
# shown to the model and the agent keeps working. Mapping:
#   bedrock 0 (PASS)                     -> exit 0 (the agent may stop)
#   bedrock 1 (VIOLATION)                -> exit 2 (blocked, findings on stderr)
#   bedrock 2 (INDETERMINATE, uncertain) -> exit 2 (blocked; uncertainty is not a pass)
#   anything else (runner failure)       -> exit 2 (blocked)
# The agent cannot finish while the check fails; the human interrupts the
# session to end a loop that does not converge. This is a repair aid; the
# authoritative gates are the git pre-commit hook and CI.
set -u
cat >/dev/null # hook input is not needed: the check always runs
here=$(cd "$(dirname "$0")" && pwd)
"${BEDROCK_CHECK:-$here/../hooks/bedrock-check.sh}"
code=$?
if [ "$code" -eq 0 ]; then
	exit 0
fi
echo "bedrock check did not pass (exit $code). Fix every VIOLATION and INDETERMINATE item above, then stop again; the check re-runs every time. Do not edit .bedrock/." >&2
exit 2
