package proposals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// The check run states of a pass (design 11.6).
const (
	statusCompleted   = "completed"
	conclusionSuccess = "success"
)

// check sets archfit/gate to success on the pinned head when every condition of M2 D13 holds, read again from GitHub:
//
//   - the branch archfit/policy is at the head. The head is a commit the App made on a parent it chose: a fast-forward
//     on the head it recorded, or one commit on a commit of the default branch (moveHead), so every commit of the
//     branch since the default branch is the App's;
//   - the pull request's head is that branch of this repository, its base the default branch, and it is open;
//   - from the tip, the head changes .archfit.yaml and nothing else;
//   - the policy at the head has the pinned digest, which is the digest of the recorded approvals written, in order,
//     on the tip's policy;
//   - the repository is under change control and the tip holds its approved policy, or, for a proposal with a revert,
//     the state the revert was made in still holds and the tip holds the change it replaces (M2 task 23, review H1):
//     an answer never starts change control.
//
// The caller holds the repository proposal lease through publication; edit
// invalidation uses the same lease and cannot overwrite or be overwritten by it.
// Any condition false sets nothing: a ruleset that requires the check then refuses the merge.
func (s *Service) check(ctx context.Context, r api.Repo, tip string, pin records.Pin, listed []approved) error {
	ref := r.RepoRef
	if err := s.editTarget(ctx, r, tip, listed); err != nil {
		return err
	}
	if err := s.stateAllows(ctx, r, tip, listed); err != nil {
		return err
	}
	head, err := s.gh.BranchHead(ctx, ref, Branch)
	if err != nil {
		return fmt.Errorf("proposals: read %s: %w", Branch, err)
	}
	if head != pin.HeadSHA {
		return fmt.Errorf("%w: %s is not at the recorded head", errRefused, Branch)
	}
	pull, err := s.gh.Pull(ctx, ref, pin.PullRequest)
	if err != nil {
		return fmt.Errorf("proposals: read pull request %d: %w", pin.PullRequest, err)
	}
	if pull.HeadRef != Branch || !strings.EqualFold(pull.HeadRepo, r.FullName) || pull.BaseRef != r.DefaultBranch ||
		pull.State != "open" {
		return fmt.Errorf("%w: pull request %d is not the open pull request of %s into %s", errRefused, pin.PullRequest,
			Branch, r.DefaultBranch)
	}
	cmp, err := s.gh.Compare(ctx, ref, tip, head)
	if err != nil {
		return fmt.Errorf("proposals: compare %s with the default branch: %w", Branch, err)
	}
	if cmp.Truncated || !slices.Equal(cmp.Paths, []string{policyPath}) {
		return fmt.Errorf("%w: %s changes more than %s", errRefused, Branch, policyPath)
	}
	file, _, err := s.gh.File(ctx, ref, head, policyPath)
	if err != nil {
		return fmt.Errorf("proposals: read the policy of %s: %w", Branch, err)
	}
	if policy3.Digest(file) != pin.Digest {
		return fmt.Errorf("%w: the policy of %s is not the one the approvals make", errRefused, Branch)
	}
	_, err = s.gh.CreateCheckRun(ctx, ref, api.CheckRun{
		Name:       gateCheck,
		HeadSHA:    head,
		ExternalID: ExternalID(pin.Proposal),
		Status:     statusCompleted,
		Conclusion: conclusionSuccess,
		Title:      checkTitle,
		Summary:    checkSummary(r.DefaultBranch, pin, listed),
		StartedAt:  s.now().UTC(),
	})
	return err
}

// stateAllows is the last condition of check: the repository's state lets the proposal pass. Without a revert in
// listed, the repository has an approved policy, is not outside the flow, and the tip holds that policy. With one,
// every revert still holds (records.Revert.Holds) and the tip holds the change it replaces.
func (s *Service) stateAllows(ctx context.Context, r api.Repo, tip string, listed []approved) error {
	repo, err := s.repo(ctx, r)
	if err != nil {
		return err
	}
	tipFile, _, err := s.gh.File(ctx, r.RepoRef, tip, policyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return fmt.Errorf("proposals: read the policy: %w", err)
	}
	want, reverts := repo.ApprovedDigest, false
	for _, a := range listed {
		if a.revert == nil {
			continue
		}
		if !a.revert.Holds(repo) {
			return fmt.Errorf("%w: a revert of a state that ended", errRefused)
		}
		want, reverts = a.revert.Outside, true
	}
	switch {
	case !reverts && (repo.ApprovedDigest == "" || repo.OutsideFlow != nil):
		return fmt.Errorf("%w: the repository is not under change control", errRefused)
	case digestOf(tipFile) != want:
		return fmt.Errorf("%w: %s does not hold the policy the proposal was written against", errRefused, r.DefaultBranch)
	}
	return nil
}
