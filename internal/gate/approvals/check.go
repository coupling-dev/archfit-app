package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
	"github.com/coupling-dev/archfit-app/internal/gate/proposals"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// OwnerCheck returns the owner's verdict on a pull request's head (Change.OwnerVerdict) only while the exact approval
// still applies: a first approval of an onboarding pull request, or a workflow approval of a pull request that also
// changes the policy. A workflow approval of any other head leaves the verdict to the engine, decided from the stored
// approval before any GitHub read. It does not turn an engine report into a verified pass. Publication calls it inside
// the head lease, including for reruns and withdrawals.
func (s *Service) OwnerCheck(ctx context.Context, r api.Repo, pull int, head string) (*api.CheckRun, error) {
	if pull <= 0 || head == "" {
		return nil, nil
	}
	id, p, err := s.pullProposal(ctx, r, pull)
	if err != nil || p.State != records.StateOpen || p.PullRequest != pull {
		return nil, err
	}
	var kind string
	switch p.Kind {
	case records.ProposalFirst:
		kind = records.KindFirst
	case records.ProposalWorkflow:
		kind = records.KindWorkflow
	default:
		return nil, nil
	}
	approvalID, a, bound, err := s.newestApproval(ctx, r, id, p, kind)
	if err != nil || approvalID == "" {
		return nil, err
	}
	if !bound.OwnerVerdict() || bound.HeadSHA != head {
		return nil, nil
	}
	c, err := s.Bind(ctx, r, Action{Kind: kind, Pull: pull})
	if errors.Is(err, ErrNotDue) || errors.Is(err, ErrNotApprovable) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c != bound || !matchesApproval(p, c) {
		return nil, nil
	}
	check := approvalCheck(approvalID, c, a.Login)
	return &check, nil
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

// newestApproval is the newest open approval of kind in proposal p of id, the one bound to its head, with the change
// it bound; "" when there is none.
func (s *Service) newestApproval(ctx context.Context, r api.Repo, id string, p records.Proposal, kind string) (string, records.Approval, Change, error) {
	for i := len(p.Approvals) - 1; i >= 0; i-- {
		var a records.Approval
		if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, p.Approvals[i]), &a); err != nil {
			return "", records.Approval{}, Change{}, err
		}
		if a.State != records.StateOpen || a.Kind != kind || a.Proposal != id || a.Login == "" {
			continue
		}
		var bound Change
		if err := json.Unmarshal(a.Change, &bound); err != nil {
			return "", records.Approval{}, Change{}, err
		}
		return p.Approvals[i], a, bound, nil
	}
	return "", records.Approval{}, Change{}, nil
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
	id, p, err := s.pullProposal(ctx, r, c.Pull)
	if err != nil || !matchesApproval(p, c) {
		return err
	}
	// The proposal holds no Base: an approval bound to another one (the policy at the merge base changed under the
	// same head) no longer applies, and the owner may approve again.
	_, _, bound, err := s.newestApproval(ctx, r, id, p, c.Kind)
	if err != nil {
		return err
	}
	if bound == c {
		return ErrNotDue
	}
	return nil
}
