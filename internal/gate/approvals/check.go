package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// OnboardingCheck returns the owner's verdict only while the exact onboarding
// approval still applies. It does not turn an engine report into a verified pass.
// Publication calls it inside the head lease, including for reruns and withdrawals.
func (s *Service) OnboardingCheck(ctx context.Context, r api.Repo, pull int, head string) (*api.CheckRun, error) {
	if pull <= 0 || head == "" {
		return nil, nil
	}
	c, err := s.Bind(ctx, r, Action{Kind: records.KindFirst, Pull: pull})
	if errors.Is(err, ErrNotDue) || errors.Is(err, ErrNotApprovable) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.HeadSHA != head {
		return nil, nil
	}
	id, p, err := s.pullProposal(ctx, r, pull)
	if err != nil || p.Kind != records.ProposalFirst || p.PullRequest != pull || !matchesApproval(p, c) {
		return nil, err
	}
	for i := len(p.Approvals) - 1; i >= 0; i-- {
		approvalID := p.Approvals[i]
		var a records.Approval
		if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, approvalID), &a); err != nil {
			return nil, err
		}
		var bound Change
		if err := json.Unmarshal(a.Change, &bound); err != nil {
			return nil, err
		}
		if a.State == records.StateOpen && a.Kind == records.KindFirst && a.Proposal == id && a.Login != "" && bound == c {
			check := approvalCheck(approvalID, c, a.Login)
			return &check, nil
		}
	}
	return nil, nil
}

func approvalCheck(id string, c Change, login string) api.CheckRun {
	return api.CheckRun{
		Name: proposals.GateCheck, HeadSHA: c.HeadSHA, ExternalID: "approval:" + id,
		Status: "completed", Conclusion: "success", Title: "archfit: owner approved this head",
		Summary: fmt.Sprintf("Owner `%s` approved these exact policy and workflow bytes at head `%s` for pull request #%d.\n\n"+
			"Policy (`%s`): `%s`.\nWorkflow (`%s`): `%s`.\n\n"+
			"This is an owner approval, not an engine pass. A new head needs a new approval.\n",
			login, c.HeadSHA, c.Pull, PolicyPath, c.Policy, WorkflowPath, c.Workflow),
	}
}

func (s *Service) checkApproved(ctx context.Context, r api.Repo, d Done, login string) {
	check := approvalCheck(d.Approval, d.Change, login)
	check.StartedAt = s.now().UTC()
	if err := feedback.WriteOwnerCheck(ctx, s.gh, s.store, r, check, s.now); err != nil {
		logFailure(ctx, r, "publish the owner's approval check", err)
	}
}

// pullProposal is the same indexed lookup for review, action and publication.
func (s *Service) pullProposal(ctx context.Context, r api.Repo, pull int) (string, records.Proposal, error) {
	var p records.Proposal
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, tenant(r), records.ProposalIndexKey(r.FullName), &index); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return "", p, err
	}
	id, ok := index.Lookup(records.PullEntry(pull))
	if !ok {
		return "", p, nil
	}
	err := records.Load(ctx, s.store, tenant(r), records.ProposalKey(r.FullName, id), &p)
	return id, p, err
}

func (s *Service) notAlreadyApproved(ctx context.Context, r api.Repo, c Change) error {
	if c.Pull <= 0 {
		return nil
	}
	_, p, err := s.pullProposal(ctx, r, c.Pull)
	if err != nil {
		return err
	}
	if matchesApproval(p, c) {
		return ErrNotDue
	}
	return nil
}
