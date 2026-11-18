package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const schemaDir = "../../docs/superpowers/specs/schema"

func TestValidExamples(t *testing.T) {
	s, err := loadSchemas(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	docs, _ := filepath.Glob(filepath.Join(schemaDir, "examples", "*.json"))
	if len(docs) == 0 {
		t.Fatal("no examples")
	}
	for _, p := range docs {
		if err := s.check("", p); err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
}

// TestInvalidExamples requires every negative fixture to be rejected with
// the error text recorded for it, so a fixture cannot pass for the wrong reason.
func TestInvalidExamples(t *testing.T) {
	s, err := loadSchemas(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(schemaDir, "examples", "invalid")
	raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expect map[string]string
	if err := json.Unmarshal(raw, &expect); err != nil {
		t.Fatal(err)
	}
	docs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	n := 0
	for _, p := range docs {
		name := filepath.Base(p)
		if name == "expect.json" {
			continue
		}
		n++
		want, ok := expect[name]
		if !ok {
			t.Errorf("%s has no entry in expect.json", name)
			continue
		}
		err := s.check("", p)
		if err == nil {
			t.Errorf("%s: accepted, want rejection containing %q", name, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: rejected for the wrong reason:\n%v\nwant substring %q", name, err, want)
		}
	}
	if n != len(expect) {
		t.Errorf("expect.json lists %d fixtures, directory has %d", len(expect), n)
	}
}
