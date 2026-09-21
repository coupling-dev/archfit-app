package verdict

import (
	"slices"
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
)

const (
	noDigest        = "none"
	noGateReference = "no gate reference"

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
		rs.add(ReasonSuspended, "")
		if t.Entitlement.Reason != "" {
			rs[0].Next = t.Entitlement.Reason
		}
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
	case f.Report != nil && !trusted(t.PolicyDigests, f.Report.Comparison.ConfigHash):
		rs.add(ReasonPolicyMismatch, f.Report.Comparison.ConfigHash)
	}
	if mismatch(t.BaselineDigests, f.BaselineDigest) {
		rs.add(ReasonBaselineMismatch, claimed(f.BaselineDigest))
	}
	if mismatch(t.LabelsDigests, f.LabelsDigest) {
		rs.add(ReasonLabelsMismatch, claimed(f.LabelsDigest))
	}
}

// evidence is steps 7 and 8: comparability, coverage, then the engine decision.
func evidence(rs *reasons, r *report.Report, baselineTrusted bool) {
	verdict, gates := r.Verdict, r.Decision.HardGates
	rules := r.Decision.UnevaluatedRequiredRules

	if baselineTrusted && (r.GateReference == nil || r.GateReference.Status != refComparable) {
		rs.add(ReasonBaselineNotComparable, referenceDetail(r.GateReference))
	}
	if !slices.Contains([]string{verdictHealthy, verdictAttention, verdictBlocked}, verdict) ||
		!slices.Contains([]string{gatesPass, gatesFail, gatesUnmeasured}, gates) {
		rs.add(ReasonEngineError, "verdict="+cut(verdict, engineValueCap)+" hard_gates="+cut(gates, engineValueCap))
	}
	if gates == gatesUnmeasured || (len(rules) > 0 && gates != gatesFail) {
		rs.add(ReasonMissingCoverage, ruleIDs(rules))
	}
	if gates == gatesFail || verdict == verdictBlocked {
		rs.add(ReasonBlockingFindings, "")
		if len(rules) > 0 {
			rs.add(ReasonMissingCoverage, ruleIDs(rules)) // no repeat when step 7c already hit
		}
	}
	if len(*rs) == 0 {
		rs.add(ReasonHealthy, "")
	}
}

// trusted reports whether digest is in list. Exact string equality; an empty digest never matches.
func trusted(list []string, digest string) bool {
	return digest != "" && slices.Contains(list, digest)
}

// mismatch is the digest predicate for a producer-claimed digest. Both sides empty is a match.
func mismatch(list []string, digest string) bool {
	if len(list) == 0 {
		return digest != ""
	}
	return !trusted(list, digest)
}

func claimed(digest string) string {
	if digest == "" {
		return noDigest
	}
	return digest
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
