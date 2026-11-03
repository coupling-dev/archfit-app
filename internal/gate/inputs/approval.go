package inputs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// The refusals of App approve/eligibility checks. Any other error is a failed read, authority lookup or write.
var (
	ErrNothingToApprove = errors.New("inputs: approve: the pull request changes no protected path")
	ErrForkPull         = errors.New("inputs: approve: the pull request comes from a fork")
	ErrAgentIdentity    = errors.New("inputs: approve: an agent identity never approves")
	ErrNotOwner         = errors.New("inputs: approve: not an architecture owner of the protected settings")
	ErrDigestsChanged   = errors.New("inputs: approve: the protected files at the head are not the ones shown")
)

// Candidate is what an owner approves in the App: the protected paths a pull request changes and what each holds at
// its head.
type Candidate struct {
	Paths []string // in ProtectedPaths order; empty when the pull request needs no owner approval
	// Digests is policy.Digest of each path at the head, "" for a path the head deletes, so a deletion is part of what
	// is approved. Nil for a fork: no head file of a fork is read.
	Digests map[string]string
}

// approvalRecord is the released App's legacy grant format under approvalKey. New App decisions use the separate
// per-PR decision ledger so objections, overrides and withdrawals can be audited without dropping this real data.
type approvalRecord struct {
	Login   string            `json:"login"`
	HeadSHA string            `json:"head_sha"`
	Digests map[string]string `json:"digests"` // the Candidate digests the owner was shown
	At      time.Time         `json:"at"`
}

// approvalKey is the legacy Store key of the grant of t's head in its pull request. It remains read-only for
// compatibility; new actions use decisionKey. Repository names compare case-insensitively.
func approvalKey(t Target) string {
	return "approval/" + strings.ToLower(t.Repo.FullName) + "/" + strconv.Itoa(t.PullRequest) + "/" + t.HeadSHA
}

func (r *resolver) Candidate(ctx context.Context, t Target) (Candidate, error) {
	var err error
	t, err = r.pinProtectedRef(ctx, t)
	if err != nil {
		return Candidate{}, err
	}
	s, found, err := r.baseSettings(ctx, t)
	if err != nil || !found {
		return Candidate{}, err
	}
	paths, err := r.changedProtected(ctx, t, s)
	if err != nil || len(paths) == 0 || t.Fork {
		return Candidate{Paths: paths}, err
	}
	digests, err := r.headDigests(ctx, t, paths)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{Paths: paths, Digests: digests}, nil
}

func (r *resolver) CanApprove(ctx context.Context, t Target, login string) error {
	_, _, err := r.approvable(ctx, t, login)
	return err
}

func (r *resolver) CanWithdraw(ctx context.Context, t Target, login string) (bool, error) {
	var err error
	t, err = r.pinProtectedRef(ctx, t)
	if err != nil {
		return false, err
	}
	s, paths, err := r.approvable(ctx, t, login)
	if err != nil {
		return false, err
	}
	current, err := r.headDigests(ctx, t, paths)
	if err != nil {
		return false, err
	}
	rec, _, err := r.readDecisionRecord(ctx, t)
	if err != nil {
		return false, err
	}
	reviews, err := r.pulls.Reviews(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return false, fmt.Errorf("inputs: reviews: %w", err)
	}
	return r.ownerHasGrant(ctx, t, s, login, current, reviews, rec)
}

// CanDecide is the owner rule of CanApprove for an approval that is not of a pull request head: ErrAgentIdentity,
// ErrNotOwner (also without a settings file on the protected ref) or nil.
func (r *resolver) CanDecide(ctx context.Context, t Target, login string) error {
	s, _, err := r.baseSettings(ctx, t)
	if err != nil {
		return err
	}
	return r.owner(ctx, t, s, login) // no settings file: no owners, so ErrNotOwner
}

// Approve is the legacy façade action and now records the same audited, objection-aware transition as Decide.
func (r *resolver) Approve(ctx context.Context, t Target, login string, seen map[string]string, now time.Time) error {
	return r.Decide(ctx, t, login, seen, DecisionApprove, "", nil, now)
}

// approvable returns the protected settings and the protected paths t changes when login may approve its head, else
// the refusal. The checks run in this order, so a caller can word the first one that fails.
func (r *resolver) approvable(ctx context.Context, t Target, login string) (settings.Settings, []string, error) {
	var err error
	t, err = r.pinProtectedRef(ctx, t)
	if err != nil {
		return settings.Settings{}, nil, err
	}
	s, found, err := r.baseSettings(ctx, t)
	if err != nil {
		return s, nil, err
	}
	var paths []string
	if found {
		if paths, err = r.changedProtected(ctx, t, s); err != nil {
			return s, nil, err
		}
	}
	switch {
	case len(paths) == 0:
		return s, nil, ErrNothingToApprove
	case t.Fork:
		return s, nil, ErrForkPull
	}
	if err := r.owner(ctx, t, s, login); err != nil {
		return s, nil, err
	}
	return s, paths, nil
}

// owner is the rule of who may approve in the App, whatever it is they approve: login is no agent identity and is an
// owner of s, directly or through a team.
func (r *resolver) owner(ctx context.Context, t Target, s settings.Settings, login string) error {
	switch {
	case s.IsAgent(login):
		return ErrAgentIdentity
	case login == "":
		return ErrNotOwner
	}
	ok, err := isOwner(s.Owners, login, r.member(ctx, t))
	if err != nil {
		return fmt.Errorf("inputs: team membership: %w", err)
	}
	if !ok {
		return ErrNotOwner
	}
	return nil
}

// pinProtectedRef selects one immutable protected-settings snapshot for a PR decision.
func (r *resolver) pinProtectedRef(ctx context.Context, t Target) (Target, error) {
	if t.PullRequest == 0 || t.BaseRef == "" || t.BaseSHA != "" {
		return t, nil
	}
	sha, err := r.repos.BranchHead(ctx, t.Repo.RepoRef, t.BaseRef)
	if err != nil {
		return Target{}, fmt.Errorf("inputs: read protected branch head: %w", err)
	}
	if sha == "" {
		return Target{}, errors.New("inputs: protected branch has no head commit")
	}
	t.BaseSHA = sha
	return t, nil
}

// protectedRef prefers the pinned protected commit over the mutable branch name.
func protectedRef(t Target) string {
	if t.PullRequest != 0 && t.BaseSHA != "" {
		return t.BaseSHA
	}
	return t.BaseRef
}

// baseSettings reads the settings of the protected ref. found is false when there is no file or it does not parse:
// a broken settings file is a missing one.
func (r *resolver) baseSettings(ctx context.Context, t Target) (s settings.Settings, found bool, err error) {
	raw, err := r.file(ctx, t, protectedRef(t), settings.Path)
	if err != nil || raw == nil {
		return settings.Settings{}, false, err
	}
	s, err = settings.Parse(raw)
	return s, err == nil, nil
}

// changedProtected returns the protected paths of s that the pull request t changes; none for a push.
func (r *resolver) changedProtected(ctx context.Context, t Target, s settings.Settings) ([]string, error) {
	if t.PullRequest == 0 {
		return nil, nil
	}
	changed, truncated, err := r.pulls.ChangedFiles(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return nil, fmt.Errorf("inputs: changed files: %w", err)
	}
	return protectedPaths(changed, truncated, s), nil
}

// headDigests hashes each path at t's head; the bytes are never parsed. An absent path is "".
func (r *resolver) headDigests(ctx context.Context, t Target, paths []string) (map[string]string, error) {
	digests := make(map[string]string, len(paths))
	for _, p := range paths {
		raw, err := r.file(ctx, t, t.HeadSHA, p)
		if err != nil {
			return nil, err
		}
		if raw != nil {
			digests[p] = policy.Digest(raw)
		} else {
			digests[p] = ""
		}
	}
	return digests, nil
}

func (r *resolver) member(ctx context.Context, t Target) func(team, login string) (bool, error) {
	return func(team, login string) (bool, error) {
		return r.pulls.TeamMember(ctx, t.Repo.RepoRef, team, login)
	}
}
