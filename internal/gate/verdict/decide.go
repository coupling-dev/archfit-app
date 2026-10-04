package verdict

import (
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// Engine values of wire/report that the decision reads.
const (
	verdictHealthy   = "healthy"
	verdictAttention = "needs_attention"
	verdictBlocked   = "blocked"

	gatesPass       = "pass"
	gatesFail       = "fail"
	gatesUnmeasured = "unmeasured"

	refComparable = "comparable"

	// deadSelectorPrefix starts the engine's unevaluated-rule reason for a required rule whose selector matches
	// nothing: a defect of the approved policy, not missing evidence.
	deadSelectorPrefix = "selector matches nothing"
)

// Engine values that only the attention_only Detail reads; the conclusion never depends on them.
const (
	dimensionMeasured   = "measured"
	kindAdvisory        = "advisory"
	statusNew           = "new"
	statusExpiredWaiver = "expired_waiver"
)

const (
	noDigest        = "none"
	noGateReference = "no gate reference"
	noDimension     = "none"

	engineValueCap      = 32
	referenceReasonsCap = 3
	referenceDetailCap  = 200
)

// Decide is a pure function. It checks, in order: subscription, settings, source and producer eligibility,
// governance (owner approval), input integrity, evidence completeness, then the engine decision.
// The step table of gate/verdict is the normative order.
func Decide(f Facts) Outcome {
	var rs reasons
	t := f.Trusted

	// Step 1.
	if t.Suspended {
		rs.suspend(t.Entitlement)
	}
	// Step 2.
	if !t.SettingsFound {
		rs.add(ReasonSettingsMissing, "")
	}
	eligibility(&rs, f)
	if !t.SettingsFound {
		// No trusted inputs to compare with: steps 4 to 8 are skipped.
		return rs.outcome()
	}

	// Step 4.
	if !f.WorkflowApproved {
		rs.add(ReasonWorkflowNotApproved, "")
	}
	if !f.IdentityApproved && f.Report != nil {
		if f.Report.Comparison.MeasurementProfile == nil {
			rs.add(ReasonProfileMissing, "")
		} else {
			rs.add(ReasonUnknownIdentity, "")
		}
	}
	// Step 5. Only the two known passing values clear it: an unknown Approval fails closed.
	if t.Approval != inputs.ApprovalNotRequired && t.Approval != inputs.ApprovalGranted {
		rs.add(ReasonOwnerApprovalMissing, strings.Join(t.ProtectedPaths, ", "))
	}
	integrity(&rs, f)
	if f.Report != nil {
		evidence(&rs, f.Report, len(t.BaselineDigests) > 0)
	}
	return rs.outcome()
}

// Without returns the fixed outcome for a target that has no report: code is one of
// ReasonMissingUpload, ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended.
func Without(code ReasonCode) Outcome {
	var rs reasons
	switch code {
	case ReasonMissingUpload, ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended:
		rs.add(code, "")
	default:
		rs.add(ReasonEngineError, "")
	}
	return rs.outcome()
}

// Suspended is Without(ReasonSuspended) for a caller that holds the entitlement record: Next is
// e.Reason when it is not empty, exactly as step 1 of Decide sets it.
func Suspended(e inputs.Entitlement) Outcome {
	var rs reasons
	rs.suspend(e)
	return rs.outcome()
}

// suspend is step 1. It is always the first reason.
func (rs *reasons) suspend(e inputs.Entitlement) {
	rs.add(ReasonSuspended, "")
	if e.Reason != "" {
		(*rs)[0].Next = e.Reason
	}
}

// eligibility is step 3. Problems are a set; a value outside the ingest constants is an envelope mismatch.
func eligibility(rs *reasons, f Facts) {
	var fork, event, envelope, invalid bool
	for _, p := range f.Problems {
		switch p {
		case ingest.ProblemFork:
			fork = true
		case ingest.ProblemUnsupportedEvent:
			event = true
		case ingest.ProblemReportInvalid:
			invalid = true
		default:
			envelope = true
		}
	}
	if fork {
		rs.add(ReasonForkUnsupported, "")
	}
	if event {
		rs.add(ReasonUnsupportedEvent, "")
	}
	if envelope {
		rs.add(ReasonEnvelopeMismatch, "")
	}
	if invalid || f.Report == nil {
		rs.add(ReasonReportInvalid, "")
	}
}

// integrity is step 6. It never reads Approval: approval cannot clear a digest mismatch.
func integrity(rs *reasons, f Facts) {
	t := f.Trusted
	switch {
	case len(t.PolicyDigests) == 0:
		rs.add(ReasonPolicyMissing, "")
	case f.Report != nil && !inputs.Accepts(t.PolicyDigests, f.Report.Comparison.ConfigHash):
		rs.add(ReasonPolicyMismatch, f.Report.Comparison.ConfigHash)
	}
	if !inputs.Accepts(t.BaselineDigests, f.BaselineDigest) {
		rs.add(ReasonBaselineMismatch, claimed(f.BaselineDigest))
	}
	if !inputs.Accepts(t.LabelsDigests, f.LabelsDigest) {
		rs.add(ReasonLabelsMismatch, claimed(f.LabelsDigest))
	}
}

// evidence is steps 7 and 8: comparability, coverage, then the engine decision.
func evidence(rs *reasons, r *report.Report, baselineTrusted bool) {
	verdict, gates := r.Verdict, r.Decision.HardGates
	defects, missing := splitRules(r.Decision.UnevaluatedRequiredRules)

	if baselineTrusted && (r.GateReference == nil || r.GateReference.Status != refComparable) {
		rs.add(ReasonBaselineNotComparable, referenceDetail(r.GateReference))
	}
	if !slices.Contains([]string{verdictHealthy, verdictAttention, verdictBlocked}, verdict) ||
		!slices.Contains([]string{gatesPass, gatesFail, gatesUnmeasured}, gates) {
		rs.add(ReasonEngineError, "verdict="+cut(verdict, engineValueCap)+" hard_gates="+cut(gates, engineValueCap))
	}
	if gates != gatesFail {
		if len(defects) > 0 {
			rs.add(ReasonPolicyDefect, ruleIDs(defects))
		}
		// Unmeasured gates that no listed rule explains still lack evidence.
		if len(missing) > 0 || (gates == gatesUnmeasured && len(defects) == 0) {
			rs.add(ReasonMissingCoverage, ruleIDs(missing))
		}
	}
	if gates == gatesFail || verdict == verdictBlocked {
		rs.add(ReasonBlockingFindings, "")
		// No repeat when step 7 already hit.
		if len(defects) > 0 {
			rs.add(ReasonPolicyDefect, ruleIDs(defects))
		}
		if len(missing) > 0 {
			rs.add(ReasonMissingCoverage, ruleIDs(missing))
		}
	}
	switch {
	case len(*rs) > 0:
	case verdict == verdictAttention:
		rs.add(ReasonAttentionOnly, attentionDetail(r))
	default:
		rs.add(ReasonHealthy, "")
	}
}

// attentionDetail names what needs attention: the dimensions the report does not show as measured, in the
// engine's fixed order of its nine keys, and the count of active diagnostics (advisories that are new or whose
// waiver expired). Only those fixed keys and a count are written, so the Detail stays identifiers only.
func attentionDetail(r *report.Report) string {
	var dims []string
	for _, key := range [...]string{
		"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift",
	} {
		if d, ok := r.Dimensions[key]; !ok || d.Status != dimensionMeasured {
			dims = append(dims, key)
		}
	}
	diagnostics := 0
	for _, f := range r.Findings {
		if f.Kind == kindAdvisory && (f.Status == statusNew || f.Status == statusExpiredWaiver) {
			diagnostics++
		}
	}
	listed := noDimension
	if len(dims) > 0 {
		listed = strings.Join(dims, ", ")
	}
	return "dimensions not fully measured: " + listed + "; active diagnostics: " + strconv.Itoa(diagnostics)
}

func claimed(digest string) string {
	if digest == "" {
		return noDigest
	}
	return digest
}

// splitRules separates the unevaluated required rules whose selector matches nothing (a policy defect) from the
// rest (missing evidence). Only the exact engine prefix counts; every other reason fails closed as missing evidence.
func splitRules(rules []report.UnevaluatedRule) (defects, missing []report.UnevaluatedRule) {
	for _, r := range rules {
		if strings.HasPrefix(r.Reason, deadSelectorPrefix) {
			defects = append(defects, r)
		} else {
			missing = append(missing, r)
		}
	}
	return defects, missing
}

func ruleIDs(rules []report.UnevaluatedRule) string {
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.RuleID)
	}
	return strings.Join(ids, ", ")
}

func referenceDetail(ref *report.Reference) string {
	if ref == nil {
		return noGateReference
	}
	return cut(strings.Join(ref.Reasons[:min(len(ref.Reasons), referenceReasonsCap)], "; "), referenceDetailCap)
}

// cut keeps the first n characters of s.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
