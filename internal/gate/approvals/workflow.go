package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// WorkflowPushed judges a push to the default branch for the workflow approval it uses (M2 task 23): the merge of a
// workflow proposal's pull request with the workflow file the approval admitted consumes it, once; a merge with
// another file voids it, so the approval admits nothing else. A push that leaves the workflow file alone is none, and
// so is a delivery of the same push again: the proposal is no longer open. An error is a failed read or write; the
// caller retries the delivery.
//
// A workflow file changed with no approval is not a state of its own: each pull request's run is compared with the
// default branch's file (upload4), so the change shows on every pull request that differs from it.
func (s *Service) WorkflowPushed(ctx context.Context, p Push) (Outcome, error) {
	if len(p.ChangedPaths) > 0 && !slices.Contains(p.ChangedPaths, WorkflowPath) {
		return Outcome{Judged: JudgedNone}, nil
	}
	merged, err := s.mergedIn(ctx, p, true)
	if err != nil || merged == nil {
		return Outcome{Judged: JudgedNone}, err
	}
	admits, err := s.landedWorkflow(ctx, p.Repo.RepoRef, merged)
	if err != nil {
		return Outcome{}, err
	}
	if !admits {
		if err := s.void(ctx, p.Repo, merged); err != nil {
			return Outcome{}, err
		}
		return Outcome{Judged: JudgedNone, Voided: merged.id}, nil
	}
	if err := s.retire(ctx, p.Repo, merged, proposals.RetiredUsed); err != nil {
		return Outcome{}, err
	}
	if err := records.ConsumeWorkflow(ctx, s.store, tenant(p.Repo), p.Repo.FullName, merged.id, merged.commit); err != nil {
		return Outcome{}, fmt.Errorf("approvals: consume workflow proposal %s: %w", merged.id, err)
	}
	return Outcome{Judged: JudgedConsumed, Proposal: merged.id}, nil
}

// landedWorkflow reports whether p's merge commit holds the workflow file its approval admitted. A proposal that
// admits no workflow file lands none.
func (s *Service) landedWorkflow(ctx context.Context, ref api.RepoRef, p *proposal) (bool, error) {
	if p.rec.WorkflowDigest == "" {
		return true, nil
	}
	raw, _, err := s.gh.File(ctx, ref, p.commit, WorkflowPath)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("approvals: read the workflow at %s: %w", p.commit, err)
	}
	return records.FileDigest(raw) == p.rec.WorkflowDigest, nil
}

// closeReverted notes and closes the issue of the policy changed outside the flow that a revert in the consumed
// proposal p ended. A consume that did not end the state (the tip holds a later change) leaves the issue open. It is a
// notification: a failure is logged, and a delivery of the push again does not repeat it.
func (s *Service) closeReverted(ctx context.Context, r api.Repo, p *proposal) {
	t := tenant(r)
	var issues []int
	for _, id := range p.rec.Approvals {
		var a records.Approval
		if err := records.Load(ctx, s.store, t, records.ApprovalKey(r.FullName, id), &a); err != nil {
			logFailure(ctx, r, "read a revert", err)
			return
		}
		var rev records.Revert
		if a.Kind != records.KindRevert || json.Unmarshal(a.Change, &rev) != nil || rev.Issue == 0 {
			continue
		}
		if !slices.Contains(issues, rev.Issue) {
			issues = append(issues, rev.Issue)
		}
	}
	if len(issues) == 0 {
		return
	}
	var repo records.Repo
	if err := records.Load(ctx, s.store, t, records.RepoKey(r.FullName), &repo); err != nil || repo.OutsideFlow != nil {
		if err != nil {
			logFailure(ctx, r, "read the repository", err)
		}
		return
	}
	for _, issue := range issues {
		s.note(ctx, r, issue, revertedNote(p.rec.PullRequest, p.commit))
		if err := s.gh.CloseIssue(ctx, r.RepoRef, issue); err != nil {
			logFailure(ctx, r, "close the issue of a reverted policy", err)
		}
	}
}

// stillApplies reports whether the merged proposal p still applies to the repository record r (M2 task 23): a first
// approval only while change control has not started (another onboarding pull request's first approval started it), a
// revert only while the state it was made in holds (records.Revert.Holds: a confirm, or a later change, ended it). Such a proposal is voided, and the push is judged as any other.
func (s *Service) stillApplies(ctx context.Context, r api.Repo, repo records.Repo, p *proposal) (bool, error) {
	if p.rec.Kind == records.ProposalFirst {
		return repo.ApprovedDigest == "" && repo.OutsideFlow == nil, nil
	}
	for _, id := range p.rec.Approvals {
		var a records.Approval
		if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, id), &a); err != nil {
			return false, fmt.Errorf("approvals: read approval %s: %w", id, err)
		}
		if a.Kind != records.KindRevert {
			continue
		}
		var rev records.Revert
		if err := json.Unmarshal(a.Change, &rev); err != nil {
			return false, fmt.Errorf("approvals: approval %s holds an unreadable revert: %w", id, err)
		}
		if !rev.Holds(repo) {
			return false, nil
		}
	}
	return true, nil
}

// retire takes back the verdict on p's head once p is used or void (M2 review H2): the App squash-merges its policy pull
// request, so that commit is not on the default branch, and another pull request at it would otherwise merge on its
// old success. It comes before the state change, and Retire repeats safely, so a failure is returned and the delivery
// retried with nothing changed. A pull request opened at that commit is retired when it opens (gate, owner.go).
func (s *Service) retire(ctx context.Context, r api.Repo, p *proposal, why string) error {
	return proposals.Retire(ctx, s.gh, r, p.rec.HeadSHA, p.rec.PullRequest, p.id, why, s.now())
}

// retireStaleReverts takes back the verdict of an open policy proposal whose revert no longer holds (a confirm, or a
// later change, ended the state it was made in): its head keeps a success that would restore the old policy over the
// new one. The proposal stays open; the next answer voids its revert (gate/proposals). A failure is returned.
func (s *Service) retireStaleReverts(ctx context.Context, r api.Repo) error {
	t := tenant(r)
	var repo records.Repo
	if err := records.Load(ctx, s.store, t, records.RepoKey(r.FullName), &repo); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return fmt.Errorf("approvals: read the repository: %w", err)
	}
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, t, records.ProposalIndexKey(r.FullName), &index); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return fmt.Errorf("approvals: read the proposal index: %w", err)
	}
	for _, e := range index.Entries {
		p := &proposal{id: e.ID}
		if err := records.Load(ctx, s.store, t, records.ProposalKey(r.FullName, e.ID), &p.rec); err != nil {
			return fmt.Errorf("approvals: read proposal %s: %w", e.ID, err)
		}
		if p.rec.State != records.StateOpen || p.rec.Kind != "" {
			continue
		}
		for _, id := range p.rec.Approvals {
			var a records.Approval
			if err := records.Load(ctx, s.store, t, records.ApprovalKey(r.FullName, id), &a); err != nil {
				return fmt.Errorf("approvals: read approval %s: %w", id, err)
			}
			var rev records.Revert
			if a.Kind != records.KindRevert || a.State != records.StateOpen || json.Unmarshal(a.Change, &rev) != nil || rev.Holds(repo) {
				continue
			}
			if err := s.retire(ctx, r, p, proposals.RetiredVoid); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
