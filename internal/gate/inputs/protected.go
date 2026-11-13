package inputs

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

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
