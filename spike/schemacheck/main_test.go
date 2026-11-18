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

// TestCrossCheck applies the run-level merge checks to a two-unit run and
// to tampered copies of it.
func TestCrossCheck(t *testing.T) {
	sym := func(id string) obj {
		return obj{"id": id, "kind": "func", "in_universe": true}
	}
	a := obj{"unit": obj{"id": "m/a"}, "build_profile_digest": "d",
		"symbols":          []any{sym("m/a.A")},
		"external_symbols": []any{obj{"id": "m/b.B", "unit_id": "m/b"}},
		"call_sites":       []any{obj{"targets": []any{"m/b.B"}}}, "routes": []any{}}
	b := obj{"unit": obj{"id": "m/b"}, "build_profile_digest": "d",
		"symbols": []any{sym("m/b.B")}, "external_symbols": []any{}, "call_sites": []any{}, "routes": []any{}}
	manifest := func() obj {
		return obj{"status": "complete", "build_profile_digest": "d",
			"units": []any{obj{"id": "m/a", "shard": sha(canon(a))}, obj{"id": "m/b", "shard": sha(canon(b))}},
			"entry": obj{"count": json.Number("1"), "digest": sha([]byte("m/a.A\n"))}}
	}
	if errs := crossCheck(manifest(), []obj{a, b}, nil); len(errs) != 0 {
		t.Fatalf("valid run rejected: %v", errs)
	}
	m := manifest()
	m["entry"] = obj{"count": json.Number("2"), "digest": sha([]byte("m/a.A\nm/b.B\n"))}
	if errs := crossCheck(m, []obj{a, b}, nil); len(errs) != 1 || !strings.Contains(errs[0], "entry_mismatch") {
		t.Fatalf("entry mismatch: %v", errs)
	}
	m = manifest()
	m["units"].([]any)[1].(obj)["shard"] = sha([]byte("x"))
	if errs := crossCheck(m, []obj{a, b}, nil); len(errs) != 1 || !strings.Contains(errs[0], "hash") {
		t.Fatalf("shard hash: %v", errs)
	}
	r := obj{"inputs": obj{"manifest_sha256": "sha256:00", "shard_count": json.Number("2")}}
	if errs := crossCheck(manifest(), []obj{a, b}, []obj{r}); len(errs) != 1 || !strings.Contains(errs[0], "manifest_sha256") {
		t.Fatalf("report manifest hash: %v", errs)
	}
}
