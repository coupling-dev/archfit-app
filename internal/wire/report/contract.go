// Package report is the no-source transport form of an engine state report.
package report

import "slices"

// StateSchemaV1 is the shared state contract of the known engine releases.
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// engineVersions share one verified state-schema key set, oldest first. Historical
// reports remain readable; only the manifest decides which producer tuple is approved.
var engineVersions = []string{"v3.0.0", "v3.0.1", "v3.2.0", "v3.2.2", "v3.2.3", "v3.4.0"}

// identityFrom is the first release whose state schema requires the result identity block (v3.4.0 and later).
// Reports of the releases before it carry none and still decode, to end unknown_engine_identity at the manifest.
const identityFrom = "v3.4.0"

// EngineVersions returns the engine releases whose state document Decode reads.
func EngineVersions() []string {
	return slices.Clone(engineVersions)
}

// Report is the no-source transport form of an engine state report. The decoder knows every key of the state schema
// shared by the known releases: keys listed here are kept (length-capped), the other known keys are validated and dropped (measurement,
// findings[].matched_by except MatchedByKeys, agent_tasks[].declarations, metric provenance, seam scores), and any key
// the schema does not define is a decode error.
type Report struct {
	SchemaVersion string `json:"schema_version"` // must equal StateSchemaV1
	Verdict       string `json:"verdict"`        // "healthy" | "needs_attention" | "blocked"
	// Identity is what the engine measured and under which inputs (engine v3.3.0 and later); nil only for a report
	// of a release before identityFrom. It is the engine's own statement, kept for display and for the receipt key. It decides nothing:
	// trust in inputs comes from the protected ref, never from a digest the report names.
	Identity *ResultIdentity `json:"identity,omitempty"`
	// GateReasons are the engine's own one-line reasons from its coupling gate (engine v3.3.0 and later, check only);
	// nil when there are none. Repository attention for the owner, never a decision of the App.
	GateReasons   []string             `json:"gate_reasons,omitempty"`
	Decision      Decision             `json:"decision"`
	Comparison    Comparison           `json:"comparison"`
	GateReference *Reference           `json:"gate_reference,omitempty"` // persisted-baseline comparison; nil = engine emitted none
	Dimensions    map[string]Dimension `json:"dimensions"`               // exactly the nine engine dimension keys
	Coverage      Coverage             `json:"coverage"`
	Findings      []Finding            `json:"findings"`
	AgentTasks    []AgentTask          `json:"agent_tasks"`
	Seams         []Seam               `json:"seams"`
}

// ResultIdentitySchemaV1 is the only result identity version read.
const ResultIdentitySchemaV1 = "archfit.result-identity.v1"

// ResultIdentity is the engine's result identity block. A digest the engine could not compute is "".
type ResultIdentity struct {
	SchemaVersion          string     `json:"schema_version"` // must equal ResultIdentitySchemaV1
	PolicyDigest           string     `json:"policy_digest,omitempty"`
	BaselineDigest         string     `json:"baseline_digest,omitempty"`
	LabelsDigest           string     `json:"labels_digest,omitempty"`
	PolicyRef              *PolicyRef `json:"policy_ref,omitempty"` // the ref and commit the policy was read from
	BaseSHA                string     `json:"base_sha,omitempty"`
	HeadTreeDigest         string     `json:"head_tree_digest,omitempty"`
	EngineVersion          string     `json:"engine_version"`
	MeasurementProfileHash string     `json:"measurement_profile_hash,omitempty"`
}

// PolicyRef is where the engine read the policy from: a ref and the commit it pointed at.
type PolicyRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
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
	ConfigHash         string              `json:"config_hash,omitempty"`         // bare lowercase hex SHA-256 of the raw policy bytes
	ClassificationHash string              `json:"classification_hash,omitempty"` // bare lowercase hex SHA-256 of the policy leaves that change measured facts
	ModelHash          string              `json:"model_hash,omitempty"`
	LabelsHash         string              `json:"labels_hash,omitempty"` // canonical hash of approved labels; not reproducible from raw bytes
	RubricVersion      string              `json:"rubric_version,omitempty"`
	MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
	Reasons            []string            `json:"reasons"`
	OriginStatus       string              `json:"origin_status,omitempty"` // "comparable" | "unknown"; presentation only
	OriginReasons      []string            `json:"origin_reasons,omitempty"`
}

// Reference is the engine gate's comparison with the persisted baseline (top-level gate_reference).
// The engine exposes no baseline digest: only whether it loaded a baseline, whether that one was comparable, and
// the classes of input that made it not comparable. Reasons are engine text for display; decisions read Status,
// BaselinePresent and Drift.
type Reference struct {
	Status          string   `json:"status"`             // "comparable" | "non_comparable" | "not_requested"
	BaseRef         string   `json:"base_ref,omitempty"` // "baseline"
	Reasons         []string `json:"reasons"`            // e.g. "no baseline file was loaded"
	Drift           []string `json:"drift,omitempty"`    // closed set DriftClasses; empty when comparable
	BaselinePresent bool     `json:"baseline_present"`   // the engine loaded a baseline file
}

// DriftClasses are the input classes that can make a reference not comparable (engine decision.DriftClass).
var DriftClasses = []string{
	"classification_hash", "model_hash", "labels_hash", "rubric_version", "measurement_profile", "reference_incomplete",
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
	Origin              string     `json:"origin,omitempty"` // "introduced" | "pre_existing" | "unknown", only with --base; presentation only
	// MatchedBy holds only the MatchedByKeys of the engine's matched_by: a metric ratchet names its numbers there, and a
	// dependency finding its full location count (the engine lists at most 50 locations).
	MatchedBy map[string]string `json:"matched_by,omitempty"`
}

// The matched_by keys a consumer shows. The engine writes more keys; the others stay out of the App.
const (
	MatchedByMetric    = "metric"
	MatchedByBefore    = "before"
	MatchedByAfter     = "after"
	MatchedByThreshold = "threshold"
	// MatchedByLocationsTotal is the deduplicated location count of a dependency finding, whose Locations the engine
	// caps at 50 (archfit rules_dependency.go maxFindingLocations).
	MatchedByLocationsTotal = "locations_total"
)

// MatchedByKeys are the matched_by keys Decode keeps.
var MatchedByKeys = []string{MatchedByMetric, MatchedByBefore, MatchedByAfter, MatchedByThreshold, MatchedByLocationsTotal}

// Active reports whether the finding currently blocks: a gate finding that is new or whose waiver expired.
// It is the single definition of "active gate finding" for every consumer.
func (f Finding) Active() bool {
	return f.Kind == "gate" && (f.Status == "new" || f.Status == "expired_waiver")
}

// Reverses reports whether f and g are the two directions of one module cycle: one rule, one kind, status and severity,
// and module pairs that are reversed, non-empty and distinct. Display only: each finding keeps its ID and gate effect.
func (f Finding) Reverses(g Finding) bool {
	from, to := f.Edge.From.Module, f.Edge.To.Module
	return from != "" && to != "" && from != to &&
		g.Edge.From.Module == to && g.Edge.To.Module == from &&
		f.RuleID == g.RuleID && f.Kind == g.Kind && f.Status == g.Status && f.Severity == g.Severity
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
	Status  string        `json:"status"`
	Reasons []string      `json:"reasons,omitempty"`
	Metrics []MetricDelta `json:"metrics,omitempty"`
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
	// VolatilityProvenance is where the engine took Volatility from: "declared" | "inherited" | "cascade" |
	// "undeclared"; "" from an engine that does not say. "declared" covers a functional edge that takes its source's
	// volatility when that is worse than the target's.
	VolatilityProvenance string `json:"volatility_provenance,omitempty"`
	Severity             string `json:"severity,omitempty"`
	// Policy is what the engine's policy says about the seam: "violation" | "accepted" |
	// "advisory" | "allowed" | "observed", a closed set. "" = the engine gave the seam no policy.
	Policy string `json:"policy,omitempty"`
	// Hypothesis is the engine's balancing move for the seam: "balanced" | "accept_low_volatility" |
	// "introduce_contract" | "move_functionality" | "declare_volatility" | "expected_by_role" | "follow_rule".
	// "" = the engine gave no move.
	Hypothesis string `json:"hypothesis,omitempty"`
	// Quadrant is the engine's strength and distance quadrant: "cohesive" | "loose" | "low_cohesion" | "tight"; "" when
	// absent or outside that set.
	Quadrant string `json:"quadrant,omitempty"`
	// DistributedMonolith is the engine's flag (relationship.QualifiesDistributedMonolith): a scored edge of intrusive
	// strength, or functional or symmetric from a role that is not cohesive, across a module boundary at high
	// volatility. Absent reads false.
	DistributedMonolith bool         `json:"distributed_monolith,omitempty"`
	RawDistance         SeamDistance `json:"raw_distance"` // only its basis is kept
}

// SeamDistance is what the engine measured the distance on.
type SeamDistance struct {
	// Basis is "<crossed>@<container>": the boundary the distance crosses ("module_boundary" | "ownership" |
	// "deploy_unit" | "declared_external") and the module that holds both ends, or "system". "" when no edge was
	// scored, the crossing is outside that set, or the container is not an identifier.
	Basis string `json:"basis,omitempty"`
}
