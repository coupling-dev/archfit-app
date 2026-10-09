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

// reanchorDrift are the drift classes a re-anchor crosses: a new scoring epoch or measurement profile. A policy,
// model or label change is never crossed by a re-anchor; it needs a full baseline capture.
var reanchorDrift = []string{"measurement_profile", "rubric_version"}

// Owner actions for a reference that does not compare. Next names one of them, chosen by referenceNext.
const (
	nextReanchor    = "Run `archfit baseline --reanchor` and open a baseline PR that an architecture owner approves."
	nextFullCapture = "Run `archfit baseline` and open a baseline PR that an architecture owner approves."
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
	ruleIDsCap          = 20 // rule IDs named in a Detail; feedback never cuts the reason block
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
	// Step 5.
	if Unapproved(t) {
		rs.add(ReasonOwnerApprovalMissing, strings.Join(t.ProtectedPaths, ", "))
	}
	integrity(&rs, f)
	if f.Report != nil {
		evidence(&rs, f.Report, len(t.BaselineDigests) > 0)
	}
	return rs.outcome()
}

// Unapproved is the condition of step 5: t needs an owner approval that it does not have. Only the two known passing
// values clear it, so an unknown Approval fails closed.
func Unapproved(t inputs.Trusted) bool {
	return t.Approval != inputs.ApprovalNotRequired && t.Approval != inputs.ApprovalGranted
}

// Without returns the fixed outcome for a target that has no report: code is one of ReasonMissingUpload,
// ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended, ReasonOwnerApprovalMissing.
func Without(code ReasonCode) Outcome {
	var rs reasons
	switch code {
	case ReasonMissingUpload, ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended,
		ReasonOwnerApprovalMissing:
		rs.add(code, "")
	default:
		rs.add(ReasonEngineError, "")
	}
	return rs.outcome()
}

// ApprovalMissing is Without(ReasonOwnerApprovalMissing) for a caller that holds the trusted inputs: Detail names
// t.ProtectedPaths, exactly as step 5 of Decide sets it.
func ApprovalMissing(t inputs.Trusted) Outcome {
	var rs reasons
	rs.add(ReasonOwnerApprovalMissing, strings.Join(t.ProtectedPaths, ", "))
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

	if ReferenceConflicts(r.GateReference, baselineTrusted) {
		rs.add(ReasonBaselineNotComparable, referenceDetail(r.GateReference))
		rs.setNext(ReasonBaselineNotComparable, referenceNext(r.GateReference))
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

// ruleIDs names the first ruleIDsCap rules, then counts the rest: the Detail sits in the reason block, which
// feedback never cuts, so it must not grow with the report.
func ruleIDs(rules []report.UnevaluatedRule) string {
	n := min(len(rules), ruleIDsCap)
	ids := make([]string, 0, n)
	for _, r := range rules[:n] {
		ids = append(ids, r.RuleID)
	}
	out := strings.Join(ids, ", ")
	if rest := len(rules) - n; rest > 0 {
		out += " and " + strconv.Itoa(rest) + " more"
	}
	return out
}

// ReferenceConflicts reports whether the engine's gate reference contradicts the trusted baseline (verdict step 7a).
// A trusted baseline needs a reference that compares. And the engine loaded a baseline exactly when the App trusts
// one, so any other pairing fails closed. A nil reference conflicts only with a trusted baseline.
func ReferenceConflicts(ref *report.Reference, baselineTrusted bool) bool {
	if ref == nil {
		return baselineTrusted
	}
	return baselineTrusted && ref.Status != refComparable || ref.BaselinePresent != baselineTrusted
}

// referenceNext names the owner action from the drift classes only. A class a re-anchor may cross (a scoring epoch
// or a measurement profile) needs a re-anchor; any other class, or a reference with no classes, needs a full capture.
func referenceNext(ref *report.Reference) string {
	if ref == nil || len(ref.Drift) == 0 {
		return nextFullCapture
	}
	for _, class := range ref.Drift {
		if !slices.Contains(reanchorDrift, class) {
			return nextFullCapture
		}
	}
	return nextReanchor
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
