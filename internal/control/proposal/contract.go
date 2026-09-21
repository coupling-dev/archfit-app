// Package proposal turns a set of file changes into one idempotent draft pull request.
package proposal

import (
	"context"
	"errors"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Change is a set of file edits proposed as a branch plus a draft pull request.
type Change struct {
	Repo    api.RepoRef
	Branch  string // deterministic, e.g. "archfit/onboarding"; the idempotency key
	Base    string // base branch
	Title   string
	Body    string
	Message string
	Files   []api.FileChange
}

type DraftState string

const (
	DraftNone     DraftState = "none"
	DraftOpen     DraftState = "draft"           // draft PR
	DraftInReview DraftState = "awaiting_review" // ready for review, not merged
	DraftMerged   DraftState = "published"
	DraftClosed   DraftState = "closed"
)

type Draft struct {
	Number  int
	State   DraftState
	HeadSHA string
	URL     string
}

type Proposals interface {
	// Propose creates or updates the branch and its draft PR. Retrying never opens a second PR.
	// expectHeadSHA, when not "", must equal the branch head or ErrStale is returned.
	Propose(ctx context.Context, c Change, expectHeadSHA string) (Draft, error)
	Get(ctx context.Context, r api.RepoRef, branch string) (Draft, error)
	SetBody(ctx context.Context, r api.RepoRef, number int, body string) error
	Ready(ctx context.Context, r api.RepoRef, number int) error
}

// ErrStale means the branch moved since the caller read it; the caller must reload and re-apply.
var ErrStale = errors.New("proposal: branch head changed")

// ValidBranch reports whether branch is a legal draft branch name: it matches
// ^archfit/[A-Za-z0-9._/-]+$, has no empty, "." or ".." path segment (so no "//" and no trailing "/").
func ValidBranch(branch string) bool {
	if !branchPattern.MatchString(branch) {
		return false
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(branch, branchPrefix), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// ValidHeadSHA reports whether sha is a full commit SHA: exactly 40 lowercase hex characters.
// "" is not valid; a caller whose own contract allows "no draft yet" checks for "" itself.
func ValidHeadSHA(sha string) bool {
	return headSHAPattern.MatchString(sha)
}

func New(gh api.Changes, pulls api.Pulls) Proposals {
	return &service{gh: gh, pulls: pulls}
}
