package approvals

import (
	"context"
	"errors"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// Status describes the recorded signature, independently of repository activation.
type Status string

const (
	Needed  Status = "needed"
	Pending Status = "approved_pending_merge"
	Active  Status = "active"
	Stale   Status = "stale"
	Unknown Status = "unknown"
)

// Summary is read-only navigation evidence, never authority to approve or pass a check.
type Summary struct {
	Status       Status
	Kind         string
	Pull         int
	CurrentHead  string
	ApprovedHead string
	Merged       bool // approval recorded, but push processing has not confirmed activation
}

// matchesApproval is also used by Review to suppress a second signature of the same change.
func matchesApproval(p records.Proposal, c Change) bool {
	return p.State == records.StateOpen && p.HeadSHA == c.HeadSHA && p.ExpectedDigest == c.Policy &&
		p.WorkflowDigest == c.Workflow && (p.Kind == records.ProposalFirst || p.Kind == records.ProposalWorkflow)
}

// Summary reads the candidate's approval. With no candidate, it also finds outstanding approvals:
// an already merged PR is absent from OpenPulls while its push can still await processing.
func (s *Service) Summary(ctx context.Context, r api.Repo, a Action) (Summary, error) {
	out := Summary{Status: Needed, Kind: a.Kind, Pull: a.Pull}
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, tenant(r), records.ProposalIndexKey(r.FullName), &index); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return out, err
	}
	for _, entry := range index.Entries {
		if a.Kind == records.KindConfirm {
			break
		}
		if a.Pull > 0 && entry.Key != records.PullEntry(a.Pull) {
			continue
		}
		var p records.Proposal
		if err := records.Load(ctx, s.store, tenant(r), records.ProposalKey(r.FullName, entry.ID), &p); err != nil {
			return out, err
		}
		if p.State != records.StateOpen || (p.Kind != records.ProposalFirst && p.Kind != records.ProposalWorkflow) {
			continue
		}
		return s.proposalSummary(ctx, r, p)
	}
	c, err := s.Bind(ctx, r, a)
	if err == nil {
		out.CurrentHead = c.HeadSHA
		if a.Pull == 0 {
			out.CurrentHead = c.Commit
		}
	} else if !errors.Is(err, ErrNotDue) && !errors.Is(err, ErrNotApprovable) {
		return out, err
	}
	return out, nil
}

func (s *Service) proposalSummary(ctx context.Context, r api.Repo, p records.Proposal) (Summary, error) {
	out := Summary{Status: Unknown, Kind: p.Kind, Pull: p.PullRequest, ApprovedHead: p.HeadSHA}
	pull, err := s.gh.Pull(ctx, r.RepoRef, p.PullRequest)
	if err != nil {
		return out, err
	}
	out.CurrentHead = pull.HeadSHA
	if pull.HeadSHA == "" || pull.BaseRef != r.DefaultBranch || !strings.EqualFold(pull.HeadRepo, r.FullName) {
		return out, nil
	}
	if pull.State != "open" && !pull.Merged {
		return out, nil
	}
	if pull.HeadSHA != p.HeadSHA {
		out.Status = Stale
		return out, nil
	}
	pol, err := s.fileAt(ctx, r.RepoRef, pull.HeadSHA, PolicyPath)
	if err != nil {
		return out, err
	}
	run, err := s.fileAt(ctx, r.RepoRef, pull.HeadSHA, WorkflowPath)
	if err != nil {
		return out, err
	}
	if pol == nil || run == nil {
		return out, nil
	}
	c := Change{HeadSHA: pull.HeadSHA, Policy: policy3.Digest(pol), Workflow: records.FileDigest(run)}
	if !matchesApproval(p, c) {
		out.Status = Stale
		return out, nil
	}
	// An open PR must still satisfy Bind's repository-state and default-branch checks.
	// Merged proposals cannot Bind; leave activation to the existing push processor.
	if !pull.Merged {
		bound, err := s.Bind(ctx, r, Action{Kind: p.Kind, Pull: p.PullRequest})
		if err != nil {
			return out, err
		}
		if !matchesApproval(p, bound) {
			return out, nil
		}
	}
	out.Status, out.Merged = Pending, pull.Merged
	return out, nil
}
