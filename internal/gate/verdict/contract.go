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
