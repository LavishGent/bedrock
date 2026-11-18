package facts

// Report is the bedrock check JSON report (report.schema.json).
type Report struct {
	SchemaVersion string             `json:"schema_version"`
	Kind          string             `json:"kind"`
	Tool          Tool               `json:"tool"`
	Status        string             `json:"status"`
	ExitCode      int                `json:"exit_code"`
	BuildProfile  BuildProfile       `json:"build_profile"`
	Policy        PolicyRef          `json:"policy"`
	Inputs        Inputs             `json:"inputs"`
	Assumptions   []Assumption       `json:"assumptions"`
	Summary       Summary            `json:"summary"`
	Packages      Packages           `json:"packages"`
	Rules         []RuleResult       `json:"rules"`
	Violations    []Violation        `json:"violations"`
	Diagnostics   []ReportDiagnostic `json:"diagnostics"`
}

// Tool identifies the producer.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// PolicyRef names the policy file; SHA256 is null when it is missing.
type PolicyRef struct {
	Path   string  `json:"path"`
	SHA256 *string `json:"sha256"`
}

// Inputs records what the evaluation read.
type Inputs struct {
	ManifestSHA256 *string `json:"manifest_sha256"`
	ShardCount     int     `json:"shard_count"`
}

// Assumption is one relevance exemption with its count.
type Assumption struct {
	ID            string `json:"id"`
	Statement     string `json:"statement"`
	ExemptedCount int    `json:"exempted_count"`
}

// Summary counts the report body.
type Summary struct {
	Violations               int `json:"violations"`
	IndeterminateDiagnostics int `json:"indeterminate_diagnostics"`
	GlobalDiagnostics        int `json:"global_diagnostics"`
	WarningDiagnostics       int `json:"warning_diagnostics"`
	UnitsAnalyzed            int `json:"units_analyzed"`
	Rules                    int `json:"rules"`
}

// Packages lists included and excluded packages and files.
type Packages struct {
	Included      []string          `json:"included"`
	Excluded      []ExcludedPackage `json:"excluded"`
	ExcludedFiles []ExcludedFile    `json:"excluded_files"`
}

// ExcludedPackage is a matched but unanalyzed package.
type ExcludedPackage struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// RuleResult is one rule's verdict.
type RuleResult struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Status        string   `json:"status"`
	ViolationIDs  []string `json:"violation_ids"`
	DiagnosticIDs []string `json:"diagnostic_ids"`
}

// Violation is one rule violation.
type Violation struct {
	ID            string         `json:"id"`
	RuleID        string         `json:"rule_id"`
	RuleKind      string         `json:"rule_kind"`
	Location      *Location      `json:"location"`
	Message       string         `json:"message"`
	EvidenceChain []EvidenceStep `json:"evidence_chain"`
	Remediation   Remediation    `json:"remediation"`
}

// EvidenceStep is one witness step.
type EvidenceStep struct {
	Kind string    `json:"kind"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Loc  *Location `json:"loc"`
}

// Remediation is copied from the policy rule.
type Remediation struct {
	Message string   `json:"message"`
	Suggest []string `json:"suggest"`
}

// AffectedRule names a rule a diagnostic makes indeterminate.
type AffectedRule struct {
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
}

// ReportDiagnostic is a diagnostic with its evaluator verdict.
type ReportDiagnostic struct {
	Diagnostic
	Relevance    string         `json:"relevance"`
	AffectsRules []AffectedRule `json:"affects_rules"`
	ExemptedBy   *string        `json:"exempted_by"`
	UnitOrigin   *string        `json:"unit_origin"`
}

// ViolID implements the schema README violation ID rule.
func ViolID(v Violation) string {
	var ev []any
	for _, e := range v.EvidenceChain {
		ev = append(ev, []any{e.Kind, e.From, e.To})
	}
	var file, line, col any
	if v.Location != nil {
		file, line, col = v.Location.File, v.Location.Line, v.Location.Column
	}
	return "viol:" + short(Canon([]any{v.RuleID, file, line, col, ev}))
}
