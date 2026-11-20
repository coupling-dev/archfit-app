package gate

import (
	"context"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/ingest"
	"github.com/coupling-dev/archfit-app/internal/gate/proposals"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

func (w *world4) wantOwnerCheck(p *pull, approval string) {
	w.t.Helper()
	run, ok := w.latestGate(p.head)
	if !ok || run.Status != "completed" || run.Conclusion != "success" || run.ExternalID != "approval:"+approval {
		w.t.Fatalf("archfit/gate = %+v, want the owner's approval success", run)
	}
	policy := w.gh.files[fileKey(shopName, p.head, policyPath)]
	workflow := w.gh.files[fileKey(shopName, p.head, ingest.WorkflowPath)]
	for _, want := range []string{"olivia", "#7", p.head, policy3.Digest(policy), records.FileDigest(workflow), "not an engine pass"} {
		if !strings.Contains(run.Summary, want) {
			w.t.Errorf("summary %q does not contain %q", run.Summary, want)
		}
	}
	if _, ok := proposals.ProposalOf(run); ok {
		w.t.Error("approval check mistaken for a proposal check")
	}
}

func TestOnboardingApprovalSurvivesUnknownUploads(t *testing.T) {
	for _, rerun := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh upload", true: "rerun"}[rerun], func(t *testing.T) {
			w := newWorld4(t)
			w.onboarding()
			p, s := w.onboardingPull(7)
			s.result = w.result("policy_differs", p.head)
			if rerun {
				w.send4(s)
			}
			out, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst, Pull: 7})
			if err != nil {
				t.Fatal(err)
			}
			w.wantOwnerCheck(p, out.Approval)
			if rerun {
				w.wantStatus(w.rerunUpload(p, s), "unknown", "")
			} else {
				w.wantStatus(w.send4(s), "unknown", "")
			}
			w.wantOwnerCheck(p, out.Approval)

			w.push(p)
			moved := w.onboardingHead(p)
			moved.result = w.result("policy_differs", p.head)
			w.wantStatus(w.send4(moved), "unknown", reasonFirstPolicy)
			if run, ok := w.latestGate(p.head); !ok || run.Conclusion != "action_required" ||
				strings.HasPrefix(run.ExternalID, "approval:") {
				t.Errorf("new head's check = %+v, want unknown without owner approval", run)
			}
		})
	}
}

func TestRerunBlockedWithoutApprovalStillNeedsAction(t *testing.T) {
	for _, gone := range []bool{true, false} {
		t.Run(map[bool]string{true: "gone run", false: "changed base"}[gone], func(t *testing.T) {
			w := newWorld4(t)
			w.onboarding()
			p, s := w.onboardingPull(7)
			w.send4(s)
			diagnostic := "(no_run)"
			if gone {
				delete(w.gh.runs, p.runID)
			} else {
				diagnostic = "(base_changed)"
				base := w.gh.pulls[pullKey(shopName, 7)].BaseSHA
				moved := w.sha()
				w.gh.heads[branchKey(shopName, testBranch)] = moved
				w.gh.compares[shopName+"|"+base+"|"+moved] = api.Comparison{Status: "ahead", Paths: []string{"pkg/a.go"}}
			}
			if got := w.gate.recheckPull(context.Background(), w.shop, p.number, p.head); got != CheckBlocked {
				t.Fatalf("recheck = %q, want blocked", got)
			}
			if run, ok := w.latestGate(p.head); !ok || run.Conclusion != "action_required" || !strings.Contains(run.Summary, diagnostic) {
				t.Errorf("check = %+v, want action_required with %s", run, diagnostic)
			}
		})
	}
}

func TestForeignProposalRetirementIgnoresOwnerApproval(t *testing.T) {
	w := newWorld4(t)
	w.onboarding()
	p, _ := w.onboardingPull(7)
	out, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst, Pull: 7})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := w.latestGate(p.head)
	w.openAt(8, p.head)
	w.wantOwnerCheck(p, out.Approval)
	if after, _ := w.latestGate(p.head); after.ID != before.ID {
		t.Error("foreign proposal retirement rewrote an approval check")
	}
}
