// Command schemacheck validates Bedrock example documents against the
// versioned JSON schemas in docs/superpowers/specs/schema.
//
// It checks each document against its schema and then against the semantic
// rules in README.md (ordering, referential integrity, ID and hash recomputation,
// verdict consistency) that JSON Schema cannot express.
//
// Usage: schemacheck <schema-dir> <schema-file>=<document> ...
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: schemacheck <schema-dir> <schema>=<document> ...")
		os.Exit(2)
	}
	dir := os.Args[1]
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	entries, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil || len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no schemas found in", dir)
		os.Exit(2)
	}
	// Register every schema under its own $id so cross-file $refs resolve offline.
	ids := map[string]string{}
	for _, p := range entries {
		f, err := os.Open(p)
		must(err)
		doc, err := jsonschema.UnmarshalJSON(f)
		f.Close()
		must(err)
		id, _ := doc.(map[string]any)["$id"].(string)
		if id == "" {
			fail(p + ": missing $id")
		}
		must(c.AddResource(id, doc))
		ids[filepath.Base(p)] = id
	}
	failed := false
	for _, arg := range os.Args[2:] {
		schemaFile, docPath, ok := strings.Cut(arg, "=")
		if !ok {
			fail("bad argument " + arg)
		}
		sch, err := c.Compile(ids[schemaFile])
		must(err)
		f, err := os.Open(docPath)
		must(err)
		doc, err := jsonschema.UnmarshalJSON(f)
		f.Close()
		must(err)
		if err := sch.Validate(doc); err != nil {
			failed = true
			fmt.Printf("FAIL %s against %s\n%v\n", docPath, schemaFile, err)
			continue
		}
		if errs := semantic(doc); len(errs) > 0 {
			failed = true
			fmt.Printf("FAIL %s semantic rules (README.md)\n  %s\n", docPath, strings.Join(errs, "\n  "))
			continue
		}
		fmt.Printf("ok   %s against %s + semantic rules\n", docPath, schemaFile)
	}
	if failed {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "schemacheck:", msg)
	os.Exit(2)
}
