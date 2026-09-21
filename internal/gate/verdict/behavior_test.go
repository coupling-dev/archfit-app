package verdict

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestUnknownIdentityNeverComparesEqual(t *testing.T) {
	profileLess := &report.Reference{Status: refNonComparable, BaseRef: "baseline", Reasons: []string{"baseline carries no measurement profile"}}
	snapshot := &report.Reference{Status: refNonComparable, BaseRef: "baseline", Reasons: []string{"baseline is a v2.3.0 numerical snapshot"}}

	wantCodes(t, decide(t, with(identityOff, noProfile, gateRef(profileLess))),
		ActionRequired, ReasonProfileMissing, ReasonBaselineNotComparable)
	wantCodes(t, decide(t, with(gateRef(profileLess))), ActionRequired, ReasonBaselineNotComparable)
	wantCodes(t, decide(t, with(gateRef(snapshot))), ActionRequired, ReasonBaselineNotComparable)
}

func TestHeadEditsCannotWeakenTheGate(t *testing.T) {
	heads := []struct {
		name string
		code ReasonCode
		edit func(*Facts)
	}{
		{"policy", ReasonPolicyMismatch, policyMismatch},
		{"baseline", ReasonBaselineMismatch, baselineMismatch},
		{"labels", ReasonLabelsMismatch, labelsMismatch},
	}
	for _, h := range heads {
		t.Run(h.name, func(t *testing.T) {
			wantCodes(t, decide(t, with(h.edit, approvalMissing, engine(verdictBlocked, gatesFail))),
				ActionRequired, ReasonOwnerApprovalMissing, h.code, ReasonBlockingFindings)
			wantCodes(t, decide(t, with(h.edit, approvalMissing)), ActionRequired, ReasonOwnerApprovalMissing, h.code)
			wantCodes(t, decide(t, with(h.edit)), ActionRequired, h.code)
		})
	}

	t.Run("no blanket re-baseline", func(t *testing.T) {
		noBaselineUsed := func(f *Facts) {
			f.BaselineDigest = ""
			f.Report.GateReference = &report.Reference{Status: refNonComparable, Reasons: []string{noBaselineLoaded}}
		}
		wantCodes(t, decide(t, with(noBaselineUsed)), ActionRequired, ReasonBaselineMismatch, ReasonBaselineNotComparable)
		wantCodes(t, decide(t, with(gateRef(nil))), ActionRequired, ReasonBaselineNotComparable)
		wantCodes(t, decide(t, with(nonComparable)), ActionRequired, ReasonBaselineNotComparable)
	})

	t.Run("detail names the protected paths", func(t *testing.T) {
		f := with(approvalMissing, func(f *Facts) { f.Trusted.ProtectedPaths = []string{".archfit.yaml", ".archfit-baseline.json"} })
		if got, want := decide(t, f).Reasons[0].Detail, ".archfit.yaml, .archfit-baseline.json"; got != want {
			t.Errorf("detail = %q, want %q", got, want)
		}
	})
}

func TestAtomicPolicyPullRequestWaitsForOwner(t *testing.T) {
	wantCodes(t, decide(t, with(policyMismatch, approvalMissing)),
		ActionRequired, ReasonOwnerApprovalMissing, ReasonPolicyMismatch)

	wantCodes(t, decide(t, with(policyMismatch, func(f *Facts) {
		f.Trusted.Approval = inputs.ApprovalGranted
		f.Trusted.PolicyDigests = []string{basePolicy, headPolicy}
	})), Success, ReasonHealthy)
}

func TestIntegrityFailureIsNotClearedByApproval(t *testing.T) {
	f := with(func(f *Facts) {
		f.Trusted.Approval = inputs.ApprovalGranted
		f.Trusted.PolicyDigests = []string{basePolicy, headPolicy}
		f.Report.Comparison.ConfigHash = strings.Repeat("9", 64)
	})
	wantCodes(t, decide(t, f), ActionRequired, ReasonPolicyMismatch)
}

func TestMissingEvidenceNeverPasses(t *testing.T) {
	runCases(t, []decideCase{
		{"unmeasured", with(engine(verdictHealthy, gatesUnmeasured)), ActionRequired, []ReasonCode{ReasonMissingCoverage}},
		{"unevaluated rule", with(engine(verdictAttention, gatesPass), unevaluated), ActionRequired, []ReasonCode{ReasonMissingCoverage}},
		{"out-of-enum verdict", with(engine("error", gatesPass)), ActionRequired, []ReasonCode{ReasonEngineError}},
		{"nil report", with(nilReport, identityOff), ActionRequired, []ReasonCode{ReasonReportInvalid}},
		{"fork", with(problem(ingest.ProblemFork)), ActionRequired, []ReasonCode{ReasonForkUnsupported}},
		{"workflow_run", with(problem(ingest.ProblemUnsupportedEvent)), ActionRequired, []ReasonCode{ReasonUnsupportedEvent}},
	})
	for _, code := range withoutCodes {
		wantCodes(t, Without(code), ActionRequired, code)
	}
}

func TestNonComparableBaselineNeedsOwnerPR(t *testing.T) {
	for _, reason := range []string{"model hash differs", "measurement profile differs"} {
		o := decide(t, with(gateRef(&report.Reference{Status: refNonComparable, BaseRef: "baseline", Reasons: []string{reason}})))
		wantCodes(t, o, ActionRequired, ReasonBaselineNotComparable)
		r := o.Reasons[0]
		if !strings.Contains(r.Next, "baseline PR") || !strings.Contains(r.Next, "owner") {
			t.Errorf("next = %q", r.Next)
		}
		if r.Detail != reason {
			t.Errorf("detail = %q, want %q", r.Detail, reason)
		}
	}
}

func TestBlockerKeepsUnevaluatedRules(t *testing.T) {
	f, _ := fixtureCase(t, "blocker-and-unevaluated")
	o := decide(t, f)
	wantCodes(t, o, Failure, ReasonBlockingFindings, ReasonMissingCoverage)
	if r, _ := find(o, ReasonMissingCoverage); r.Detail != "layers/no_upward_imports" {
		t.Errorf("detail = %q, want the rule ID", r.Detail)
	}
}

func TestOriginNeverExempts(t *testing.T) {
	f, _ := fixtureCase(t, "real-blocked-no-baseline")
	r := f.Report
	if len(r.AgentTasks) != 1 || len(r.Findings) == 0 {
		t.Fatalf("fixture has %d tasks and %d findings", len(r.AgentTasks), len(r.Findings))
	}
	r.AgentTasks[0].RuleID, r.AgentTasks[0].Origin = "bc/coupling_gate", "unknown"
	for i := range r.Findings {
		r.Findings[i].Status = "baseline"
	}
	wantCodes(t, decide(t, f), Failure, ReasonBlockingFindings)
}

func TestSuspensionIsItsOwnCategory(t *testing.T) {
	const reason = "Trial ended 2026-10-20. Choose a plan in Archfit settings."
	stop := func(f *Facts) {
		f.Trusted.Suspended = true
		f.Trusted.Entitlement = inputs.Entitlement{Reason: reason}
	}
	check := func(o Outcome, next string) {
		t.Helper()
		if r := o.Reasons[0]; r.Category != CategorySubscription || r.Next != next {
			t.Errorf("suspended = %+v", r)
		}
	}

	o := decide(t, with(stop))
	wantCodes(t, o, ActionRequired, ReasonSuspended)
	check(o, reason)

	o = decide(t, with(stop, engine(verdictBlocked, gatesFail)))
	wantCodes(t, o, ActionRequired, ReasonSuspended, ReasonBlockingFindings)
	check(o, reason)
	if o.Reasons[1].Category != CategoryArchitecture {
		t.Errorf("second reason = %+v", o.Reasons[1])
	}

	check(decide(t, with(suspended)), "Archfit subscription inactive.")

	wantCodes(t, decide(t, with(func(f *Facts) { f.Trusted.Entitlement = inputs.Entitlement{} })), Success, ReasonHealthy)
	wantCodes(t, decide(t, with(engine(verdictBlocked, gatesFail))), Failure, ReasonBlockingFindings)
}
