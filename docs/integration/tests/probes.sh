#!/usr/bin/env bash
# Failure-path probes for the step 5 integration examples (no agent, no
# network, no repository changes; everything happens under a temp dir).
# A stub checker stands in for bedrock and a git shim injects failures, so
# the probes test the scripts' control flow and quoting, not the analyzer.
#
# Usage: docs/integration/tests/probes.sh      (prints PASS/FAIL per probe)
# Needs: bash, git, perl, jq, python3.
set -u
here=$(cd "$(dirname "$0")" && pwd)
integ=$(cd "$here/.." && pwd)
tmp=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/bedrock-probes.XXXXXX")" && pwd -P)
fails=0
cd "$tmp" || exit 2 # every probe runs inside the temp dir
pass() { echo "PASS  $*"; }
fail() {
	echo "FAIL  $*"
	fails=$((fails + 1))
}

# Stub checker: logs its working directory and arguments (one per line,
# NUL-separated file), writes the expected violating baseline report to
# --out, and exits $STUB_CODE (default 1).
# shellcheck disable=SC2016 # the stub's source is literal on purpose
stub_src='#!/usr/bin/env bash
{ printf "%s\0" "$PWD" "$@"; } >"$STUB_LOG"
report=
while [ "$#" -gt 0 ]; do case "$1" in --out) shift; report=$1 ;; esac; shift; done
[ -z "$report" ] || cat >"$report" <<JSON
{"exit_code":1,"violations":[
 {"rule_id":"auth-private-import","location":{"file":"internal/billing/billing.go","line":9}},
 {"rule_id":"db-in-repo","location":{"file":"internal/handler/handler.go","line":95}},
 {"rule_id":"routes-require-auth","location":{"file":"internal/server/server.go","line":18}}]}
JSON
echo "stub checker" >&2
exit "${STUB_CODE:-1}"'

# ---- S2: generated commands keep hostile paths literal ------------------
# shellcheck disable=SC2016 # hostile path text must stay literal
weird='sp ace '\''sq'\'' "dq" $HOME `true` $(touch REVIEW_SENTINEL)'
# shellcheck disable=SC2016
for name in 'demo$(touch REVIEW_SENTINEL)' "$weird"; do
	base="$tmp/s2/$name"
	mkdir -p "$base/bin dir \$(touch REVIEW_SENTINEL)"
	bin="$base/bin dir \$(touch REVIEW_SENTINEL)/bedrock \`true\` '\$x'"
	printf '%s\n' "$stub_src" >"$bin"
	chmod +x "$bin"
	out="$base/out"
	export STUB_LOG="$tmp/stub.log"
	log="$tmp/prepare.log"
	(cd "$tmp" && BEDROCK="$bin" "$integ/agent-demo/prepare.sh" "$out") >"$log" 2>&1
	code=$?
	if [ "$code" -ne 0 ]; then
		fail "S2 prepare into [$name] exited $code: $(tail -3 "$log")"
		continue
	fi
	recheck=$(sed -n 's/^re-check:   //p' "$log")
	exportl=$(sed -n 's/^checker:    //p' "$log")
	promptcmd=$(sed -n '5s/^    //p' "$out/prompt.txt")
	[ "$promptcmd" = "${recheck#* && }" ] || fail "S2 [$name] prompt.txt command differs from re-check command"
	for sh in bash sh zsh; do
		command -v "$sh" >/dev/null 2>&1 || continue
		rm -f "$STUB_LOG"
		runcwd="$tmp/run-$sh"
		mkdir -p "$runcwd"
		(cd "$runcwd" && "$sh" -c "$recheck") >/dev/null 2>&1
		got=$(python3 -c 'import sys; print(repr(open(sys.argv[1],"rb").read().split(b"\0")[:-1]))' "$STUB_LOG" 2>/dev/null)
		want=$(python3 -c 'import sys; print(repr([a.encode() for a in sys.argv[1:]]))' "$out/demo" check --policy .bedrock/policy.toml --out "$out/report.json" .)
		if [ "$got" = "$want" ]; then
			pass "S2 [$name] re-check via $sh: checker got literal cwd and arguments"
		else
			fail "S2 [$name] re-check via $sh: got $got want $want"
		fi
		val=$(cd "$runcwd" && "$sh" -c "$exportl; printf '%s' \"\$BEDROCK\"")
		if [ "$val" = "$bin" ]; then
			pass "S2 [$name] export via $sh is literal"
		else
			fail "S2 [$name] export via $sh gave [$val]"
		fi
	done
	if find "$tmp" -name REVIEW_SENTINEL | grep -q .; then
		fail "S2 [$name] a REVIEW_SENTINEL file was created"
	else
		pass "S2 [$name] no REVIEW_SENTINEL anywhere"
	fi
done

# ---- S3: Cursor hook without jq stays valid JSON ------------------------
nojq="$tmp/nojq-bin"
mkdir -p "$nojq"
for t in bash cat dirname git sed sh printf; do
	p=$(command -v "$t") && [ "${p#/}" != "$p" ] && ln -sf "$p" "$nojq/$t"
done
stub="$tmp/stub-checker"
printf '%s\n' "$stub_src" >"$stub"
chmod +x "$stub"
rpt="$tmp/report with
newline	and tab \"q\" \\ back.json"
export STUB_LOG="$tmp/stub-nojq.log"
echo '{"loop_count":0}' | STUB_CODE=2 BEDROCK="$stub" BEDROCK_REPORT="$rpt" PATH="$nojq" \
	"$integ/cursor/bedrock-stop.sh" >"$tmp/cursor.out" 2>"$tmp/cursor.err"
code=$?
if python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert "FAILED" in d["followup_message"]' "$tmp/cursor.out"; then
	pass "S3 no-jq output is valid JSON with a FAILED followup_message (path has newline and tab)"
else
	fail "S3 no-jq output is not valid JSON: $(cat "$tmp/cursor.out")"
fi
if [ "$code" -eq 2 ]; then pass "S3 no-jq hook exits 2"; else fail "S3 no-jq hook exited $code"; fi
if grep -q 'and tab' "$tmp/cursor.err"; then pass "S3 report path is on stderr"; else fail "S3 report path missing from stderr"; fi

# ---- N1: a failing git step stops preparation ---------------------------
shim="$tmp/git-shim"
mkdir -p "$shim"
realgit=$(command -v git)
cat >"$shim/git" <<SHIM
#!/usr/bin/env bash
op=\$1
[ "\$op" = -c ] && op=\$5
echo "\$op" >>"\$GIT_LOG"
[ "\$op" = "\$GIT_FAIL" ] && exit 1
exec "$realgit" "\$@"
SHIM
chmod +x "$shim/git"
for step in init add commit; do
	export GIT_LOG="$tmp/git-$step.log" GIT_FAIL="$step" STUB_LOG="$tmp/stub-n1.log"
	: >"$GIT_LOG"
	out="$tmp/n1-$step"
	PATH="$shim:$PATH" BEDROCK="$stub" "$integ/agent-demo/prepare.sh" "$out" >"$tmp/n1.out" 2>"$tmp/n1.err"
	code=$?
	ops=$(tr '\n' ' ' <"$GIT_LOG")
	case "$step" in
	init) want="init " ;;
	add) want="init add " ;;
	commit) want="init add commit " ;;
	esac
	if [ "$code" -eq 2 ] && [ "$ops" = "$want" ] && ! grep -q 'Baseline ready' "$tmp/n1.out" && [ ! -e "$out/prompt.txt" ] &&
		grep -q 'creating the git baseline failed' "$tmp/n1.err"; then
		pass "N1 git $step fails: exit 2, git ops [$ops], no later step, no prompt"
	else
		fail "N1 git $step fails: exit $code, git ops [$ops], stdout $(tr '\n' ' ' <"$tmp/n1.out")"
	fi
done

echo "probes: $fails failed (artifacts in $tmp)"
[ "$fails" -eq 0 ]
