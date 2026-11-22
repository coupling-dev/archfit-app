package proposals

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
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
	// current voids a proposal whose pull request was closed, so a reopened one never merges on its old approvals.
	cur, err := s.current(ctx, r)
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
	// The tip's policy is read as bytes: a revert may restore a deleted or unreadable policy.
	tipFile, _, err := s.gh.File(ctx, r.RepoRef, cur.tip, policyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return err
	}
	file, kept, voided, err := apply(tipFile, cur)
	if err != nil {
		return err
	}
	listed := kept
	switch {
	case len(voided) > 0 || policy3.Digest(file) != pin.Digest:
		// The default branch's policy changed under the proposal. Only once that policy is the approved one (an owner
		// confirmed it) are its answers written again on it, as an answer does, never its old file copied over it.
		// While the change is still outside the flow, nothing is voided or rewritten: the owner decides first.
		if err := s.stateAllows(ctx, r, cur.tip, cur.approvals); err != nil {
			return nil
		}
		if err := s.voidAndNote(ctx, r, pin.PullRequest, voided, false); err != nil {
			return err
		}
		next := s.resync(ctx, r, cur, cur.tip, file, kept)
		if next == nil || len(voided) > 0 {
			// An answer left the pull request: whether the rest merges now is a new answer's choice.
			return nil
		}
		pin = *next
	default:
		pin = s.upToDate(ctx, r, cur.tip, pin, listed)
	}
	// Every D13 condition is read again on every pass; the check run is written only when this head has none yet.
	check := s.check
	if s.passedOn(ctx, r.RepoRef, pin) {
		check = s.verify
	}
	if err := check(ctx, r, cur.tip, pin, listed); err != nil {
		if errors.Is(err, ErrEditChanged) {
			s.voidMovedEdits(ctx, r, pin, listed)
			return nil
		}
		logFailure(ctx, r, "set archfit/gate on the policy pull request", err)
		return nil
	}
	return s.mergeWhenReady(ctx, r, cur.tip, pin, listed)
}

// Closed voids the open proposal of pull request pull, which the pull_request closed event says closed without a merge,
// so it never merges on its old approvals after a reopen. The event decides, not the pull request's state now: a
// reopen may already have happened before the event is handled.
func (s *Service) Closed(ctx context.Context, r api.Repo, pull int) error {
	release, err := s.hold(ctx, r)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	cur, err := s.current(ctx, r)
	if err != nil || cur == nil || cur.rec.PullRequest != pull {
		return err
	}
	return s.dropClosed(ctx, r, cur)
}

// passedOn reports whether the latest archfit/gate run on the pinned head is this proposal's success.
func (s *Service) passedOn(ctx context.Context, ref api.RepoRef, pin records.Pin) bool {
	runs, err := s.gh.CheckRuns(ctx, ref, pin.HeadSHA, gateCheck)
	if err != nil || len(runs) == 0 {
		return false
	}
	last := slices.MaxFunc(runs, func(a, b api.CheckRun) int { return cmp.Compare(a.ID, b.ID) })
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
