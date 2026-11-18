package facts

// Shard is one unit_shard document.
type Shard struct {
	SchemaVersion      string       `json:"schema_version"`
	Kind               string       `json:"kind"`
	BuildProfileDigest string       `json:"build_profile_digest"`
	Unit               Unit         `json:"unit"`
	Symbols            []Symbol     `json:"symbols"`
	ExternalSymbols    []ExtSymbol  `json:"external_symbols"`
	Imports            []Import     `json:"imports"`
	CallSites          []any        `json:"call_sites"`
	Routes             []any        `json:"routes"`
	RouteMiddleware    []any        `json:"route_middleware"`
	Diagnostics        []Diagnostic `json:"diagnostics"`
}

// Unit is the shard's unit record.
type Unit struct {
	ID            string         `json:"id"`
	Lang          string         `json:"lang"`
	Origin        string         `json:"origin"`
	Module        *string        `json:"module"`
	Dir           string         `json:"dir"`
	Files         []File         `json:"files"`
	ExcludedFiles []ExcludedFile `json:"excluded_files"`
	ContentHash   string         `json:"content_hash"`
	DepHashes     []DepHash      `json:"dep_hashes"`
}

// File is one compiled source file.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// ExcludedFile is a Go file in the package directory that is not compiled.
type ExcludedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// DepHash is the content hash of one direct import.
type DepHash struct {
	UnitID      string `json:"unit_id"`
	ContentHash string `json:"content_hash"`
}

// Symbol is a declared symbol owned by the shard's unit.
type Symbol struct {
	ID          string     `json:"id"`
	UnitID      string     `json:"unit_id"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	Exported    bool       `json:"exported"`
	HasBody     bool       `json:"has_body"`
	InUniverse  bool       `json:"in_universe"`
	ParentID    *string    `json:"parent_id"`
	OriginID    *string    `json:"origin_id"`
	WrapperKind *string    `json:"wrapper_kind"`
	EntryHints  EntryHints `json:"entry_hints"`
	Loc         *Location  `json:"loc"`
}

// EntryHints explain Entry membership.
type EntryHints struct {
	IsMain       bool `json:"is_main"`
	IsInit       bool `json:"is_init"`
	AddressTaken bool `json:"address_taken"`
}

// ExtSymbol is a symbol referenced but owned by another unit.
type ExtSymbol struct {
	ID     string `json:"id"`
	UnitID string `json:"unit_id"`
}

// Import is one direct import declaration.
type Import struct {
	FromUnit string   `json:"from_unit"`
	ToUnit   string   `json:"to_unit"`
	ToPath   string   `json:"to_path"`
	Loc      Location `json:"loc"`
}

// ContentHash implements the schema README unit content hash.
func ContentHash(files []File) string {
	var b []byte
	for _, f := range files {
		b = append(b, f.Path...)
		b = append(b, 0)
		b = append(b, f.SHA256[len("sha256:"):]...)
		b = append(b, '\n')
	}
	return SHA256(b)
}
