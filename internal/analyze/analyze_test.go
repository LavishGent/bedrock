package analyze

import (
	"os"
	"strings"
	"testing"
)

// The embedded inventory must be the documented one byte for byte.
func TestInventoryMatchesDocs(t *testing.T) {
	doc, err := os.ReadFile("../../docs/superpowers/specs/schema/stdlib-go1.26.11.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(doc) != stdlibInventory {
		t.Fatal("internal/analyze/stdlib-go1.26.11.txt differs from docs/superpowers/specs/schema/stdlib-go1.26.11.txt")
	}
	inv := StdlibInventory()
	for _, p := range []string{"unsafe", "net/http", "database/sql"} {
		if !inv[p] {
			t.Errorf("inventory lacks %s", p)
		}
	}
	if inv["app/internal/helper"] {
		t.Error("inventory must not contain dotless module paths")
	}
}

func TestEnvIsControlled(t *testing.T) {
	t.Setenv("GOROOT", "/nonexistent")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPACKAGESDRIVER", "/bin/false")
	t.Setenv("GOEXPERIMENT", "evil")
	t.Setenv("GOPATH", "/keep/me")
	got := map[string][]string{}
	for _, kv := range Env() {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = append(got[k], v)
	}
	want := map[string]string{"GOFLAGS": "-mod=readonly", "GOPACKAGESDRIVER": "off", "GOWORK": "off",
		"GOTOOLCHAIN": "local", "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64", "GOENV": "off", "GOEXPERIMENT": ""}
	for k, v := range want {
		if len(got[k]) != 1 || got[k][0] != v {
			t.Errorf("%s = %v, want exactly [%q]", k, got[k], v)
		}
	}
	if _, ok := got["GOROOT"]; ok {
		t.Error("GOROOT from the caller must be dropped")
	}
	if len(got["GOPATH"]) != 1 || got["GOPATH"][0] != "/keep/me" {
		t.Errorf("GOPATH = %v, want passthrough", got["GOPATH"])
	}
}
