package proposals

import (
	"context"
	"errors"
	"slices"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// mergeable are GitHub's mergeable_state values under which a merge is expected to succeed.
var mergeable = []string{"clean", "unstable", "has_hooks"}

// Advance moves the open policy pull request on without an owner: it brings the branch up to date with the default
// branch (one App commit of the same policy bytes on the tip), sets archfit/gate on the new head, and merges when
// GitHub reports the pull request mergeable. The default-branch push and the sweep call it, so an approved change
// merges even when the default branch moved or the checks finished after the answer. It leaves alone a pull request
// whose last answer chose to merge later, one with an approval that no longer holds (the owner approves again), and a
// branch someone else changed (the owner restores it). Every refusal waits for the next call.
func (s *Service) Advance(ctx context.Context, r api.Repo) error {
	release, err := s.hold(ctx, r)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout+afterTimeout)
	defer cancel()
	cur, err := s.readCurrent(ctx, r, false)
	if err != nil || cur == nil {
		return err
	}
	if !cur.rec.MergeNow || cur.rec.EditVoided || len(cur.stale) > 0 || len(cur.approvals) == 0 {
		return nil
	}
	head, err := s.gh.BranchHead(ctx, r.RepoRef, Branch)
	if err != nil {
		return err
	}
	if head != cur.rec.HeadSHA {
		return nil
	}
	pin := records.Pin{Proposal: cur.id, PullRequest: cur.rec.PullRequest, HeadSHA: head, Digest: cur.rec.ExpectedDigest}
	listed := cur.approvals
	if next := s.upToDate(ctx, r, cur.tip, pin, listed); next.HeadSHA != pin.HeadSHA {
		pin = next
	} else if s.passedOn(ctx, r.RepoRef, pin) {
		return s.mergeWhenReady(ctx, r, cur.tip, pin, listed)
	}
	if err := s.check(ctx, r, cur.tip, pin, listed); err != nil {
		if errors.Is(err, ErrEditChanged) {
			s.voidMovedEdits(ctx, r, pin, listed)
			return nil
		}
		logFailure(ctx, r, "set archfit/gate on the policy pull request", err)
		return nil
	}
	return s.mergeWhenReady(ctx, r, cur.tip, pin, listed)
}

// passedOn reports whether the latest archfit/gate run on the pinned head is this proposal's success.
func (s *Service) passedOn(ctx context.Context, ref api.RepoRef, pin records.Pin) bool {
	runs, err := s.gh.CheckRuns(ctx, ref, pin.HeadSHA, gateCheck)
	if err != nil || len(runs) == 0 {
		return false
	}
	last := runs[len(runs)-1]
	return last.ExternalID == ExternalID(pin.Proposal) && last.Status == statusCompleted && last.Conclusion == conclusionSuccess
}

// mergeWhenReady merges when GitHub reports the pull request mergeable; any other state waits for the next call.
func (s *Service) mergeWhenReady(ctx context.Context, r api.Repo, tip string, pin records.Pin, listed []approved) error {
	pull, err := s.gh.Pull(ctx, r.RepoRef, pin.PullRequest)
	if err != nil {
		return err
	}
	if pull.HeadSHA != pin.HeadSHA || !slices.Contains(mergeable, pull.MergeableState) {
		return nil
	}
	if state, _, reason := s.merge(ctx, r, tip, pin, listed); state == requests.PullRefused {
		logFailure(ctx, r, "merge the policy pull request when ready", errors.New(reason))
	}
	return nil
}
