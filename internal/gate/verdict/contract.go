// Package verdict maps the gate's facts to one outcome. It is pure: no I/O, no clock.
package verdict

import (
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// Conclusion is the only vocabulary the gate can publish. neutral and skipped are not representable.
type Conclusion string

const (
	Success        Conclusion = "success"
	Failure        Conclusion = "failure"
	ActionRequired Conclusion = "action_required"
)

// Category keeps architecture, evidence, governance and subscription outcomes from masquerading as each other.
type Category string

const (
	CategoryArchitecture Category = "architecture"
	CategoryEvidence     Category = "evidence"
	CategoryGovernance   Category = "governance"
	CategorySubscription Category = "subscription"
)

type ReasonCode string

const (
	ReasonHealthy               ReasonCode = "healthy"
	ReasonAttentionOnly         ReasonCode = "attention_only" // needs_attention, and nothing blocks
	ReasonBlockingFindings      ReasonCode = "blocking_findings"
	ReasonEngineError           ReasonCode = "engine_error"
	ReasonMissingCoverage       ReasonCode = "missing_required_coverage"
	ReasonBaselineNotComparable ReasonCode = "baseline_not_comparable"
	ReasonBaselineMismatch      ReasonCode = "baseline_mismatch"
	ReasonLabelsMismatch        ReasonCode = "labels_mismatch"
	ReasonPolicyMismatch        ReasonCode = "policy_mismatch"
	ReasonPolicyMissing         ReasonCode = "policy_missing" // settings merged, no policy on the protected ref yet
	ReasonPolicyDefect          ReasonCode = "policy_defect"  // a required rule's selector matches nothing
	ReasonUnknownIdentity       ReasonCode = "unknown_engine_identity"
	ReasonProfileMissing        ReasonCode = "measurement_profile_missing"
	ReasonWorkflowNotApproved   ReasonCode = "workflow_not_approved"
	ReasonOwnerApprovalMissing  ReasonCode = "owner_approval_missing"
	ReasonSettingsMissing       ReasonCode = "settings_missing"
	ReasonForkUnsupported       ReasonCode = "fork_unsupported"
	ReasonUnsupportedEvent      ReasonCode = "unsupported_event"
	ReasonEnvelopeMismatch      ReasonCode = "envelope_mismatch"
	ReasonReportInvalid         ReasonCode = "report_invalid"
	ReasonMissingUpload         ReasonCode = "missing_upload"
	ReasonStaleInputs           ReasonCode = "stale_inputs"
	ReasonWaiverExpired         ReasonCode = "waiver_expired"
	ReasonSuspended             ReasonCode = "subscription_suspended"
)

// ClassifyDisplayReason classifies recorded reason metadata for presentation only, never a gate decision.
// A missing/unknown code or mismatched category is unclassified and must not supply current repair guidance.
func ClassifyDisplayReason(code ReasonCode, category Category) (reportDerived, known bool) {
	entry, ok := lookup(code)
	if !ok || entry.category != category {
		return false, false
	}
	switch code {
	case ReasonHealthy, ReasonAttentionOnly, ReasonBlockingFindings, ReasonBaselineNotComparable, ReasonMissingCoverage, ReasonPolicyDefect:
		return true, true
	}
	return false, true
}

// ReportDerivedAction reports a reason that comes from the report itself and makes a check ask for action: missing
// required coverage, a baseline that is not comparable, a policy defect. The PR comment and the Pull page show the
// report under such a result, so the owner approval it was computed under matters as it does for success.
func ReportDerivedAction(code ReasonCode) bool {
	e, ok := lookup(code)
	if !ok || e.conclusion != ActionRequired {
		return false
	}
	derived, _ := ClassifyDisplayReason(code, e.category)
	return derived
}

type Reason struct {
	Code     ReasonCode
	Category Category
	Detail   string // IDs and digests only
	Next     string // the recovery action
}

// Outcome is the gate decision. Reasons[0] determines Conclusion; the rest are preserved for feedback.
type Outcome struct {
	Conclusion Conclusion
	Reasons    []Reason
}

// Facts is the closed input of the decision. Report is nil when no usable report exists.
type Facts struct {
	Problems         []ingest.Problem
	BaselineDigest   string // claimed by the envelope; "" = none used
	LabelsDigest     string // claimed by the envelope; "" = none used
	Report           *report.Report
	IdentityApproved bool
	WorkflowApproved bool
	Trusted          inputs.Trusted
}
