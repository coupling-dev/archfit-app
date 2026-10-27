package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// The refusals of Approve and CanApprove. Any other error is a failed read or write.
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

// approvalRecord is an owner's approval recorded in the App, under approvalKey. The key holds the head SHA, so a new
// commit never finds the record of an older one.
type approvalRecord struct {
	Login   string            `json:"login"`
	HeadSHA string            `json:"head_sha"`
	Digests map[string]string `json:"digests"` // the Candidate digests the owner was shown
	At      time.Time         `json:"at"`
}

// approvalKey is the Store key of the approval of t's head. Repository names compare case-insensitively.
func approvalKey(t Target) string {
	return "approval/" + strings.ToLower(t.Repo.FullName) + "/" + t.HeadSHA
}

func (r *resolver) Candidate(ctx context.Context, t Target) (Candidate, error) {
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

// Approve records login's approval of t's head when seen is what the head holds now. A record that still counts is
// never replaced: one for this head already, by any owner, is success. One that no longer counts approves nothing, so
// it is replaced; it must not shadow the approval of an owner who may approve now.
func (r *resolver) Approve(ctx context.Context, t Target, login string, seen map[string]string, now time.Time) error {
	s, paths, err := r.approvable(ctx, t, login)
	if err != nil {
		return err
	}
	digests, err := r.headDigests(ctx, t, paths)
	if err != nil {
		return err
	}
	if !maps.Equal(seen, digests) {
		return ErrDigestsChanged
	}
	raw, err := json.Marshal(approvalRecord{Login: login, HeadSHA: t.HeadSHA, Digests: digests, At: now.UTC()})
	if err != nil {
		return fmt.Errorf("inputs: approve: %w", err)
	}
	tenant, key := store.Tenant(t.Repo.InstallationID), approvalKey(t)
	swapped, err := r.store.CompareAndSwap(ctx, tenant, key, nil, raw)
	if err != nil || swapped {
		return wrap("approve", err)
	}
	if by, err := r.recorded(ctx, t, s, paths); err != nil || by != "" {
		return err
	}
	old, err := r.store.Get(ctx, tenant, key)
	if err != nil {
		return fmt.Errorf("inputs: approve: %w", err)
	}
	// A lost swap means another approval landed between the two reads; that one stays.
	_, err = r.store.CompareAndSwap(ctx, tenant, key, old, raw)
	return wrap("approve", err)
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("inputs: %s: %w", op, err)
}

// approvable returns the protected settings and the protected paths t changes when login may approve its head, else
// the refusal. The checks run in this order, so a caller can word the first one that fails.
func (r *resolver) approvable(ctx context.Context, t Target, login string) (settings.Settings, []string, error) {
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
	case s.IsAgent(login):
		return s, nil, ErrAgentIdentity
	case login == "":
		return s, nil, ErrNotOwner
	}
	ok, err := isOwner(s.Owners, login, r.member(ctx, t))
	if err != nil {
		return s, nil, fmt.Errorf("inputs: team membership: %w", err)
	}
	if !ok {
		return s, nil, ErrNotOwner
	}
	return s, paths, nil
}

// recorded returns the login of the approval recorded in the App for t's head when it holds now, else "": the login is
// still an owner and no agent under s, and the recorded digests are those of paths at the head. A base that moved so
// that the pull request changes other protected paths therefore voids it. A record that does not decode approves
// nothing; a failed read is an error, so the decision is retried instead of published on a guess.
func (r *resolver) recorded(ctx context.Context, t Target, s settings.Settings, paths []string) (string, error) {
	raw, err := r.store.Get(ctx, store.Tenant(t.Repo.InstallationID), approvalKey(t))
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inputs: read approval: %w", err)
	}
	var rec approvalRecord
	if json.Unmarshal(raw, &rec) != nil || rec.HeadSHA != t.HeadSHA {
		logOutcome(t, "approval", "unreadable")
		return "", nil
	}
	if rec.Login == "" || s.IsAgent(rec.Login) {
		return "", nil
	}
	ok, err := isOwner(s.Owners, rec.Login, r.member(ctx, t))
	if err != nil {
		return "", fmt.Errorf("inputs: team membership: %w", err)
	}
	if !ok {
		return "", nil
	}
	digests, err := r.headDigests(ctx, t, paths)
	if err != nil || !maps.Equal(rec.Digests, digests) {
		return "", err
	}
	return rec.Login, nil
}

// baseSettings reads the settings of the protected ref. found is false when there is no file or it does not parse:
// a broken settings file is a missing one.
func (r *resolver) baseSettings(ctx context.Context, t Target) (s settings.Settings, found bool, err error) {
	raw, err := r.file(ctx, t, t.BaseRef, settings.Path)
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
