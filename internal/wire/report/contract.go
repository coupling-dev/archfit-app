// Package report is the no-source transport form of an engine state report.
package report

import "slices"

// StateSchemaV1 is the engine state contract accepted (engine v2.3.1, archfit.state.schema.json).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// engineVersions are the engine releases Decode reads, oldest first. A shadow field tagged `since:"<release>"`
// is a key that release added; every other key is in all of them. v2.4.0 adds rule types, finding kinds and a
// config key, but no state key, so its key set is the v2.3.1 key set. v2.4.1 only enumerates the rule types in
// its config schema; its state schema is byte-identical, so its key set is the v2.3.1 key set too. v2.3.1 stays
// listed after its manifest rows are gone: every synthetic corpus fixture is a v2.3.1 document.
var engineVersions = []string{"v2.3.1", "v2.4.0", "v2.4.1"}

// EngineVersions returns the engine releases whose state document Decode reads, oldest first. Each emits
// StateSchemaV1; a later release only adds optional keys, so the last key set holds every earlier one.
func EngineVersions() []string {
	return slices.Clone(engineVersions)
}

// Report is the no-source transport form of an engine state report. The decoder knows every key of the
// v2.3.1 state schema: keys listed here are kept (length-capped), the other known keys are validated and
// dropped (measurement, findings[].matched_by, agent_tasks[].declarations, metric provenance, seam scores),
// and any key the schema does not define is a decode error.
type Report struct {
	SchemaVersion string               `json:"schema_version"` // must equal StateSchemaV1
	Verdict       string               `json:"verdict"`        // "healthy" | "needs_attention" | "blocked"
	Decision      Decision             `json:"decision"`
	Comparison    Comparison           `json:"comparison"`
	GateReference *Reference           `json:"gate_reference,omitempty"` // persisted-baseline comparison; nil = engine emitted none
	Dimensions    map[string]Dimension `json:"dimensions"`               // exactly the nine engine dimension keys
	Coverage      Coverage             `json:"coverage"`
	Findings      []Finding            `json:"findings"`
	AgentTasks    []AgentTask          `json:"agent_tasks"`
	Seams         []Seam               `json:"seams"`
}

// Decision is the engine's structured gate decision. The process exit code is not part of a report:
// healthy, needs_attention and blocked are Report.Verdict; an engine error produces no report at all.
type Decision struct {
	HardGates                string            `json:"hard_gates"` // "pass" | "fail" | "unmeasured"
	UnevaluatedRequiredRules []UnevaluatedRule `json:"unevaluated_required_rules,omitempty"`
	ActiveBlockers           int               `json:"active_blockers"`
	AttentionDimensions      int               `json:"attention_dimensions"`
	UnknownDimensions        int               `json:"unknown_dimensions"`
}

type UnevaluatedRule struct {
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
}

// Comparison binds the report to the inputs the engine actually used (report-only --base comparison).
type Comparison struct {
	Status             string              `json:"status"` // "not_requested" | "comparable" | "non_comparable"
	BaseRef            string              `json:"base_ref,omitempty"`
	ConfigHash         string              `json:"config_hash,omitempty"` // bare lowercase hex SHA-256 of the raw policy bytes
	ModelHash          string              `json:"model_hash,omitempty"`
	LabelsHash         string              `json:"labels_hash,omitempty"` // canonical hash of approved labels; not reproducible from raw bytes
	RubricVersion      string              `json:"rubric_version,omitempty"`
	MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
	Reasons            []string            `json:"reasons"`
	TaskOriginStatus   string              `json:"task_origin_status,omitempty"`
	TaskOriginReasons  []string            `json:"task_origin_reasons,omitempty"`
}

// Reference is the engine gate's comparison with the persisted baseline (top-level gate_reference).
// The engine exposes no baseline digest: only whether the baseline it loaded was comparable, and why not.
type Reference struct {
	Status  string   `json:"status"`             // "comparable" | "non_comparable" | "not_requested"
	BaseRef string   `json:"base_ref,omitempty"` // "baseline"
	Reasons []string `json:"reasons"`            // e.g. "no baseline file was loaded"
}

type MeasurementProfile struct {
	Version      string     `json:"version"`
	SettingsHash string     `json:"settings_hash"`
	Producers    []Producer `json:"producers"`
	Unknowns     []string   `json:"unknowns"`
}

type Producer struct {
	Tool             string `json:"tool"`
	SemanticsVersion string `json:"semantics_version"`
	Status           string `json:"status"` // "ok" | "partial" | "absent" | "disabled" | "timed out"
	ToolVersion      string `json:"tool_version,omitempty"`
	PartialBasis     string `json:"partial_basis,omitempty"` // "unresolved_specifiers" | "degraded_precision"
}

type Finding struct {
	ID                  string     `json:"id"`   // 32-hex fingerprint, unique per report
	Kind                string     `json:"kind"` // "gate" | "advisory"
	RuleID              string     `json:"rule_id"`
	Status              string     `json:"status"`   // "new" | "baseline" | "waived" | "expired_waiver" | "fixed"
	Severity            string     `json:"severity"` // "critical" | "high" | "medium" | "low"
	Confidence          string     `json:"confidence"`
	Edge                Edge       `json:"edge"`
	Locations           []Location `json:"locations"`  // may be empty; never invent one
	Why                 string     `json:"why"`        // engine-authored, single line, length-capped
	Constraint          string     `json:"constraint"` // engine-authored, single line, length-capped
	AllowedAlternatives []string   `json:"allowed_alternatives,omitempty"`
}

// Active reports whether the finding currently blocks: a gate finding that is new or whose waiver expired.
// It is the single definition of "active gate finding" for every consumer.
func (f Finding) Active() bool {
	return f.Kind == "gate" && (f.Status == "new" || f.Status == "expired_waiver")
}

type Edge struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
	Kind string   `json:"kind"`
}

type Endpoint struct {
	Module string `json:"module"` // may be ""
	Path   string `json:"path"`   // may be ""
}

type Location struct {
	File string `json:"file"`
	Line int    `json:"line"` // 0 = whole file
}

type AgentTask struct {
	FindingID   string   `json:"finding_id"`
	RuleID      string   `json:"rule_id"`
	RepairKind  string   `json:"repair_kind"` // "code_change" | "needs_owner_decision"
	Goal        string   `json:"goal"`
	Constraints []string `json:"constraints"`
	Files       []string `json:"files"`
	Validation  []string `json:"validation"`       // shell command strings, replayed verbatim; never contain --refresh
	Origin      string   `json:"origin,omitempty"` // "introduced" | "pre_existing" | "unknown"; presentation only
}

// Dimension mirrors the engine's per-dimension state. Completeness is Status; measurement trust is not an
// engine field: in v1 every fact is customer-attested and feedback says so.
type Dimension struct {
	Name       string            `json:"name"`
	Owner      string            `json:"owner"`
	Status     string            `json:"status"` // "measured" | "partial" | "unmeasured"
	Confidence string            `json:"confidence"`
	Gate       string            `json:"gate"` // "pass" | "warn" | "fail" | "not_applicable"
	Coverage   DimensionCoverage `json:"coverage"`
	Metrics    []Metric          `json:"metrics"`
	Findings   []FindingRef      `json:"findings"`
	Unknown    []UnknownFact     `json:"unknown"`
	Delta      *Delta            `json:"delta,omitempty"`
}

type DimensionCoverage struct {
	Basis    string `json:"basis"`
	Observed int    `json:"observed"`
	Total    int    `json:"total"`
}

type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type FindingRef struct {
	ID       string `json:"id"`
	RuleID   string `json:"rule_id"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
}

type UnknownFact struct {
	Fact   string `json:"fact"`
	Reason string `json:"reason"`
	Owner  string `json:"owner"`
}

type Delta struct {
	Status           string        `json:"status"`
	Reasons          []string      `json:"reasons,omitempty"`
	Metrics          []MetricDelta `json:"metrics,omitempty"`
	NewFindings      []string      `json:"new_findings,omitempty"`
	ResolvedFindings []string      `json:"resolved_findings,omitempty"`
}

type MetricDelta struct {
	Name   string  `json:"name"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Change float64 `json:"change"`
}

type Coverage struct {
	Measured   int            `json:"measured"`
	Partial    int            `json:"partial"`
	Unmeasured int            `json:"unmeasured"`
	Tools      []ToolCoverage `json:"tools"`
}

type ToolCoverage struct {
	Tool   string `json:"tool"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Seam is an observed module-to-module relationship. Only these fields are kept.
type Seam struct {
	ID         string `json:"id"`
	FromModule string `json:"from_module"`
	ToModule   string `json:"to_module"`
	Edges      int    `json:"edges"` // observed dependency count
	Strength   string `json:"strength"`
	Distance   string `json:"distance"`
	Volatility string `json:"volatility"`
	Severity   string `json:"severity,omitempty"`
}
