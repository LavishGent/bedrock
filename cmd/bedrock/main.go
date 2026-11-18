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
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"time"

	"github.com/LavishGent/bedrock/internal/analyze"
	"github.com/LavishGent/bedrock/internal/check"
	"github.com/LavishGent/bedrock/internal/facts"
	"github.com/LavishGent/bedrock/internal/policy"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "check" {
		fmt.Fprintln(os.Stderr, "usage: bedrock check [--policy FILE] [--format json] [--out FILE] [--facts-dir DIR] [MODULE_DIR]")
		os.Exit(2)
	}
	os.Exit(runCheck(os.Args[2:], os.Stdout, os.Stderr))
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyPath := fs.String("policy", "", "policy file (default MODULE_DIR/.bedrock/policy.toml)")
	format := fs.String("format", "json", "output format (json)")
	outPath := fs.String("out", "", "write the report here instead of stdout")
	factsDir := fs.String("facts-dir", "", "also write unit shards to DIR/shards/")
	timeout := fs.Duration("timeout", 120*time.Second, "analyzer wall-clock limit")
	memMiB := fs.Int64("mem-limit-mib", 4096, "analyzer memory limit (Go runtime total)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *format != "json" {
		fmt.Fprintln(stderr, "bedrock: only --format json is implemented")
		return 2
	}
	if v := analyze.XToolsVersion(); v != analyze.PinnedXToolsVersion {
		fmt.Fprintf(stderr, "bedrock: built with golang.org/x/tools %s, pinned %s\n", v, analyze.PinnedXToolsVersion)
		return 2
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	label := *policyPath
	if label == "" {
		label = filepath.ToSlash(filepath.Join(dir, ".bedrock", "policy.toml"))
	}
	opts := check.Options{Dir: dir, PolicyPath: label, PolicyLabel: label}

	emit := func(r *facts.Report) int {
		b := facts.Marshal(r)
		if *outPath != "" {
			if err := os.WriteFile(*outPath, b, 0o644); err != nil {
				fmt.Fprintln(stderr, "bedrock:", err)
				return 2
			}
		} else if _, err := stdout.Write(b); err != nil {
			return 2
		}
		return r.ExitCode
	}

	// Aborts (timeout, memory limit, crash) become a run-level diagnostic.
	var once sync.Once
	abortCode := make(chan int, 1)
	abort := func(kind, msg string) {
		once.Do(func() {
			mp, _ := analyze.ModulePath(dir)
			pr := policy.Load(opts.PolicyPath, mp)
			var rules []policy.Rule
			if pr.Policy != nil {
				rules = pr.Policy.Rules
			}
			d := facts.NewDiagnostic(kind, "error", nil, nil, msg, nil)
			abortCode <- emit(check.PolicyOnly(label, pr.SHA256, d, rules))
		})
	}
	limit := *memMiB << 20
	debug.SetMemoryLimit(limit)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	done := make(chan *check.Output, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				abort("analyzer_crash", fmt.Sprintf("analyzer panic: %v", p))
			}
		}()
		done <- check.Run(ctx, opts)
	}()
	sample := []metrics.Sample{{Name: "/memory/classes/total:bytes"}}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case out := <-done:
			return finish(out, emit, *factsDir, stderr)
		case code := <-abortCode:
			return code
		case <-ctx.Done():
			abort("analyzer_timeout", "analyzer exceeded the wall-clock limit "+timeout.String())
			return <-abortCode
		case <-tick.C:
			metrics.Read(sample)
			if total := sample[0].Value.Uint64(); int64(total) > limit {
				abort("analyzer_memory_limit", fmt.Sprintf("analyzer used %d MiB, limit %d MiB", total>>20, *memMiB))
				return <-abortCode
			}
		}
	}
}

func finish(out *check.Output, emit func(*facts.Report) int, factsDir string, stderr io.Writer) int {
	for _, ru := range out.NotEvaluated {
		fmt.Fprintf(stderr, "bedrock: rule %q (%s) is valid but NOT EVALUATED: this build evaluates forbid_import only (plan step 2)\n", ru.ID, ru.Kind)
	}
	if len(out.Report.Rules) == 0 && len(out.NotEvaluated) > 0 {
		// Every rule is of a kind this build cannot evaluate. A report would
		// say PASS for rules that were never checked, so fail closed instead.
		fmt.Fprintln(stderr, "bedrock: no rule in the policy can be evaluated by this build; refusing to report PASS")
		return 2
	}
	if factsDir != "" {
		dir := filepath.Join(factsDir, "shards")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(stderr, "bedrock:", err)
			return 2
		}
		for _, sh := range out.Shards {
			name := url.PathEscape(sh.Unit.ID) + ".json"
			if err := os.WriteFile(filepath.Join(dir, name), facts.Marshal(sh), 0o644); err != nil {
				fmt.Fprintln(stderr, "bedrock:", err)
				return 2
			}
		}
	}
	return emit(out.Report)
}
