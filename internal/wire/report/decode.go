package report

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// originValues are the engine's origin of a finding or task; they exist only in a report made with --base.
var originValues = []string{"introduced", "pre_existing", "unknown"}

// Decode strictly parses payload as the no-source transport of the state document engineVersion emits. It
// fails on: an engineVersion outside EngineVersions, size over MaxBytes, a key the state schema
// does not define, wrong schema_version, an enum value outside its set, strings over the per-field caps,
// absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte, engineVersion string) (Report, error) {
	if !slices.Contains(engineVersions, engineVersion) {
		// The version is input: it is never echoed.
		return Report{}, locError("engine_version", "is not a release this decoder reads")
	}
	if len(payload) > MaxBytes {
		return Report{}, locError("payload", "is larger than MaxBytes")
	}
	if !utf8.Valid(payload) {
		return Report{}, locError("payload", "is not valid UTF-8")
	}
	if err := prepass(payload, reflect.TypeFor[shadowReport]()); err != nil {
		return Report{}, err
	}
	var shadow shadowReport
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&shadow); err != nil {
		// The pre-pass has checked keys and types: what is left is malformed JSON. The json error text may
		// quote the payload, so it is not passed on.
		return Report{}, locError("payload", "invalid JSON")
	}
	c := &checker{ids: map[string]struct{}{}}
	r := c.report(&shadow)
	if err := c.err(); err != nil {
		return Report{}, err
	}
	return r, nil
}

func (c *checker) report(s *shadowReport) Report {
	r := Report{SchemaVersion: c.str("schema_version", s.SchemaVersion, classVersion)}
	if s.SchemaVersion != nil && r.SchemaVersion != StateSchemaV1 {
		c.fail(phaseVersion, "schema_version", "must equal "+StateSchemaV1)
	}
	r.Verdict = c.enum("verdict", s.Verdict, "healthy", "needs_attention", "blocked")
	r.Identity = optional(c, "identity", s.Identity, c.identity)
	// The coupling gate writes one summary line, then one line per newly introduced seam it blocks on: as many as the
	// seams the report may hold, plus the summary.
	r.GateReasons = stringsOf(c, "gate_reasons", s.GateReasons, maxSeams+1, false, classFindingText)
	r.Decision = object(c, "decision", s.Decision, c.decision)
	r.Comparison = object(c, "comparison", s.Comparison, c.comparison)
	r.GateReference = optional(c, "gate_reference", s.GateReference, c.reference)
	r.Dimensions = c.dimensions(s.Dimensions)
	r.Coverage = object(c, "coverage", s.Coverage, c.coverage)
	r.Findings = list(c, "findings", s.Findings, maxFindings, true, c.finding)
	r.AgentTasks = list(c, "agent_tasks", s.AgentTasks, maxAgentTasks, true, c.task)
	r.Seams = list(c, "seams", s.Seams, maxSeams, true, c.seam)
	return r
}

func (c *checker) identity(s *shadowIdentity) ResultIdentity {
	id := ResultIdentity{
		SchemaVersion:          c.str("schema_version", s.SchemaVersion, classVersion),
		PolicyDigest:           c.opt("policy_digest", s.PolicyDigest, classToken),
		BaselineDigest:         c.opt("baseline_digest", s.BaselineDigest, classToken),
		LabelsDigest:           c.opt("labels_digest", s.LabelsDigest, classToken),
		BaseSHA:                c.opt("base_sha", s.BaseSHA, classToken),
		HeadTreeDigest:         c.opt("head_tree_digest", s.HeadTreeDigest, classToken),
		EngineVersion:          c.str("engine_version", s.EngineVersion, classVersion),
		MeasurementProfileHash: c.opt("measurement_profile_hash", s.MeasurementProfileHash, classToken),
		PolicyRef: optional(c, "policy_ref", s.PolicyRef, func(p *shadowPolicyRef) PolicyRef {
			return PolicyRef{Ref: c.str("ref", p.Ref, classIdentifier), SHA: c.str("sha", p.SHA, classToken)}
		}),
	}
	if s.SchemaVersion != nil && id.SchemaVersion != ResultIdentitySchemaV1 {
		c.fail(phaseVersion, "schema_version", "must equal "+ResultIdentitySchemaV1)
	}
	return id
}

func (c *checker) decision(s *shadowDecision) Decision {
	return Decision{
		HardGates: c.enum("hard_gates", s.HardGates, "pass", "fail", "unmeasured"),
		UnevaluatedRequiredRules: list(c, "unevaluated_required_rules", s.UnevaluatedRequiredRules, maxUnevaluatedRules, false,
			func(u *shadowUnevaluatedRule) UnevaluatedRule {
				return UnevaluatedRule{RuleID: c.str("rule_id", u.RuleID, classIdentifier), Reason: c.str("reason", u.Reason, classText)}
			}),
		ActiveBlockers:      c.count("active_blockers", s.ActiveBlockers, math.MaxInt),
		AttentionDimensions: c.count("attention_dimensions", s.AttentionDimensions, math.MaxInt),
		UnknownDimensions:   c.count("unknown_dimensions", s.UnknownDimensions, math.MaxInt),
	}
}

func (c *checker) comparisonStatus(s *string) string {
	return c.enum("status", s, "not_requested", "comparable", "non_comparable")
}

func (c *checker) comparison(s *shadowComparison) Comparison {
	return Comparison{
		Status:             c.comparisonStatus(s.Status),
		BaseRef:            c.opt("base_ref", s.BaseRef, classIdentifier),
		ConfigHash:         c.opt("config_hash", s.ConfigHash, classConfigHash),
		ClassificationHash: c.opt("classification_hash", s.ClassificationHash, classConfigHash),
		ModelHash:          c.opt("model_hash", s.ModelHash, classVersion),
		LabelsHash:         c.opt("labels_hash", s.LabelsHash, classVersion),
		RubricVersion:      c.opt("rubric_version", s.RubricVersion, classVersion),
		MeasurementProfile: optional(c, "measurement_profile", s.MeasurementProfile, c.profile),
		Reasons:            stringsOf(c, "reasons", s.Reasons, maxSmallList, true, classText),
		OriginStatus:       c.optEnum("origin_status", s.OriginStatus, "comparable", "unknown"),
		OriginReasons:      stringsOf(c, "origin_reasons", s.OriginReasons, maxSmallList, false, classText),
	}
}

// reference keeps status, base_ref, reasons, drift and baseline_present of gate_reference; its other comparison
// keys are dropped.
func (c *checker) reference(s *shadowComparison) Reference {
	ref := Reference{
		Status:  c.comparisonStatus(s.Status),
		BaseRef: c.opt("base_ref", s.BaseRef, classIdentifier),
		Reasons: stringsOf(c, "reasons", s.Reasons, maxSmallList, true, classText),
		Drift:   stringsOf(c, "drift", s.Drift, len(DriftClasses), false, classToken),
	}
	for i := range s.Drift {
		if !slices.Contains(DriftClasses, s.Drift[i]) {
			c.fail(phaseEnum, "drift", "must hold only "+strings.Join(DriftClasses, ", "))
		}
	}
	if s.BaselinePresent != nil {
		ref.BaselinePresent = *s.BaselinePresent
	}
	return ref
}

func (c *checker) profile(s *shadowProfile) MeasurementProfile {
	return MeasurementProfile{
		Version:      c.str("version", s.Version, classVersion),
		SettingsHash: c.str("settings_hash", s.SettingsHash, classVersion),
		Producers: list(c, "producers", s.Producers, maxSmallList, true, func(p *shadowProducer) Producer {
			out := Producer{
				Tool:             c.str("tool", p.Tool, classIdentifier),
				SemanticsVersion: c.str("semantics_version", p.SemanticsVersion, classVersion),
				Status:           c.str("status", p.Status, classToken),
				ToolVersion:      c.opt("tool_version", p.ToolVersion, classToolVersion),
				PartialBasis:     p.PartialBasis,
			}
			if p.PartialBasis != "" {
				c.enum("partial_basis", &p.PartialBasis, "unresolved_specifiers", "degraded_precision")
			}
			return out
		}),
		Unknowns: stringsOf(c, "unknowns", s.Unknowns, maxSmallList, true, classVersion),
	}
}

// dimensions requires exactly the nine engine dimensions, each named after its key.
func (c *checker) dimensions(s map[string]shadowDimension) map[string]Dimension {
	if s == nil {
		c.fail(phaseRequired, "dimensions", "is required")
		return nil
	}
	names := dimensionNames()
	out := make(map[string]Dimension, len(names))
	c.path = append(c.path, seg{"dimensions", -1})
	for _, name := range names {
		d, ok := s[name]
		if !ok {
			c.fail(phaseRequired, name, "is required")
			continue
		}
		out[name] = *optional(c, name, &d, c.dimension)
		if d.Name != nil && *d.Name != name {
			c.fail(phaseRequired, name+".name", "must equal its key")
		}
	}
	c.path = c.path[:len(c.path)-1]
	if len(s) > len(out) {
		c.fail(phaseRequired, "dimensions", "must hold exactly the nine engine dimensions")
	}
	return out
}

func (c *checker) dimension(s *shadowDimension) Dimension {
	return Dimension{
		Name:       c.str("name", s.Name, classIdentifier),
		Owner:      c.str("owner", s.Owner, classIdentifier),
		Status:     c.enum("status", s.Status, "measured", "partial", "unmeasured"),
		Confidence: c.str("confidence", s.Confidence, classToken),
		Gate:       c.str("gate", s.Gate, classToken),
		Coverage: object(c, "coverage", s.Coverage, func(v *shadowDimensionCoverage) DimensionCoverage {
			return DimensionCoverage{
				Basis:    c.str("basis", v.Basis, classText),
				Observed: c.count("observed", v.Observed, math.MaxInt),
				Total:    c.count("total", v.Total, math.MaxInt),
			}
		}),
		Metrics: list(c, "metrics", s.Metrics, maxDimensionFacts, true, func(m *shadowMetric) Metric {
			return Metric{
				Name:  c.str("name", m.Name, classIdentifier),
				Value: c.number("value", m.Value),
				Unit:  c.str("unit", m.Unit, classIdentifier),
			}
		}),
		Findings: list(c, "findings", s.Findings, maxFindingRefs, true, func(f *shadowFindingRef) FindingRef {
			return FindingRef{
				ID:       c.str("id", f.ID, classIdentifier),
				RuleID:   c.str("rule_id", f.RuleID, classIdentifier),
				Kind:     c.str("kind", f.Kind, classToken),
				Severity: c.str("severity", f.Severity, classToken),
				Status:   c.str("status", f.Status, classToken),
			}
		}),
		Unknown: list(c, "unknown", s.Unknown, maxDimensionFacts, true, func(u *shadowUnknownFact) UnknownFact {
			return UnknownFact{
				Fact:   c.str("fact", u.Fact, classText),
				Reason: c.str("reason", u.Reason, classText),
				Owner:  c.str("owner", u.Owner, classIdentifier),
			}
		}),
		Delta: optional(c, "delta", s.Delta, c.delta),
	}
}

func (c *checker) delta(s *shadowDelta) Delta {
	return Delta{
		Status:  c.str("status", s.Status, classToken),
		Reasons: stringsOf(c, "reasons", s.Reasons, maxSmallList, false, classText),
		Metrics: list(c, "metrics", s.Metrics, maxDimensionFacts, false, func(m *shadowMetricDelta) MetricDelta {
			return MetricDelta{
				Name:   c.str("name", m.Name, classIdentifier),
				Before: c.number("before", m.Before),
				After:  c.number("after", m.After),
				Change: c.number("change", m.Change),
			}
		}),
	}
}

func (c *checker) coverage(s *shadowCoverage) Coverage {
	return Coverage{
		Measured:   c.count("measured", s.Measured, math.MaxInt),
		Partial:    c.count("partial", s.Partial, math.MaxInt),
		Unmeasured: c.count("unmeasured", s.Unmeasured, math.MaxInt),
		Tools: list(c, "tools", s.Tools, maxSmallList, true, func(t *shadowToolCoverage) ToolCoverage {
			stringsOf(c, "details", t.Details, maxSmallList, false, classText) // validated, not kept
			return ToolCoverage{
				Tool:   c.str("tool", t.Tool, classIdentifier),
				Status: c.str("status", t.Status, classToken),
				Reason: c.opt("reason", t.Reason, classText),
			}
		}),
	}
}

// finding keeps only the MatchedByKeys of matched_by.
func (c *checker) finding(s *shadowFinding) Finding {
	f := Finding{
		ID:         c.str("id", s.ID, classIdentifier),
		Kind:       c.str("kind", s.Kind, classToken),
		RuleID:     c.str("rule_id", s.RuleID, classIdentifier),
		Status:     c.str("status", s.Status, classToken),
		Severity:   c.str("severity", s.Severity, classToken),
		Confidence: c.str("confidence", s.Confidence, classToken),
		Edge: object(c, "edge", s.Edge, func(e *shadowEdge) Edge {
			return Edge{
				From: object(c, "from", e.From, c.endpoint),
				To:   object(c, "to", e.To, c.endpoint),
				Kind: c.str("kind", e.Kind, classToken),
			}
		}),
		Locations: list(c, "locations", s.Locations, maxLocations, true, func(l *shadowLocation) Location {
			return Location{File: c.str("file", l.File, classPath), Line: c.count("line", l.Line, maxLine)}
		}),
		Why:                 c.str("why", s.Why, classFindingText),
		Constraint:          c.str("constraint", s.Constraint, classFindingText),
		AllowedAlternatives: stringsOf(c, "allowed_alternatives", s.AllowedAlternatives, maxAlternatives, false, classText),
		Origin:              c.optEnum("origin", s.Origin, originValues...),
		MatchedBy:           c.matchedBy(s.MatchedBy),
	}
	if s.ID != nil {
		if _, dup := c.ids[f.ID]; dup {
			c.fail(phaseDuplicate, "id", "repeats an earlier finding id")
		}
		c.ids[f.ID] = struct{}{}
	}
	return f
}

func (c *checker) endpoint(s *shadowEndpoint) Endpoint {
	return Endpoint{
		Module: c.str("module", s.Module, classIdentifier),
		Path:   c.str("path", s.Path, classEndpointPath),
	}
}

// task drops declarations: they hold source identifiers.
func (c *checker) task(s *shadowTask) AgentTask {
	return AgentTask{
		FindingID:   c.str("finding_id", s.FindingID, classIdentifier),
		RuleID:      c.str("rule_id", s.RuleID, classIdentifier),
		RepairKind:  c.enum("repair_kind", s.RepairKind, "code_change", "needs_owner_decision"),
		Goal:        c.str("goal", s.Goal, classTaskText),
		Constraints: stringsOf(c, "constraints", s.Constraints, maxTaskConstraints, true, classTaskText),
		Files:       stringsOf(c, "files", s.Files, maxTaskFiles, true, classPath),
		Validation:  stringsOf(c, "validation", s.Validation, maxTaskValidation, true, classCommand),
		Origin:      c.optEnum("origin", s.Origin, originValues...),
	}
}

// matchedBy keeps the MatchedByKeys, each value checked as text; every other key is dropped unchecked, as before.
// locations_total is kept only as a non-negative integer and dropped otherwise: a count of locations is shown only,
// and LocationsTotal falls back to the listed locations, so a bad count never fails the report.
func (c *checker) matchedBy(m map[string]string) map[string]string {
	var kept map[string]string
	for _, key := range MatchedByKeys {
		v, ok := m[key]
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(v); key == MatchedByLocationsTotal && (err != nil || n < 0) {
			continue
		}
		if kept == nil {
			kept = map[string]string{}
		}
		kept[key] = c.opt("matched_by."+key, v, classText)
	}
	return kept
}

// Display-only seam readings closed here, from the engine's constants (archfit internal/relationship/seam.go and
// coupling/coupling.go): anything else reads as absent and never fails the report.
var (
	quadrants = []string{"cohesive", "loose", "low_cohesion", "tight"}
	crossings = []string{"module_boundary", "ownership", "deploy_unit", "declared_external"}
)

// distanceBasis keeps "<crossed>@<container>" when the crossing is one the engine names and the container is an
// identifier, else "": a basis is shown only, so it never fails the report.
func distanceBasis(basis string) string {
	crossed, container, ok := strings.Cut(basis, "@")
	if !ok || known(crossed, crossings...) == "" {
		return ""
	}
	if _, rule := stringRule(container, classIdentifier); rule != "" {
		return ""
	}
	return basis
}

// seam keeps the identity, the edge count, the three coupling dimensions, the engine's policy status, its balancing
// move and its balance reading (quadrant, distributed monolith, what the distance crosses); scores, owners and evidence
// are dropped.
func (c *checker) seam(s *shadowSeam) Seam {
	return Seam{
		ID:                   c.str("id", s.ID, classIdentifier),
		FromModule:           c.str("from_module", s.FromModule, classIdentifier),
		ToModule:             c.str("to_module", s.ToModule, classIdentifier),
		Edges:                c.count("edges", s.Edges, math.MaxInt),
		Strength:             c.str("strength", s.Strength, classToken),
		Distance:             c.str("distance", s.Distance, classToken),
		Volatility:           c.str("volatility", s.Volatility, classToken),
		Severity:             c.opt("severity", s.Severity, classToken),
		VolatilityProvenance: c.opt("volatility_provenance", s.VolatilityProvenance, classToken),
		Policy:               c.optEnum("policy", s.Policy, "violation", "accepted", "advisory", "allowed", "observed"),
		Hypothesis:           c.opt("hypothesis", s.Hypothesis, classToken),
		Quadrant:             known(s.Quadrant, quadrants...),
		DistributedMonolith:  s.DistributedMonolith,
		RawDistance:          SeamDistance{Basis: distanceBasis(s.RawDistance.Basis)},
	}
}
