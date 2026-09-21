package proposal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"unicode"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	branchPrefix = "archfit/"
	pullOpen     = "open"
)

type service struct {
	gh    api.Changes
	pulls api.Pulls
}

var _ Proposals = (*service)(nil)

func (s *service) Propose(ctx context.Context, c Change, expectHeadSHA string) (d Draft, err error) {
	defer func() { logOutcome(ctx, "propose", c.Repo, c.Branch, d, err) }()

	if err := validate(c); err != nil {
		return Draft{}, err
	}
	if err := s.gh.EnsureBranch(ctx, c.Repo, c.Branch, c.Base); err != nil {
		return Draft{}, fmt.Errorf("proposal: ensure branch %s: %w", c.Branch, err)
	}
	if expectHeadSHA != "" {
		current, err := s.Get(ctx, c.Repo, c.Branch)
		if err != nil {
			return Draft{}, err
		}
		open := current.State == DraftOpen || current.State == DraftInReview
		if !open || current.HeadSHA != expectHeadSHA {
			return Draft{}, fmt.Errorf("%w: %s is %s at %q", ErrStale, c.Branch, current.State, current.HeadSHA)
		}
	}
	sha, err := s.gh.Commit(ctx, c.Repo, c.Branch, c.Message, c.Files)
	if err != nil {
		return Draft{}, fmt.Errorf("proposal: commit to %s: %w", c.Branch, err)
	}
	pull, err := s.gh.EnsurePull(ctx, c.Repo, api.NewPull{
		Title: c.Title, Body: c.Body, Head: c.Branch, Base: c.Base, Draft: true,
	})
	if err != nil {
		return Draft{}, fmt.Errorf("proposal: ensure pull for %s: %w", c.Branch, err)
	}
	return Draft{Number: pull.Number, State: stateOf(pull), HeadSHA: sha, URL: pull.URL}, nil
}

func (s *service) Get(ctx context.Context, r api.RepoRef, branch string) (Draft, error) {
	pull, err := s.pulls.PullByHead(ctx, r, branch)
	if errors.Is(err, api.ErrNotFound) {
		return Draft{State: DraftNone}, nil
	}
	if err != nil {
		return Draft{}, fmt.Errorf("proposal: pull for %s: %w", branch, err)
	}
	return Draft{Number: pull.Number, State: stateOf(pull), HeadSHA: pull.HeadSHA, URL: pull.URL}, nil
}

func (s *service) SetBody(ctx context.Context, r api.RepoRef, number int, body string) (err error) {
	defer func() { logOutcome(ctx, "set_body", r, "", Draft{Number: number}, err) }()
	if err := s.gh.UpdatePullBody(ctx, r, number, body); err != nil {
		return fmt.Errorf("proposal: update body of pull %d: %w", number, err)
	}
	return nil
}

func (s *service) Ready(ctx context.Context, r api.RepoRef, number int) (err error) {
	defer func() { logOutcome(ctx, "ready", r, "", Draft{Number: number}, err) }()
	if err := s.gh.MarkReady(ctx, r, number); err != nil {
		return fmt.Errorf("proposal: mark pull %d ready: %w", number, err)
	}
	return nil
}

// stateOf maps an existing pull; first match wins. A state that is neither merged nor open counts as closed.
func stateOf(p api.Pull) DraftState {
	switch {
	case p.Merged:
		return DraftMerged
	case p.State != pullOpen:
		return DraftClosed
	case p.Draft:
		return DraftOpen
	default:
		return DraftInReview
	}
}

// validate names the offending field or file index only: titles, bodies, messages, paths and
// contents are customer text and must not reach errors or logs.
func validate(c Change) error {
	owner, name, ok := strings.Cut(c.Repo.FullName, "/")
	switch {
	case c.Repo.InstallationID <= 0:
		return errors.New("proposal: invalid change: installation ID must be positive")
	case !ok || owner == "" || name == "" || strings.Contains(name, "/"):
		return errors.New("proposal: invalid change: repository must be owner/name")
	case !strings.HasPrefix(c.Branch, branchPrefix) || c.Branch == branchPrefix:
		return errors.New("proposal: invalid change: branch must start with " + branchPrefix)
	case strings.Contains(c.Branch, "..") || strings.ContainsFunc(c.Branch, invalidBranchRune):
		return errors.New("proposal: invalid change: branch has .., a space or a control character")
	case c.Base == "":
		return errors.New("proposal: invalid change: base is empty")
	case c.Branch == c.Base:
		return errors.New("proposal: invalid change: branch equals base")
	case c.Title == "":
		return errors.New("proposal: invalid change: title is empty")
	case c.Message == "":
		return errors.New("proposal: invalid change: message is empty")
	case len(c.Files) == 0:
		return errors.New("proposal: invalid change: no files")
	}
	seen := make(map[string]bool, len(c.Files))
	for i, f := range c.Files {
		// fs.ValidPath: relative, slash-separated, no empty, "." or ".." element.
		if f.Path == "." || !fs.ValidPath(f.Path) {
			return fmt.Errorf("proposal: invalid change: file %d: path must be relative and must not contain a .. element", i)
		}
		if seen[f.Path] {
			return fmt.Errorf("proposal: invalid change: file %d: duplicate path", i)
		}
		seen[f.Path] = true
	}
	return nil
}

func invalidBranchRune(r rune) bool {
	return r == ' ' || unicode.IsControl(r)
}

// logOutcome logs identifiers and an outcome class only. Error text stays out: a counterpart error
// may quote a GitHub response, and file contents must never reach the logs.
func logOutcome(ctx context.Context, op string, r api.RepoRef, branch string, d Draft, err error) {
	outcome, level := "ok", slog.LevelInfo
	switch {
	case err == nil:
	case errors.Is(err, ErrStale):
		outcome = "stale"
	case errors.Is(err, api.ErrRateLimited):
		outcome, level = "rate_limited", slog.LevelWarn
	case errors.Is(err, api.ErrNotFound):
		outcome, level = "not_found", slog.LevelWarn
	default:
		outcome, level = "error", slog.LevelWarn
	}
	slog.Log(ctx, level, "proposal", "op", op, "repo", r.FullName, "branch", branch,
		"pull", d.Number, "sha", d.HeadSHA, "outcome", outcome)
}
