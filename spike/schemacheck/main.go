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
	_, err := s.validate(schemaFile, docPath)
	return err
}

// validate checks one document and returns it decoded.
func (s *schemaSet) validate(schemaFile, docPath string) (any, error) {
	f, err := os.Open(docPath)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if schemaFile == "" {
		m, _ := doc.(map[string]any)
		kind, _ := m["kind"].(string)
		if schemaFile = schemaForKind[kind]; schemaFile == "" {
			return nil, fmt.Errorf("unknown document kind %q", kind)
		}
	}
	id, ok := s.ids[schemaFile]
	if !ok {
		return nil, fmt.Errorf("no schema %s", schemaFile)
	}
	sch, err := s.c.Compile(id)
	if err != nil {
		return nil, err
	}
	if err := sch.Validate(doc); err != nil {
		return nil, fmt.Errorf("schema %s: %w", schemaFile, err)
	}
	if errs := semantic(doc); len(errs) > 0 {
		return nil, errors.New("semantic rules (README.md):\n  " + strings.Join(errs, "\n  "))
	}
	return doc, nil
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
	byKind := map[string][]obj{}
	for _, arg := range os.Args[2:] {
		schemaFile, docPath, ok := strings.Cut(arg, "=")
		if !ok {
			schemaFile, docPath = "", arg
		}
		doc, err := s.validate(schemaFile, docPath)
		if err != nil {
			failed = true
			fmt.Printf("FAIL %s\n%v\n", docPath, err)
			continue
		}
		if d, ok := doc.(obj); ok {
			byKind[str(d["kind"])] = append(byKind[str(d["kind"])], d)
		}
		fmt.Printf("ok   %s\n", docPath)
	}
	// One run's documents together: the merge checks across shards, the
	// manifest and the report (README: Shard ownership, Recovering Entry).
	if ms := byKind["analysis_manifest"]; len(ms) == 1 && len(byKind["unit_shard"]) > 0 {
		if errs := crossCheck(ms[0], byKind["unit_shard"], byKind["report"]); len(errs) > 0 {
			failed = true
			fmt.Printf("FAIL run (manifest + %d shards)\n  %s\n", len(byKind["unit_shard"]), strings.Join(errs, "\n  "))
		} else {
			fmt.Printf("ok   run (manifest + %d shards, %d reports)\n", len(byKind["unit_shard"]), len(byKind["report"]))
		}
	}
	if failed {
		os.Exit(1)
	}
}
