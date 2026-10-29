package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestCommentIsTextFirstAndKeepsDebtInTheExactCheck(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Report.Findings = append(p.Report.Findings, report.Finding{ID: "debt-only-id", RuleID: "accepted-only-rule", Kind: "gate", Status: "baseline"})
	body := renderComment(p, t0, "", "https://app.example/r/acme/shop/pull/7")
	mustContain(t, body, "PR impact:", "unknown", "HEAD", headB, "Run 7", "archfit/state", fixedTrustSentence)
	mustNotContain(t, body, "accepted-only-rule", "debt-only-id", "```mermaid", headDimensions, "--base origin/main")
	mustContain(t, renderState(p), "accepted-only-rule")
	tasks := decodeTasks(t, body)
	if len(tasks) != len(p.Report.AgentTasks) {
		t.Fatalf("tasks = %d, want %d", len(tasks), len(p.Report.AgentTasks))
	}
}

func TestNoTasksIsExplainedWithoutInventingRepair(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Outcome = outcome(verdict.Success, reasonHealthy)
	p.Report = loadReport(t, "healthy")
	mustNotContain(t, renderComment(p, t0, "", ""), "No required architecture change requested")
	// Synthetic renderer scenario: declared success and a present comparable accepted reference agree.
	p.Report.GateReference = &report.Reference{Status: "comparable", BaselinePresent: true}
	body := renderComment(p, t0, "", "")
	mustContain(t, body, "No automated source-repair task supplied.", "No required architecture change requested")
	if len(decodeTasks(t, body)) != 0 {
		t.Fatal("invented task")
	}
	p.Outcome = outcome(verdict.ActionRequired, reasonIdentity)
	mustNotContain(t, renderComment(p, t0, "", ""), "No required architecture change requested")
}

func TestCommentLinksToItsExactStateCheck(t *testing.T) {
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	if err := h.pub.Publish(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	state := h.checks.latest(t, headB, stateCheck)
	mustContain(t, h.comments.all()[0].Body, fmt.Sprintf("[archfit/state check %d](https://github.com/acme/shop/runs/%d)", state.ID, state.ID))
	p.Receipt.RunID = 8
	if err := h.pub.Publish(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	newer := h.comments.all()[0].Body
	p.Receipt.RunID = 7
	if err := h.pub.Publish(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if h.comments.all()[0].Body != newer {
		t.Fatal("old publication replaced exact newer identity")
	}
}

func TestExactCheckIDIsAbsentForFailedOrSupersededWrites(t *testing.T) {
	for _, method := range []string{"CreateCheckRun", "UpdateCheckRun"} {
		t.Run(method, func(t *testing.T) {
			checks := &fakeChecks{}
			receipt := Receipt{RunID: 7, RunAttempt: 1}
			run := api.CheckRun{Name: stateCheck, HeadSHA: headB, ExternalID: externalID(receipt), Summary: receiptMarker(receipt)}
			if method == "UpdateCheckRun" {
				checks.runs = []api.CheckRun{{ID: 12, Name: stateCheck, HeadSHA: headB, ExternalID: run.ExternalID, Summary: run.Summary}}
			}
			checks.failOnce(method, errors.New("write failed"))
			p := publisher{checks: checks}
			_, id, err := p.writeCheck(context.Background(), api.RepoRef{}, run, receipt)
			if err == nil || id != 0 {
				t.Fatalf("id=%d err=%v", id, err)
			}
		})
	}
	checks := &fakeChecks{runs: []api.CheckRun{{ID: 13, Name: stateCheck, HeadSHA: headB, Status: statusCompleted, Summary: receiptMarker(Receipt{RunID: 8})}}}
	p := publisher{checks: checks}
	action, id, err := p.writeCheck(context.Background(), api.RepoRef{}, api.CheckRun{Name: stateCheck, HeadSHA: headB}, Receipt{RunID: 7})
	if err != nil || id != 0 || action != actionSkippedNewer {
		t.Fatalf("action=%s id=%d err=%v", action, id, err)
	}
}

func TestRejectedReportNeverBecomesRepairAdvice(t *testing.T) {
	for _, code := range []verdict.ReasonCode{verdict.ReasonUnknownIdentity, verdict.ReasonProfileMissing, verdict.ReasonWorkflowNotApproved, verdict.ReasonEnvelopeMismatch, verdict.ReasonPolicyMismatch, verdict.ReasonBaselineMismatch, verdict.ReasonLabelsMismatch, verdict.ReasonPolicyMissing, verdict.ReasonOwnerApprovalMissing, verdict.ReasonSettingsMissing, verdict.ReasonStaleInputs, verdict.ReasonWaiverExpired, verdict.ReasonSuspended, verdict.ReasonForkUnsupported, verdict.ReasonUnsupportedEvent, verdict.ReasonReportInvalid, verdict.ReasonMissingUpload, verdict.ReasonEngineError, verdict.ReasonTargetMoved, "future_rejection"} {
		t.Run(string(code), func(t *testing.T) {
			p := blockedPublication(t, headB, 7, 1)
			p.Report.Findings[0].RuleID = "rejected_claim_rule"
			p.Report.AgentTasks[0].Goal = "rejected_claim_repair"
			p.Outcome = outcome(verdict.ActionRequired, reasonBlocking, reasonCoverage, reasonProfile, verdict.Reason{Code: code, Category: verdict.CategoryEvidence, Detail: "safe diagnostic", Next: "Restore the approved workflow and re-run."})
			original := p.Outcome
			receipt := p.Receipt
			snapshot := func() string {
				raw, err := json.Marshal(struct {
					Outcome verdict.Outcome
					Receipt Receipt
					Report  *report.Report
				}{p.Outcome, p.Receipt, p.Report})
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
			before := snapshot()
			for name, body := range map[string]string{"comment": renderComment(p, t0, "", ""), "state": renderState(p), "gate": renderGate(p)} {
				mustNotContain(t, body, "rejected_claim_rule", "rejected_claim_repair", headFindings, headDimensions, headTasks, headCommands, "Engine verdict", reasonBlocking.Next, reasonCoverage.Next, reasonProfile.Next)
				mustContain(t, body, "Restore the approved workflow and re-run.")
				if name != "comment" {
					mustContain(t, body, "Measurement profile: not available for display")
					mustNotContain(t, body, "Measurement profile: missing")
					got, ok := parseReceipt(body)
					if !ok || got != receipt {
						t.Fatalf("receipt changed: %+v", got)
					}
				}
			}
			if len(annotations(p, anyPath)) != 0 {
				t.Fatal("rejected claim annotated")
			}
			if !reflect.DeepEqual(p.Outcome, original) || p.Receipt != receipt || snapshot() != before {
				t.Fatal("presentation mutated authoritative decision")
			}
		})
	}
}

func TestApprovedIncompleteReportKeepsKnownBlockerAndIndependentGap(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Outcome = outcome(verdict.Failure, reasonBlocking, reasonCoverage, reasonProfile)
	p.Report.Decision.UnevaluatedRequiredRules = []report.UnevaluatedRule{{RuleID: "layers/no_upward_imports", Reason: "independent tool absent"}}
	for _, body := range []string{renderComment(p, t0, "", ""), renderState(p), renderGate(p)} {
		mustContain(t, body, "no_direct_b_dependency", reasonCoverage.Next)
	}
	if len(annotations(p, anyPath)) == 0 {
		t.Fatal("eligible located blocker lost")
	}
}

func TestReferenceOnlyRecoveryHasOneOwnerAction(t *testing.T) {
	// Synthetic renderer fixture, not an engine capture: reference-only gaps with matching protected digests.
	p := blockedPublication(t, headB, 7, 1)
	p.Report = loadReport(t, "healthy")
	p.Report.Verdict = "needs_attention"
	p.Report.Comparison.ConfigHash = policyDigest
	p.Report.Comparison.OriginStatus = "unknown"
	p.Report.GateReference = &report.Reference{Status: "non_comparable", BaselinePresent: true, Drift: []string{"measurement_profile"}, Reasons: []string{"measurement profile changed"}}
	p.Report.Decision.HardGates = "unmeasured"
	p.Report.Decision.UnevaluatedRequiredRules = []report.UnevaluatedRule{{RuleID: "metric_ratchets", Reason: "reference not comparable (drift: measurement_profile): 4 metric ratchets cannot be evaluated"}}
	p.Outcome = verdict.Decide(verdict.Facts{Report: p.Report, Trusted: p.Trusted, IdentityApproved: true, WorkflowApproved: true, BaselineDigest: baselineDigest, LabelsDigest: labelsDigest})
	p.Receipt = receiptFor(p.Outcome, 7, 1)
	if len(p.Outcome.Reasons) != 2 || p.Outcome.Reasons[0].Code != verdict.ReasonBaselineNotComparable || p.Outcome.Reasons[1].Code != verdict.ReasonMissingCoverage {
		t.Fatalf("unexpected fixture outcome: %+v", p.Outcome)
	}
	body := renderComment(p, t0, "", "https://app.example/r/acme/shop/pull/7")
	mustContain(t, body, "**Next:** Architecture owner:", "Implicit accepted-reference metric checks: unavailable", report.NoTaskNotice, "current PR result/recovery diagnostics, not a historical snapshot")
	mustNotContain(t, body, "CI maintainer:", "```mermaid", "Included in")
	golden(t, "reference-only-recovery.comment", body)
	state := renderState(p)
	golden(t, "reference-only-recovery.state", state)
	prelude, _, _ := strings.Cut(state, "\n### ")
	if strings.Count(prelude, ". Next:") != 0 || strings.Count(prelude, "**Next:**") != 1 {
		t.Fatalf("duplicate recovery: %s", prelude)
	}
	p.Report.Decision.UnevaluatedRequiredRules = append(p.Report.Decision.UnevaluatedRequiredRules, report.UnevaluatedRule{RuleID: "layers", Reason: "required tool absent"})
	mustContain(t, renderComment(p, t0, "", ""), "CI maintainer:")
}

// The recovery of target_moved is the author's: only a new head measures the merge.
func TestTargetMovedNamesTheAuthorAsRecoveryOwner(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Outcome = verdict.TargetMoved([]string{"a", "b"}, "0123456789abcdef", "fedcba9876543210")
	p.Receipt = receiptFor(p.Outcome, 7, 1)
	body := renderComment(p, t0, "", "")
	mustContain(t, body, "**Next:** Pull request author: ", "closes a module cycle: a, b.")
	mustNotContain(t, body, "CI maintainer:", "Architecture owner:")
	mustContain(t, renderGate(p), "Detail: a, b; 0123456789ab..fedcba987654")
}

func TestAnnotationRaceNeverSuppliesAnExactCheckID(t *testing.T) {
	checks := &fakeChecks{}
	receipt := Receipt{RunID: 7, RunAttempt: 1}
	run := api.CheckRun{Name: stateCheck, HeadSHA: headB, Status: statusCompleted, Summary: receiptMarker(receipt), Annotations: make([]api.Annotation, 51)}
	checks.afterWrite = func() {
		checks.mu.Lock()
		defer checks.mu.Unlock()
		checks.runs[0].Summary = receiptMarker(Receipt{RunID: 8, RunAttempt: 1})
	}
	p := publisher{checks: checks}
	id, err := p.createRun(context.Background(), api.RepoRef{}, run, receipt)
	if err != nil || id != 0 {
		t.Fatalf("raced check id=%d err=%v", id, err)
	}
}

func TestMissingReasonEvidenceWithholdsReport(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Outcome.Reasons = nil
	for _, body := range []string{renderComment(p, t0, "", ""), renderGate(p), renderState(p)} {
		mustNotContain(t, body, "no_direct_b_dependency", headTasks, headCommands, "Engine verdict", "No required architecture change requested")
	}
	if len(annotations(p, anyPath)) != 0 {
		t.Fatal("missing reason evidence admitted annotations")
	}
}

func TestApprovedPolicyDefectKeepsSupportedCurrentFacts(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	reason := verdict.Reason{Code: verdict.ReasonPolicyDefect, Category: verdict.CategoryGovernance, Detail: "empty_selector", Next: "Fix the approved selector in an owner-reviewed policy PR."}
	p.Outcome = outcome(verdict.ActionRequired, reason, reasonBlocking)
	p.Receipt = receiptFor(p.Outcome, 7, 1)
	for _, body := range []string{renderComment(p, t0, "", ""), renderGate(p), renderState(p)} {
		mustContain(t, body, "no_direct_b_dependency", reason.Next)
	}
	if len(annotations(p, anyPath)) == 0 {
		t.Fatal("approved policy defect erased independent current finding")
	}
}

func TestCommentDistinguishesAdvisoryCauseAndCurrentRecoveryLink(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Trusted.Settings.Gate = "advisory"
	body := renderComment(p, t0, "", "https://app.example/r/acme/shop/pull/7")
	mustContain(t, body, "Cause: Architecture rule violations.", "current PR result/recovery diagnostics, not a historical snapshot")
	mustNotContain(t, body, "Cause: Blocking findings.", "current admitted result")
	mustContain(t, renderState(p), "- Reason: `blocking_findings` (architecture)")
	if p.Outcome.Reasons[0].Code != verdict.ReasonBlockingFindings {
		t.Fatal("human copy changed structured reason")
	}
}

func TestAnnotationsRequireReportedExactLines(t *testing.T) {
	for _, tt := range []struct {
		name, file string
		line       int
		want       bool
	}{
		{"file only", "whole.go", 0, false},
		{"invalid negative line", "negative.go", -1, false},
		{"invalid empty file", "", 1, false},
		{"exact first line", "one.go", 1, true},
		{"exact higher line", "many.go", 17, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := blockedPublication(t, headB, 7, 1)
			p.Report.Findings = p.Report.Findings[:1]
			p.Report.Findings[0].Locations = []report.Location{{File: tt.file, Line: tt.line}}
			got := annotations(p, anyPath)
			if tt.want {
				if len(got) != 1 || got[0].Path != tt.file || got[0].Line != tt.line {
					t.Fatalf("annotations=%+v, want exact %s:%d", got, tt.file, tt.line)
				}
			} else if len(got) != 0 {
				t.Fatalf("unreported source line became annotation: %+v", got)
			}
			if tt.file != "" && tt.line == 0 {
				for _, body := range []string{renderComment(p, t0, "", ""), renderState(p), renderGate(p)} {
					mustContain(t, body, "`whole.go`")
					mustNotContain(t, body, "`whole.go:1`")
				}
			}
			p.Outcome = outcome(verdict.ActionRequired, reasonIdentity, reasonBlocking)
			if got := annotations(p, anyPath); len(got) != 0 {
				t.Fatalf("rejected report annotated: %+v", got)
			}
		})
	}
}

func TestTaskBudgetPrecedesDiscretionaryDetail(t *testing.T) {
	task := strings.Repeat("x", 3000)
	l := layout{head: "HEAD", findings: strings.Repeat("detail\n", 2000), tasks: []string{`{"goal":"` + task + `"}`}, hasTasks: true}
	body := l.assemble(5000)
	if len(body) > 5000 {
		t.Fatalf("body = %d bytes", len(body))
	}
	mustContain(t, body, task, tasksOpen, tasksClose)
}
