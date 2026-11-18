#!/usr/bin/env bash
# Example only. Shared runner used by the Claude Code, Cursor and git hook
# examples in docs/integration. It runs
#
#   bedrock check --policy .bedrock/policy.toml <module>
#
# writes the JSON report to $BEDROCK_REPORT (default .bedrock-report.json in
# the repository root, which you should add to .gitignore), prints a short
# summary to stderr, and exits with bedrock's own exit code:
#
#   0  PASS           every rule passed
#   1  VIOLATION      at least one rule was violated
#   2  INDETERMINATE  bedrock could not decide (load failure, unsupported
#                     construct, reflection, policy problem, crash, limit).
#                     Exit 2 means UNCERTAINTY, NOT PASS: treat it as blocking.
#
# Environment:
#   BEDROCK         path to the pinned bedrock binary (default: bedrock on PATH)
#   BEDROCK_MODULE  module directory to analyze, relative to the repo root (default: .)
#   BEDROCK_POLICY  policy file (default: .bedrock/policy.toml)
#   BEDROCK_REPORT  where to write the JSON report (default: .bedrock-report.json)
set -u

root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root" || exit 2
bin=${BEDROCK:-bedrock}
module=${BEDROCK_MODULE:-.}
policy=${BEDROCK_POLICY:-.bedrock/policy.toml}
report=${BEDROCK_REPORT:-.bedrock-report.json}

"$bin" check --policy "$policy" --out "$report" "$module"
code=$?

if [ "$code" -ne 0 ]; then
	{
		echo "bedrock check exited $code ($( [ "$code" -eq 1 ] && echo VIOLATION || echo 'INDETERMINATE: uncertainty, not a pass' ))."
		case "$report" in
		/*) echo "Full JSON report: $report" ;;
		*) echo "Full JSON report: $root/$report" ;;
		esac
		if command -v jq >/dev/null 2>&1 && [ -s "$report" ]; then
			jq -r '
			  (.violations[] | "VIOLATION \(.rule_id) at \(.location.file // "?"):\(.location.line // "?"): \(.message)\n  fix: \(.remediation.message)"),
			  (.diagnostics[] | select(.relevance != "warning") |
			    "\(.relevance | ascii_upcase) \(.kind) \(.loc.file // "")\(if .loc then ":\(.loc.line)" else "" end): \(.message)")
			' "$report"
		fi
	} >&2
fi
exit "$code"
