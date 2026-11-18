package facts

// Manifest is the analysis_manifest document (analysis.schema.json): one per
// analyzer run, naming the shard set, the universe and the Entry digest.
type Manifest struct {
	SchemaVersion      string            `json:"schema_version"`
	Kind               string            `json:"kind"`
	Analyzer           Analyzer          `json:"analyzer"`
	Status             string            `json:"status"`
	BuildProfile       BuildProfile      `json:"build_profile"`
	BuildProfileDigest string            `json:"build_profile_digest"`
	Module             ManifestModule    `json:"module"`
	Patterns           []string          `json:"patterns"`
	Limits             Limits            `json:"limits"`
	Universe           Universe          `json:"universe"`
	Units              []ManifestUnit    `json:"units"`
	ExcludedPackages   []ExcludedPackage `json:"excluded_packages"`
	Entry              EntryDigest       `json:"entry"`
	GlobalDiagnostics  []Diagnostic      `json:"global_diagnostics"`
}

// Analyzer names the analyzer build.
type Analyzer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ManifestModule is the analyzed module.
type ManifestModule struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// Limits are the analyzer resource limits.
type Limits struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	MemoryMiB      int `json:"memory_mib"`
}

// Universe describes the analysis universe.
type Universe struct {
	Definition     string `json:"definition"`
	BodylessPolicy string `json:"bodyless_policy"`
	FunctionCount  int    `json:"function_count"`
	BodylessCount  int    `json:"bodyless_count"`
}

// ManifestUnit binds one unit to its shard hash.
type ManifestUnit struct {
	ID          string `json:"id"`
	Origin      string `json:"origin"`
	ContentHash string `json:"content_hash"`
	Shard       string `json:"shard"`
}

// EntryDigest is the count and digest of the Entry set.
type EntryDigest struct {
	Count  int    `json:"count"`
	Digest string `json:"digest"`
}

// EntryDigestOf implements the schema README Entry digest: sha256 over the
// sorted Entry symbol IDs, each followed by LF.
func EntryDigestOf(sorted []string) EntryDigest {
	var b []byte
	for _, id := range sorted {
		b = append(b, id...)
		b = append(b, '\n')
	}
	return EntryDigest{Count: len(sorted), Digest: SHA256(b)}
}
