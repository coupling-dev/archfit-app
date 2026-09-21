package report

// Shadow types mirror the whole v2.3.1 state schema (engine revision f8877d25ba36d84d3780071580d23486e3d794d0),
// dropped keys included. Kept required keys are pointers or slices so that nil means absent: the pre-pass
// rejects null, so nothing else produces nil. Fields marked dropped are type-checked and never copied.

// optInt marks the only values that may be null: seams[].scores.p10 and p90.
type optInt int

type shadowReport struct {
	SchemaVersion *string                    `json:"schema_version"`
	Verdict       *string                    `json:"verdict"`
	Decision      *shadowDecision            `json:"decision"`
	Comparison    *shadowComparison          `json:"comparison"`
	GateReference *shadowComparison          `json:"gate_reference"`
	Measurement   shadowMeasurement          `json:"measurement"` // dropped
	Dimensions    map[string]shadowDimension `json:"dimensions"`
	Coverage      *shadowCoverage            `json:"coverage"`
	Findings      []shadowFinding            `json:"findings"`
	AgentTasks    []shadowTask               `json:"agent_tasks"`
	Seams         []shadowSeam               `json:"seams"`
}

type shadowDecision struct {
	HardGates                *string                 `json:"hard_gates"`
	UnevaluatedRequiredRules []shadowUnevaluatedRule `json:"unevaluated_required_rules"`
	ActiveBlockers           *int                    `json:"active_blockers"`
	AttentionDimensions      *int                    `json:"attention_dimensions"`
	UnknownDimensions        *int                    `json:"unknown_dimensions"`
}

type shadowUnevaluatedRule struct {
	RuleID *string `json:"rule_id"`
	Reason *string `json:"reason"`
}

// shadowComparison is both comparison and gate_reference: the engine uses one shape for the two. For
// gate_reference only status, base_ref and reasons are kept.
type shadowComparison struct {
	Status             *string        `json:"status"`
	BaseRef            string         `json:"base_ref"`
	ConfigHash         string         `json:"config_hash"`
	ModelHash          string         `json:"model_hash"`
	LabelsHash         string         `json:"labels_hash"`
	RubricVersion      string         `json:"rubric_version"`
	MeasurementProfile *shadowProfile `json:"measurement_profile"`
	Reasons            []string       `json:"reasons"`
	TaskOriginStatus   string         `json:"task_origin_status"`
	TaskOriginReasons  []string       `json:"task_origin_reasons"`
}

type shadowProfile struct {
	Version      *string          `json:"version"`
	SettingsHash *string          `json:"settings_hash"`
	Producers    []shadowProducer `json:"producers"`
	Unknowns     []string         `json:"unknowns"`
}

type shadowProducer struct {
	Tool             *string `json:"tool"`
	SemanticsVersion *string `json:"semantics_version"`
	Status           *string `json:"status"`
	ToolVersion      string  `json:"tool_version"`
	PartialBasis     string  `json:"partial_basis"`
}

// shadowMeasurement is dropped as a whole.
type shadowMeasurement struct {
	SourceRef     string            `json:"source_ref"`
	HistoryDepth  int               `json:"history_depth"`
	HistoryWindow string            `json:"history_window"`
	ToolVersions  map[string]string `json:"tool_versions"`
}

type shadowFinding struct {
	ID                  *string           `json:"id"`
	Kind                *string           `json:"kind"`
	RuleID              *string           `json:"rule_id"`
	Status              *string           `json:"status"`
	Severity            *string           `json:"severity"`
	Confidence          *string           `json:"confidence"`
	Edge                *shadowEdge       `json:"edge"`
	MatchedBy           map[string]string `json:"matched_by"` // dropped
	Locations           []shadowLocation  `json:"locations"`
	Why                 *string           `json:"why"`
	Constraint          *string           `json:"constraint"`
	AllowedAlternatives []string          `json:"allowed_alternatives"`
}

type shadowEdge struct {
	From *shadowEndpoint `json:"from"`
	To   *shadowEndpoint `json:"to"`
	Kind *string         `json:"kind"`
}

type shadowEndpoint struct {
	Module *string `json:"module"`
	Path   *string `json:"path"`
}

type shadowLocation struct {
	File *string `json:"file"`
	Line *int    `json:"line"`
}

type shadowTask struct {
	FindingID    *string            `json:"finding_id"`
	RuleID       *string            `json:"rule_id"`
	RepairKind   *string            `json:"repair_kind"`
	Goal         *string            `json:"goal"`
	Constraints  []string           `json:"constraints"`
	Files        []string           `json:"files"`
	Validation   []string           `json:"validation"`
	Origin       string             `json:"origin"`
	Declarations []shadowSyntaxFact `json:"declarations"` // dropped
}

// shadowSyntaxFact is dropped as a whole. Name is a source identifier.
type shadowSyntaxFact struct {
	Language  string `json:"language"`
	File      string `json:"file"`
	Module    string `json:"module"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Framework string `json:"framework"`
	Exported  bool   `json:"exported"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Count     int    `json:"count"`
}

type shadowDimension struct {
	Name       *string                  `json:"name"`
	Owner      *string                  `json:"owner"`
	Status     *string                  `json:"status"`
	Confidence *string                  `json:"confidence"`
	Gate       *string                  `json:"gate"`
	Coverage   *shadowDimensionCoverage `json:"coverage"`
	Metrics    []shadowMetric           `json:"metrics"`
	Findings   []shadowFindingRef       `json:"findings"`
	Unknown    []shadowUnknownFact      `json:"unknown"`
	Delta      *shadowDelta             `json:"delta"`
}

type shadowDimensionCoverage struct {
	Basis    *string `json:"basis"`
	Observed *int    `json:"observed"`
	Total    *int    `json:"total"`
}

type shadowMetric struct {
	Name        *string           `json:"name"`
	Value       *float64          `json:"value"`
	Unit        *string           `json:"unit"`
	Denominator shadowDenominator `json:"denominator"` // dropped
	Provenance  []string          `json:"provenance"`  // dropped
}

type shadowDenominator struct {
	Observed int `json:"observed"`
	Total    int `json:"total"`
}

type shadowFindingRef struct {
	ID       *string `json:"id"`
	RuleID   *string `json:"rule_id"`
	Kind     *string `json:"kind"`
	Severity *string `json:"severity"`
	Status   *string `json:"status"`
}

type shadowUnknownFact struct {
	Fact   *string `json:"fact"`
	Reason *string `json:"reason"`
	Owner  *string `json:"owner"`
}

type shadowDelta struct {
	Status           *string             `json:"status"`
	Reasons          []string            `json:"reasons"`
	Metrics          []shadowMetricDelta `json:"metrics"`
	NewFindings      []string            `json:"new_findings"`
	ResolvedFindings []string            `json:"resolved_findings"`
}

type shadowMetricDelta struct {
	Name   *string  `json:"name"`
	Before *float64 `json:"before"`
	After  *float64 `json:"after"`
	Change *float64 `json:"change"`
}

type shadowCoverage struct {
	Measured   *int                 `json:"measured"`
	Partial    *int                 `json:"partial"`
	Unmeasured *int                 `json:"unmeasured"`
	Tools      []shadowToolCoverage `json:"tools"`
}

type shadowToolCoverage struct {
	Tool   *string `json:"tool"`
	Status *string `json:"status"`
	Reason string  `json:"reason"`
}

type shadowSeam struct {
	ID         *string `json:"id"`
	FromModule *string `json:"from_module"`
	ToModule   *string `json:"to_module"`
	Edges      *int    `json:"edges"`
	Strength   *string `json:"strength"`
	Distance   *string `json:"distance"`
	Volatility *string `json:"volatility"`
	Severity   string  `json:"severity"`

	// Dropped.
	ScoredEdges          int               `json:"scored_edges"`
	AbstainedEdges       int               `json:"abstained_edges"`
	CriticalEdges        int               `json:"critical_edges"`
	HighOrWorseEdges     int               `json:"high_or_worse_edges"`
	CriticalSharePct     int               `json:"critical_share_pct"`
	HighOrWorseSharePct  int               `json:"high_or_worse_share_pct"`
	VolatilityProvenance string            `json:"volatility_provenance"`
	Quadrant             string            `json:"quadrant"`
	LabelEvidenceHash    string            `json:"label_evidence_hash"`
	Confidence           string            `json:"confidence"`
	RoleExpectation      string            `json:"role_expectation"`
	Hypothesis           string            `json:"hypothesis"`
	Labels               []string          `json:"labels"`
	DistributedMonolith  bool              `json:"distributed_monolith"`
	RawDistance          shadowRawDistance `json:"raw_distance"`
	Scores               shadowScores      `json:"scores"`
}

type shadowRawDistance struct {
	Level             string `json:"level"`
	Basis             string `json:"basis"`
	FromOwner         string `json:"from_owner"`
	ToOwner           string `json:"to_owner"`
	FromDeployUnit    string `json:"from_deploy_unit"`
	ToDeployUnit      string `json:"to_deploy_unit"`
	SameOwner         bool   `json:"same_owner"`
	SameDeployUnit    bool   `json:"same_deploy_unit"`
	BoundaryCrossings int    `json:"boundary_crossings"`
	SharedAncestor    int    `json:"shared_ancestor"`
}

type shadowScores struct {
	N      int     `json:"n"`
	Min    int     `json:"min"`
	Median int     `json:"median"`
	Max    int     `json:"max"`
	P10    *optInt `json:"p10"`
	P90    *optInt `json:"p90"`
	Mean   float64 `json:"mean"`
}
