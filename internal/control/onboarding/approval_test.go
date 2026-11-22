package onboarding

import (
	"context"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

type approvalView struct {
	setupView
	summary gate.ApprovalSummary
}

func (v approvalView) SetupApproval(context.Context, api.Repo, gate.OwnerAction) gate.ApprovalSummary {
	return v.summary
}

func TestSetupSeparatesSignatureFromActivation(t *testing.T) {
	for _, tt := range []struct {
		name               string
		approved, workflow bool
		summary            gate.ApprovalSummary
		want               string
	}{
		{"approved policy missing workflow", true, false, gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: gate.OwnerWorkflow}, "needed"},
		{"exact head pending", true, false, gate.ApprovalSummary{Status: gate.ApprovalPending, Kind: gate.OwnerWorkflow, Pull: 12, CurrentHead: "h1", ApprovedHead: "h1"}, "approved_pending_merge"},
		{"changed head", true, false, gate.ApprovalSummary{Status: gate.ApprovalStale, Kind: gate.OwnerWorkflow, Pull: 12, CurrentHead: "h2", ApprovedHead: "h1"}, "stale"},
		{"merged unprocessed", true, true, gate.ApprovalSummary{Status: gate.ApprovalPending, Kind: gate.OwnerWorkflow, Pull: 12, CurrentHead: "h1", ApprovedHead: "h1", Merged: true}, "approved_pending_merge"},
		{"already converted needs approval", false, true, gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: gate.OwnerFirst}, "needed"},
		{"already converted active", true, true, gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: gate.OwnerFirst}, "active"},
		{"unreadable is not active", true, true, gate.ApprovalSummary{Status: gate.ApprovalUnknown, Kind: gate.OwnerFirst}, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy}
			if tt.workflow {
				h.files["tip"][workflowPath] = setupWorkflow(setupCommit)
			}
			state := gate.SetupState{}
			if tt.approved {
				state.ApprovedDigest = policy3.Digest([]byte(setupPolicy))
			}
			s := &setups{gh: h, gate: approvalView{setupView{state}, tt.summary}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			got, err := s.Setup(t.Context(), setupRepo)
			if err != nil || string(got.Approval.Status) != tt.want || got.Approval.Merged != tt.summary.Merged {
				t.Fatalf("Setup = %+v, %v", got, err)
			}
			if got.Approval.Status == gate.ApprovalActive && !got.Admitted {
				t.Fatal("active without default-branch files")
			}
		})
	}
}

func TestSetupPullRequiresNoRecordedApproval(t *testing.T) {
	for _, summary := range []gate.ApprovalSummary{
		{Status: gate.ApprovalPending, Kind: gate.OwnerFirst, Pull: 12, ApprovedHead: "head", CurrentHead: "head"},
		{Status: gate.ApprovalStale, Kind: gate.OwnerFirst, Pull: 12, ApprovedHead: "old", CurrentHead: "head"},
		{Status: gate.ApprovalUnknown, Kind: gate.OwnerFirst, Pull: 12},
	} {
		t.Run(string(summary.Status), func(t *testing.T) {
			h := newSetupHub()
			h.pulls = []api.Pull{{Number: 12, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadRef: "archfit/setup", HeadSHA: "head"}}
			h.files["head"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
			s := &setups{gh: h, gate: approvalView{summary: summary}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(t.Context(), setupRepo)
			if err != nil || out.SetupPull != 0 || out.FirstPull != 12 || out.Approval != summary {
				t.Fatalf("Setup = %+v, %v", out, err)
			}
		})
	}
}
