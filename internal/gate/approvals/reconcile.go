package approvals

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// JudgedAmbiguous leaves the range unknown without consuming its approvals.
// Independently proven stale edits can be voided before the history judgement.
const JudgedAmbiguous = "ambiguous"

// Reconcile repairs only a proven single policy change on a complete linear history from the approved commit
// to tip. An indexed proposal must uniquely match that change's merge commit and the tip's digest. Every other
// range is ambiguous: unlike Pushed, this entry point never voids a proposal for an ambiguous range. A pending recovered handoff is
// returned until the gate finishes AfterAnswer and clears Repo.SweepRecheck.
func (s *Service) Reconcile(ctx context.Context, r api.Repo, tip string) (Outcome, error) {
	if err := s.retireMovedEdits(ctx, r); err != nil {
		return Outcome{}, err
	}
	var rec records.Repo
	err := records.Load(ctx, s.store, tenant(r), records.RepoKey(r.FullName), &rec)
	if errors.Is(err, records.ErrNoRecord) {
		return Outcome{}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if rec.SweepRecheck != "" {
		return s.consumed(ctx, r, rec.SweepRecheck)
	}
	if rec.ApprovedDigest == "" {
		return Outcome{}, nil
	}
	raw, err := s.policy(ctx, r.RepoRef, tip)
	if err != nil {
		return Outcome{}, err
	}
	digest := digestOf(raw)
	if digest == rec.ApprovedDigest {
		return Outcome{}, nil
	}
	ambiguous := Outcome{Judged: JudgedAmbiguous}
	if rec.OutsideFlow != nil {
		if rec.OutsideFlow.Digest == digest {
			return s.outsideIf(ctx, Push{Repo: r, Before: rec.ApprovedCommit, After: tip}, digest, rec)
		}
		return ambiguous, nil
	}
	commit, err := s.singleChange(ctx, r, rec, tip, digest)
	if err != nil || commit == "" {
		return ambiguous, err
	}
	index := records.Index{Schema: records.SchemaProposalIndex}
	err = records.Load(ctx, s.store, tenant(r), records.ProposalIndexKey(r.FullName), &index)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return Outcome{}, err
	}
	var match *proposal
	for _, e := range index.Entries {
		p := &proposal{id: e.ID, commit: commit}
		if err := records.Load(ctx, s.store, tenant(r), records.ProposalKey(r.FullName, e.ID), &p.rec); err != nil {
			return Outcome{}, err
		}
		if p.rec.Kind == records.ProposalWorkflow {
			continue
		}
		pl, err := s.gh.Pull(ctx, r.RepoRef, p.rec.PullRequest)
		if err != nil {
			if errors.Is(err, api.ErrRateLimited) || ctx.Err() != nil {
				return Outcome{}, err
			}
			return ambiguous, nil
		}
		if !pl.Merged || pl.MergeCommitSHA != commit || p.rec.State != records.StateOpen ||
			p.rec.ExpectedDigest != digest || p.rec.Kind != "" || match != nil {
			return ambiguous, nil
		}
		match = p
	}
	// Re-read the tip before any write. An extension or a second merge arriving during proof invalidates it.
	current, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return Outcome{}, err
	}
	if current != tip {
		return ambiguous, nil
	}
	push := records.Push{Before: rec.ApprovedCommit, After: tip}
	if match == nil {
		return s.outsideIf(ctx, Push{Repo: r, Before: push.Before, After: tip}, digest, rec)
	}
	parent, err := s.editParent(ctx, r, match)
	if err != nil {
		if errors.Is(err, records.ErrMoved) || api.Permanent(err) {
			return ambiguous, nil
		}
		return Outcome{}, err
	}
	if err := s.retire(ctx, r, match, proposals.RetiredUsed); err != nil {
		return Outcome{}, err
	}
	err = records.Consume(ctx, s.store, tenant(r), r.FullName, records.Merge{
		Proposal: match.id, Commit: commit, Digest: digest, Push: push, AtTip: true, Over: rec.ApprovedCommit, Recheck: true, Parent: parent,
	})
	if errors.Is(err, records.ErrMoved) || errors.Is(err, records.ErrApprovedMoved) {
		return ambiguous, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	return s.consumed(ctx, r, match.id)
}

// singleChange follows parent identities rather than trusting list order. Missing, cut, non-linear or unreadable
// history is not proof. The only policy-changing commit must leave exactly the tip's policy digest.
func (s *Service) singleChange(ctx context.Context, r api.Repo, rec records.Repo, tip, digest string) (string, error) {
	if rec.ApprovedCommit == "" {
		return "", nil
	}
	base, err := s.policy(ctx, r.RepoRef, rec.ApprovedCommit)
	if err != nil || digestOf(base) != rec.ApprovedDigest {
		if errors.Is(err, api.ErrRateLimited) || ctx.Err() != nil {
			return "", err
		}
		return "", nil
	}
	c, err := s.gh.Compare(ctx, r.RepoRef, rec.ApprovedCommit, tip)
	if err != nil {
		if errors.Is(err, api.ErrRateLimited) || ctx.Err() != nil {
			return "", err
		}
		return "", nil
	}
	reverse := linearRange(c, rec.ApprovedCommit, tip)
	if len(reverse) == 0 {
		return "", nil
	}
	previous, changed := rec.ApprovedDigest, ""
	for i := len(reverse) - 1; i >= 0; i-- {
		raw, err := s.policy(ctx, r.RepoRef, reverse[i])
		if err != nil {
			if errors.Is(err, api.ErrRateLimited) || ctx.Err() != nil {
				return "", err
			}
			return "", nil
		}
		next := digestOf(raw)
		if next != previous {
			if changed != "" {
				return "", nil
			}
			changed = reverse[i]
		}
		previous = next
	}
	if previous != digest {
		return "", nil
	}
	return changed, nil
}
