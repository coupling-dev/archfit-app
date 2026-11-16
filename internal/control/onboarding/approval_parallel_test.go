package onboarding

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type waitingApproval struct {
	setupView
	rulesStarted <-chan struct{}
	calls        atomic.Int64
}

func (v *waitingApproval) SetupApproval(ctx context.Context, _ api.Repo, a gate.OwnerAction) gate.ApprovalSummary {
	v.calls.Add(1)
	select {
	case <-v.rulesStarted:
		return gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: a.Kind, Pull: a.Pull}
	case <-ctx.Done():
		return gate.ApprovalSummary{Status: gate.ApprovalUnknown, Kind: a.Kind, Pull: a.Pull}
	}
}

type signalingRules struct {
	*setupHub
	started chan struct{}
}

func (h signalingRules) BranchPolicy(ctx context.Context, r api.RepoRef, branch string) ([]api.RepositoryRule, error) {
	close(h.started)
	return h.setupHub.BranchPolicy(ctx, r, branch)
}

func TestSetupApprovalAndRulesOverlapWithoutRepeatingSummary(t *testing.T) {
	for _, rulesErr := range []error{nil, api.ErrRateLimited} {
		t.Run(map[bool]string{true: "unreadable rules", false: "readable rules"}[rulesErr != nil], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			ctx, stats := api.WithStageStats(ctx)
			base := newSetupHub()
			base.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
			base.ruleErr = rulesErr
			rulesStarted := make(chan struct{})
			view := &waitingApproval{rulesStarted: rulesStarted}
			s := &setups{gh: signalingRules{base, rulesStarted}, gate: view, actionApproved: func(ref string) bool { return ref == setupCommit }}
			got, err := s.Setup(ctx, setupRepo)
			if err != nil || ctx.Err() != nil || got.Approval.Status != gate.ApprovalNeeded {
				t.Fatalf("Setup = %+v, %v; context = %v", got, err, ctx.Err())
			}
			if view.calls.Load() != 1 || stats.Snapshot()["approval_summary"].Count != 1 {
				t.Fatal("approval summary was not measured exactly once")
			}
			if rulesErr != nil && (len(got.Rules.Findings) != 1 || got.Rules.Findings[0].State != "unknown") {
				t.Fatalf("rules failure became clean: %+v", got.Rules)
			}
		})
	}
}
