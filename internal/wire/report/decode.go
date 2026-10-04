package report

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"unicode/utf8"
)

// Decode strictly parses payload as the no-source transport of the state document engineVersion emits. It
// fails on: an engineVersion outside EngineVersions, size over MaxBytes, a key that release's state schema
// does not define, wrong schema_version, an enum value outside its set, strings over the per-field caps,
// absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte, engineVersion string) (Report, error) {
	version := slices.Index(engineVersions, engineVersion)
	if version < 0 {
		// The version is input: it is never echoed.
		return Report{}, locError("engine_version", "is not a release this decoder reads")
	}
	if len(payload) > MaxBytes {
		return Report{}, locError("payload", "is larger than MaxBytes")
	}
	if !utf8.Valid(payload) {
		return Report{}, locError("payload", "is not valid UTF-8")
	}
	if err := prepass(payload, reflect.TypeFor[shadowReport](), version); err != nil {
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
		ModelHash:          c.opt("model_hash", s.ModelHash, classVersion),
		LabelsHash:         c.opt("labels_hash", s.LabelsHash, classVersion),
		RubricVersion:      c.opt("rubric_version", s.RubricVersion, classVersion),
		MeasurementProfile: optional(c, "measurement_profile", s.MeasurementProfile, c.profile),
		Reasons:            stringsOf(c, "reasons", s.Reasons, maxSmallList, true, classText),
		TaskOriginStatus:   c.opt("task_origin_status", s.TaskOriginStatus, classToken),
		TaskOriginReasons:  stringsOf(c, "task_origin_reasons", s.TaskOriginReasons, maxSmallList, false, classText),
	}
}

// reference keeps status, base_ref and reasons of gate_reference; its other comparison keys are dropped.
func (c *checker) reference(s *shadowComparison) Reference {
	return Reference{
		Status:  c.comparisonStatus(s.Status),
		BaseRef: c.opt("base_ref", s.BaseRef, classIdentifier),
		Reasons: stringsOf(c, "reasons", s.Reasons, maxSmallList, true, classText),
	}
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
		NewFindings:      stringsOf(c, "new_findings", s.NewFindings, maxFindingRefs, false, classIdentifier),
		ResolvedFindings: stringsOf(c, "resolved_findings", s.ResolvedFindings, maxFindingRefs, false, classIdentifier),
	}
}

func (c *checker) coverage(s *shadowCoverage) Coverage {
	return Coverage{
		Measured:   c.count("measured", s.Measured, math.MaxInt),
		Partial:    c.count("partial", s.Partial, math.MaxInt),
		Unmeasured: c.count("unmeasured", s.Unmeasured, math.MaxInt),
		Tools: list(c, "tools", s.Tools, maxSmallList, true, func(t *shadowToolCoverage) ToolCoverage {
			return ToolCoverage{
				Tool:   c.str("tool", t.Tool, classIdentifier),
				Status: c.str("status", t.Status, classToken),
				Reason: c.opt("reason", t.Reason, classText),
			}
		}),
	}
}

// finding drops matched_by.
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
		Why:                 c.str("why", s.Why, classText),
		Constraint:          c.str("constraint", s.Constraint, classText),
		AllowedAlternatives: stringsOf(c, "allowed_alternatives", s.AllowedAlternatives, maxAlternatives, false, classText),
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
		Origin:      c.opt("origin", s.Origin, classToken),
	}
}

// seam keeps the identity, the edge count and the three coupling dimensions; scores and evidence are dropped.
func (c *checker) seam(s *shadowSeam) Seam {
	return Seam{
		ID:         c.str("id", s.ID, classIdentifier),
		FromModule: c.str("from_module", s.FromModule, classIdentifier),
		ToModule:   c.str("to_module", s.ToModule, classIdentifier),
		Edges:      c.count("edges", s.Edges, math.MaxInt),
		Strength:   c.str("strength", s.Strength, classToken),
		Distance:   c.str("distance", s.Distance, classToken),
		Volatility: c.str("volatility", s.Volatility, classToken),
		Severity:   c.opt("severity", s.Severity, classToken),
	}
}
