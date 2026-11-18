// Command schemacheck validates Bedrock example documents against the
// versioned JSON schemas in docs/superpowers/specs/schema.
//
// It checks each document against its schema and then against the semantic
// rules in README.md (ordering, referential integrity, ID and hash recomputation,
// verdict consistency, assumption attribution) that JSON Schema cannot express.
//
// Usage:
//
//	schemacheck <schema-dir> <schema-file>=<document> ...
//	schemacheck <schema-dir> <document> ...   (schema chosen by the document's kind)
//
// main_test.go runs every file in examples/ (must pass) and examples/invalid/
// (must fail with the error substring recorded in examples/invalid/expect.json).
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var schemaForKind = map[string]string{
	"unit_shard":        "shard.schema.json",
	"analysis_manifest": "analysis.schema.json",
	"report":            "report.schema.json",
}

// checker compiles the schemas in one directory.
type schemaSet struct {
	c   *jsonschema.Compiler
	ids map[string]string
}

func loadSchemas(dir string) (*schemaSet, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	entries, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("no schemas found in %s", dir)
	}
	// Register every schema under its own $id so cross-file $refs resolve offline.
	ids := map[string]string{}
	for _, p := range entries {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		id, _ := doc.(map[string]any)["$id"].(string)
		if id == "" {
			return nil, fmt.Errorf("%s: missing $id", p)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, err
		}
		ids[filepath.Base(p)] = id
	}
	invs, _ := filepath.Glob(filepath.Join(dir, "stdlib-*.txt"))
	if len(invs) == 0 {
		return nil, fmt.Errorf("no stdlib-<go_version>.txt inventory in %s", dir)
	}
	for _, p := range invs {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "stdlib-"), ".txt")
		set := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				set[line] = true
			}
		}
		stdlibInventory[v] = set
	}
	return &schemaSet{c, ids}, nil
}

// check validates one document. schemaFile may be empty to pick by kind.
func (s *schemaSet) check(schemaFile, docPath string) error {
	f, err := os.Open(docPath)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(f)
	f.Close()
	if err != nil {
		return err
	}
	if schemaFile == "" {
		m, _ := doc.(map[string]any)
		kind, _ := m["kind"].(string)
		if schemaFile = schemaForKind[kind]; schemaFile == "" {
			return fmt.Errorf("unknown document kind %q", kind)
		}
	}
	id, ok := s.ids[schemaFile]
	if !ok {
		return fmt.Errorf("no schema %s", schemaFile)
	}
	sch, err := s.c.Compile(id)
	if err != nil {
		return err
	}
	if err := sch.Validate(doc); err != nil {
		return fmt.Errorf("schema %s: %w", schemaFile, err)
	}
	if errs := semantic(doc); len(errs) > 0 {
		return errors.New("semantic rules (README.md):\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: schemacheck <schema-dir> [<schema>=]<document> ...")
		os.Exit(2)
	}
	s, err := loadSchemas(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemacheck:", err)
		os.Exit(2)
	}
	failed := false
	for _, arg := range os.Args[2:] {
		schemaFile, docPath, ok := strings.Cut(arg, "=")
		if !ok {
			schemaFile, docPath = "", arg
		}
		if err := s.check(schemaFile, docPath); err != nil {
			failed = true
			fmt.Printf("FAIL %s\n%v\n", docPath, err)
			continue
		}
		fmt.Printf("ok   %s\n", docPath)
	}
	if failed {
		os.Exit(1)
	}
}
