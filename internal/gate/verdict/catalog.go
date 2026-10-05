package verdict

// entry is one row of the reason catalog: the category, the conclusion when the reason is first,
// and the recovery action.
type entry struct {
	category   Category
	conclusion Conclusion
	next       string
}

const suspendedNext = "Archfit subscription inactive."

// lookup is the reason catalog and the only place a Conclusion is produced from.
// It is a switch, not a map, so the package holds no state.
func lookup(code ReasonCode) (entry, bool) {
	switch code {
	case ReasonHealthy:
		return entry{CategoryArchitecture, Success, ""}, true
	case ReasonAttentionOnly:
		return entry{CategoryArchitecture, Success, "Advisories never block. Review the listed dimensions and diagnostics on the archfit/state check."}, true
	case ReasonBlockingFindings:
		return entry{CategoryArchitecture, Failure, "Fix the blocking findings and run the validation command."}, true
	case ReasonEngineError:
		return entry{CategoryEvidence, ActionRequired, "Read the Archfit workflow log, use the pinned engine image, re-run the workflow."}, true
	case ReasonMissingCoverage:
		return entry{CategoryEvidence, ActionRequired, "Supply the missing evidence for the listed rules. Approval cannot clear this."}, true
	case ReasonBaselineNotComparable:
		return entry{CategoryEvidence, ActionRequired, "Open a baseline PR and get an architecture owner's approval."}, true
	case ReasonBaselineMismatch:
		return entry{CategoryEvidence, ActionRequired, "Run the workflow with the baseline from the base branch."}, true
	case ReasonLabelsMismatch:
		return entry{CategoryEvidence, ActionRequired, "Run the workflow with the labels file from the base branch."}, true
	case ReasonPolicyMismatch:
		return entry{CategoryEvidence, ActionRequired, "Run the workflow with the policy from the base branch; re-run if the base changed."}, true
	case ReasonPolicyMissing:
		return entry{CategoryGovernance, ActionRequired, "Merge the Archfit policy PR."}, true
	case ReasonPolicyDefect:
		return entry{CategoryGovernance, ActionRequired, "Fix the selectors of the listed rules in an owner-approved policy PR, or mark an intentional guard rule guard: true."}, true
	case ReasonUnknownIdentity:
		return entry{CategoryEvidence, ActionRequired, "Use the engine image pinned by the Archfit onboarding workflow."}, true
	case ReasonProfileMissing:
		return entry{CategoryEvidence, ActionRequired, "Use engine v2.3.1 or later; the report carries no measurement profile."}, true
	case ReasonWorkflowNotApproved:
		return entry{CategoryEvidence, ActionRequired, "Upload from the workflow named in `.archfit-app.yaml`."}, true
	case ReasonOwnerApprovalMissing:
		return entry{CategoryGovernance, ActionRequired, "An architecture owner must approve this exact commit."}, true
	case ReasonSettingsMissing:
		return entry{CategoryGovernance, ActionRequired, "Merge the Archfit onboarding PR to add `.archfit-app.yaml`."}, true
	case ReasonForkUnsupported:
		return entry{CategoryEvidence, ActionRequired, "Push the branch to this repository, or use the documented ruleset bypass."}, true
	case ReasonUnsupportedEvent:
		return entry{CategoryEvidence, ActionRequired, "Run Archfit on `pull_request` or on `push` to the default branch."}, true
	case ReasonEnvelopeMismatch:
		return entry{CategoryEvidence, ActionRequired, "Re-run the Archfit workflow; the upload did not match the GitHub run."}, true
	case ReasonReportInvalid:
		return entry{CategoryEvidence, ActionRequired, "Upload the unmodified engine report; use the pinned action version."}, true
	case ReasonMissingUpload:
		return entry{CategoryEvidence, ActionRequired, "No report arrived. Check the Archfit workflow run and re-run it."}, true
	case ReasonStaleInputs:
		return entry{CategoryEvidence, ActionRequired, "Policy or baseline changed on the base branch. A re-run was requested; re-run the Archfit workflow if none appears."}, true
	case ReasonWaiverExpired:
		return entry{CategoryGovernance, ActionRequired, "A waiver expired. A re-run was requested; renew the waiver by owner-approved PR or fix the finding."}, true
	case ReasonSuspended:
		return entry{CategorySubscription, ActionRequired, suspendedNext}, true
	}
	return entry{}, false
}

// reasons is an ordered list that holds a ReasonCode at most once.
type reasons []Reason

func (rs *reasons) has(code ReasonCode) bool {
	for _, r := range *rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

func (rs *reasons) add(code ReasonCode, detail string) {
	if rs.has(code) {
		return
	}
	e, _ := lookup(code)
	*rs = append(*rs, Reason{Code: code, Category: e.category, Detail: detail, Next: e.next})
}

// outcome closes the decision: the conclusion is the catalog conclusion of the first reason.
// A code outside the catalog cannot be added by this package; if one ever is, it ends action_required.
func (rs reasons) outcome() Outcome {
	if len(rs) == 0 {
		rs.add(ReasonEngineError, "")
	}
	e, ok := lookup(rs[0].Code)
	if !ok {
		return Outcome{Conclusion: ActionRequired, Reasons: rs}
	}
	return Outcome{Conclusion: e.conclusion, Reasons: rs}
}
