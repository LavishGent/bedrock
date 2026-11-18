// Command bedrock is the Bedrock POC CLI.
//
//	bedrock check [--policy FILE] [--format json] [--out FILE] [--facts-dir DIR]
//	              [--timeout 120s] [--mem-limit-mib 4096] [MODULE_DIR]
//
// MODULE_DIR (default ".") is the root of the one module to analyze; report
// locations are relative to it. The policy defaults to
// MODULE_DIR/.bedrock/policy.toml. The JSON report goes to stdout (or --out);
// --facts-dir also writes one unit shard per unit under DIR/shards/.
// Exit codes follow the verdict table: 0 PASS, 1 VIOLATION, 2 INDETERMINATE.
//
// Analysis runs in a supervised child process of the same binary. A panic in
// any goroutine, the wall-clock limit or the memory limit ends the child, and
// the parent writes a run-level analyzer_crash, analyzer_timeout or
// analyzer_memory_limit report from what it observed (exit status, signal,
// peak RSS, stderr). A report is always written.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/check"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/fsutil"
	"github.com/LavishGent/bedrock/internal/policy"
)

const (
	childCmd = "__analyze" // hidden subcommand: the supervised analysis child
	// memExit is the child's exit status when its memory watchdog trips.
	memExit = 86
	// faultEnv injects faults into the child for the supervisor's regression
	// tests: "hang-policy" (hangs while acquiring the policy, before the
	// handshake), "panic" (a goroutine panics), "hang" (never finishes),
	// "alloc" (allocates without bound). It is test-only and has no other
	// effect.
	faultEnv = "BEDROCK_INTERNAL_FAULT"

	maxTimeout = time.Hour
	minMemMiB  = 64
	maxMemMiB  = 1 << 20
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == childCmd {
		os.Exit(childMain(os.Args[2:]))
	}
	if len(os.Args) < 2 || os.Args[1] != "check" {
		fmt.Fprintln(os.Stderr, "usage: bedrock check [--policy FILE] [--format json] [--out FILE] [--facts-dir DIR] [--timeout D] [--mem-limit-mib N] [MODULE_DIR]")
		os.Exit(2)
	}
	os.Exit(runCheck(os.Args[2:], os.Stdout, os.Stderr))
}

// policyMeta is the bounded policy metadata the child hands to the parent
// before analysis starts (the handshake), so an abort report never needs
// fresh policy or target I/O in the parent.
type policyMeta struct {
	SHA256 *string     `json:"sha256"`
	Rules  []ruleIdent `json:"rules"`
}

type ruleIdent struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

const maxMetaBytes = 1 << 20

// payload is what the child hands back to the parent.
type payload struct {
	Report       *facts.Report  `json:"report"`
	Shards       []*facts.Shard `json:"shards"`
	NotEvaluated []string       `json:"not_evaluated"`
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyPath := fs.String("policy", "", "policy file (default MODULE_DIR/.bedrock/policy.toml)")
	format := fs.String("format", "json", "output format (json)")
	outPath := fs.String("out", "", "write the report here instead of stdout")
	factsDir := fs.String("facts-dir", "", "also write unit shards to DIR/shards/")
	timeout := fs.Duration("timeout", 120*time.Second, "analyzer wall-clock limit (0 < D <= 1h)")
	memMiB := fs.Int64("mem-limit-mib", 4096, fmt.Sprintf("analyzer memory limit in MiB (%d..%d)", minMemMiB, maxMemMiB))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *format != "json" {
		fmt.Fprintln(stderr, "bedrock: only --format json is implemented")
		return 2
	}
	if *timeout <= 0 || *timeout > maxTimeout {
		fmt.Fprintf(stderr, "bedrock: --timeout must be in (0, %s], got %s\n", maxTimeout, *timeout)
		return 2
	}
	if *memMiB < minMemMiB || *memMiB > maxMemMiB {
		fmt.Fprintf(stderr, "bedrock: --mem-limit-mib must be in [%d, %d], got %d\n", minMemMiB, maxMemMiB, *memMiB)
		return 2
	}
	if v := analyze.XToolsVersion(); v != analyze.PinnedXToolsVersion {
		fmt.Fprintf(stderr, "bedrock: built with golang.org/x/tools %s, pinned %s\n", v, analyze.PinnedXToolsVersion)
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "bedrock: at most one MODULE_DIR")
		return 2
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	label := *policyPath
	if label == "" {
		label = filepath.ToSlash(filepath.Join(dir, ".bedrock", "policy.toml"))
	}

	p, abortKind, abortMsg, meta := supervise(dir, label, *timeout, *memMiB, stderr)
	if p == nil {
		// The abort report uses only what the child handed over before
		// analysis; the parent never re-reads the policy or the target.
		var sum *string
		var rules []policy.Rule
		if meta != nil {
			sum = meta.SHA256
			for _, r := range meta.Rules {
				rules = append(rules, policy.Rule{ID: r.ID, Kind: r.Kind})
			}
		} else {
			abortMsg += " (policy metadata unavailable: the analysis process aborted before reporting it, so no rule is listed)"
		}
		d := facts.NewDiagnostic(abortKind, "error", nil, nil, abortMsg, nil)
		p = &payload{Report: check.PolicyOnly(label, sum, d, rules)}
	}
	for _, s := range p.NotEvaluated {
		fmt.Fprintln(stderr, "bedrock:", s)
	}
	if *factsDir != "" {
		sd := filepath.Join(*factsDir, "shards")
		if err := os.MkdirAll(sd, 0o755); err != nil {
			fmt.Fprintln(stderr, "bedrock:", err)
			return 2
		}
		for _, sh := range p.Shards {
			if err := os.WriteFile(filepath.Join(sd, url.PathEscape(sh.Unit.ID)+".json"), facts.Marshal(sh), 0o644); err != nil {
				fmt.Fprintln(stderr, "bedrock:", err)
				return 2
			}
		}
	}
	b := facts.Marshal(p.Report)
	if *outPath != "" {
		if err := os.WriteFile(*outPath, b, 0o644); err != nil {
			fmt.Fprintln(stderr, "bedrock:", err)
			return 2
		}
	} else if _, err := stdout.Write(b); err != nil {
		return 2
	}
	return p.Report.ExitCode
}

// supervise runs the analysis child and returns its payload, or nil with the
// run-level diagnostic kind and message describing why it produced none.
func supervise(dir, label string, timeout time.Duration, memMiB int64, stderr io.Writer) (*payload, string, string, *policyMeta) {
	exe, err := os.Executable()
	if err != nil {
		return nil, "analyzer_crash", "cannot locate the bedrock executable: " + err.Error(), nil
	}
	tmp, err := os.MkdirTemp("", "bedrock-*")
	if err != nil {
		return nil, "analyzer_crash", "cannot create a temporary directory: " + err.Error(), nil
	}
	defer os.RemoveAll(tmp)
	result := filepath.Join(tmp, "result.json")
	metaPath := filepath.Join(tmp, "meta.json")
	abort := func(kind, msg string) (*payload, string, string, *policyMeta) {
		var m *policyMeta
		if raw, err := fsutil.ReadRegular(metaPath, maxMetaBytes); err == nil {
			var mm policyMeta
			if json.Unmarshal(raw, &mm) == nil {
				m = &mm
			}
		}
		return nil, kind, msg, m
	}
	cmd := exec.Command(exe, childCmd, "--result", result, "--meta", metaPath,
		"--mem-limit-mib", strconv.FormatInt(memMiB, 10), "--policy", label, dir)
	var errBuf bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &errBuf, n: 64 << 10}
	if err := cmd.Start(); err != nil {
		return abort("analyzer_crash", "cannot start the analysis process: "+err.Error())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return abort("analyzer_timeout", fmt.Sprintf("analysis exceeded the wall-clock limit %s and was killed", timeout))
	}
	st := cmd.ProcessState
	peak := peakRSSMiB(st)
	switch {
	case st.ExitCode() == memExit || peak > memMiB:
		return abort("analyzer_memory_limit", fmt.Sprintf("analysis exceeded the memory limit of %d MiB (peak RSS %d MiB)", memMiB, peak))
	case waitErr != nil || !st.Success():
		return abort("analyzer_crash", fmt.Sprintf("analysis process failed (%s): %s", st.String(), firstPanicLine(errBuf.String())))
	}
	raw, err := os.ReadFile(result)
	var p payload
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	if err != nil || p.Report == nil {
		return abort("analyzer_crash", "analysis process exited without a readable result")
	}
	return &p, "", "", nil
}

// childMain runs the analysis and writes the payload to --result.
func childMain(args []string) int {
	fs := flag.NewFlagSet(childCmd, flag.ContinueOnError)
	result := fs.String("result", "", "")
	metaPath := fs.String("meta", "", "")
	memMiB := fs.Int64("mem-limit-mib", 4096, "")
	policyPath := fs.String("policy", "", "")
	if err := fs.Parse(args); err != nil || *result == "" || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "bedrock: bad child invocation")
		return 2
	}
	limit := *memMiB << 20
	debug.SetMemoryLimit(limit)
	go func() { // memory watchdog: exit before the OS has to step in
		sample := []metrics.Sample{{Name: "/memory/classes/total:bytes"}}
		for range time.Tick(20 * time.Millisecond) {
			metrics.Read(sample)
			if int64(sample[0].Value.Uint64()) > limit {
				os.Exit(memExit)
			}
		}
	}()
	fault := os.Getenv(faultEnv)
	if fault == "hang-policy" { // fault during policy acquisition: before the handshake
		select {}
	}
	opts := check.Options{Dir: fs.Arg(0), PolicyPath: *policyPath, PolicyLabel: *policyPath}
	pr := check.LoadPolicy(opts)
	if *metaPath != "" {
		m := policyMeta{SHA256: pr.SHA256, Rules: []ruleIdent{}}
		if pr.Policy != nil {
			for _, r := range pr.Policy.Rules {
				m.Rules = append(m.Rules, ruleIdent{ID: r.ID, Kind: r.Kind})
			}
		}
		if err := writeAtomic(*metaPath, m); err != nil {
			fmt.Fprintln(os.Stderr, "bedrock:", err)
			return 2
		}
	}
	injectFault(fault)

	out := check.RunWithPolicy(contextBackground(), opts, pr)
	p := payload{Report: out.Report, Shards: out.Shards}
	for _, ru := range out.NotEvaluated {
		p.NotEvaluated = append(p.NotEvaluated, fmt.Sprintf("rule %q (%s) is NOT EVALUATED by this build (forbid_import only); it is reported INDETERMINATE", ru.ID, ru.Kind))
	}
	raw, err := json.Marshal(p)
	if err == nil {
		err = os.WriteFile(*result, raw, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bedrock:", err)
		return 2
	}
	return 0
}

// writeAtomic writes v as JSON via a temporary file and rename, so the
// parent never sees a partial handshake.
func writeAtomic(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func injectFault(f string) {
	switch f {
	case "panic":
		go func() { panic("injected fault: loader goroutine panic") }()
		time.Sleep(time.Minute)
	case "hang":
		select {}
	case "alloc":
		var hold [][]byte
		for {
			b := make([]byte, 16<<20)
			for i := range b {
				b[i] = 1
			}
			hold = append(hold, b)
		}
	}
}

// peakRSSMiB reads the child's peak resident set size from its rusage.
func peakRSSMiB(st *os.ProcessState) int64 {
	ru, ok := st.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return int64(ru.Maxrss) >> 20 // bytes
	}
	return int64(ru.Maxrss) >> 10 // KiB
}

func firstPanicLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "fatal error:") {
			return strings.TrimSpace(line)
		}
	}
	if s = strings.TrimSpace(s); len(s) > 300 {
		s = s[:300]
	}
	if s == "" {
		return "no output"
	}
	return s
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	if _, err := l.w.Write(q); err != nil {
		return 0, errors.New("stderr capture failed")
	}
	return len(p), nil
}

func contextBackground() context.Context { return context.Background() }
