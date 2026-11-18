// Package facts holds the Go form of the Bedrock fact and report schema 0.3
// (docs/superpowers/specs/schema) plus the canonical JSON, hashing and ID rules
// from its README. Nullable schema fields are pointers without omitempty, so
// they encode as JSON null; slices are always non-nil so they encode as [].
package facts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SchemaVersion is the schema version every document carries.
const SchemaVersion = "0.3"

// Location is a source position (schema common#/$defs/location).
type Location struct {
	Root   string  `json:"root"`
	Module *string `json:"module"`
	File   string  `json:"file"`
	Line   int     `json:"line"`
	Column int     `json:"column"`
}

// BuildProfile is the pinned build profile.
type BuildProfile struct {
	GOOS          string   `json:"goos"`
	GOARCH        string   `json:"goarch"`
	CgoEnabled    bool     `json:"cgo_enabled"`
	Tags          []string `json:"tags"`
	GoVersion     string   `json:"go_version"`
	XToolsVersion string   `json:"x_tools_version"`
}

// Diagnostic is the closed diagnostic shape.
type Diagnostic struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Scope    string    `json:"scope"`
	UnitID   *string   `json:"unit_id"`
	SymbolID *string   `json:"symbol_id"`
	SiteID   *string   `json:"site_id"`
	Message  string    `json:"message"`
	Loc      *Location `json:"loc"`
}

// Str returns a pointer to s (for nullable fields).
func Str(s string) *string { return &s }

// NewDiagnostic builds a diagnostic and computes its ID.
func NewDiagnostic(kind, severity string, unit, symbol *string, msg string, loc *Location) Diagnostic {
	scope := "unit"
	if unit == nil {
		scope = "global"
	}
	d := Diagnostic{Kind: kind, Severity: severity, Scope: scope, UnitID: unit, SymbolID: symbol, Message: msg, Loc: loc}
	d.ID = DiagID(d)
	return d
}

// Canon returns RFC 8785-style canonical JSON for the value shapes used in
// Bedrock documents: object keys sorted, no whitespace, no HTML escaping.
func Canon(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		panic(err)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		panic(err)
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

// SHA256 returns "sha256:<hex>" of b.
func SHA256(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func short(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16]
}

func locParts(l *Location) []any {
	if l == nil {
		return []any{nil, nil, nil, nil, nil}
	}
	var mod any
	if l.Module != nil {
		mod = *l.Module
	}
	return []any{l.Root, mod, l.File, l.Line, l.Column}
}

func ptr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// DiagID implements the schema README diagnostic ID rule.
func DiagID(d Diagnostic) string {
	t := []any{d.Kind, d.Scope, ptr(d.UnitID), ptr(d.SymbolID), ptr(d.SiteID)}
	t = append(t, locParts(d.Loc)...)
	t = append(t, d.Message)
	return "diag:" + short(Canon(t))
}

// Marshal encodes v as indented JSON with a trailing newline, without HTML
// escaping. Field order follows the struct definitions, so output is stable.
func Marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return b.Bytes()
}
