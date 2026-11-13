// Package drift keeps the one drift issue of a repository (design 7.1, M2 task 22) from the repository block of a push
// run: opened when the default branch has errors that no exception covers, edited in place after each push, and closed
// when none is left in a tree the check read whole.
package drift

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// policyPath is the policy file; its owners are assigned to the issue.
const policyPath = ".archfit.yaml"

// Label marks the drift issue.
const Label = "archfit-drift"

// maxAssignees is GitHub's limit of assignees per issue.
const maxAssignees = 10

// What Pushed did with a push run's block.
const (
	None    = "none"    // no block, nothing to say, or the issue already describes this head
	Opened  = "opened"  // the issue was opened
	Updated = "updated" // the issue was edited, and reopened if someone closed it
	Closed  = "closed"  // no error is left in a tree the check read whole
)

// GitHub is what the drift issue needs.
type GitHub interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error)
	CreateIssue(ctx context.Context, r api.RepoRef, i api.NewIssue) (api.Issue, error)
	UpdateIssueBody(ctx context.Context, r api.RepoRef, number int, body string) error
	CloseIssue(ctx context.Context, r api.RepoRef, number int) error
	ReopenIssue(ctx context.Context, r api.RepoRef, number int) error
	CreateComment(ctx context.Context, r api.RepoRef, number int, body string) (api.Comment, error)
	SetAssigneesAndLabels(ctx context.Context, r api.RepoRef, number int, assignees, labels []string) ([]string, []string, error)
}

// Service keeps the drift issues.
type Service struct {
	gh    GitHub
	store store.Store
}

// New returns a Service over gh and s.
func New(gh GitHub, s store.Store) *Service { return &Service{gh: gh, store: s} }

// Run is one push run the App accepted as it is, at the default branch's tip: its head and its repository block.
type Run struct {
	Repo  api.Repo
	Head  string
	State *result.Repository // nil: the run carries no state, which is never clean
}

// Pushed applies the repository block of run to the drift issue:
//   - no block: nothing changes, and the issue is never closed;
//   - drift: the issue is opened, or edited in place (and reopened);
//   - no drift, but places the check could not read: an open issue is edited to say what was not read, never closed;
//   - no drift and every place read: the issue is closed with a note.
//
// The issue is written on GitHub first and recorded after, so a failed record is redone by the retried upload. An
// error is a failed read or write; the caller retries.
func (s *Service) Pushed(ctx context.Context, run Run) (string, error) {
	if run.State == nil {
		return None, nil
	}
	t, name := store.Tenant(run.Repo.InstallationID), run.Repo.FullName
	var rec records.Repo
	if err := records.Load(ctx, s.store, t, records.RepoKey(name), &rec); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return None, fmt.Errorf("drift: read the repository record: %w", err)
	}
	drift, unread := run.State.Drift, run.State.Unread
	switch {
	case len(drift) > 0 && rec.DriftIssue == 0:
		return s.open(ctx, run)
	case rec.DriftIssue == 0, rec.DriftHead == run.Head:
		return None, nil
	case len(drift) > 0 || len(unread) > 0:
		return s.update(ctx, run, rec.DriftIssue)
	}
	return s.close(ctx, run, rec.DriftIssue)
}

func (s *Service) open(ctx context.Context, run Run) (string, error) {
	ref := run.Repo.RepoRef
	owners, err := s.owners(ctx, ref, run.Head)
	if err != nil {
		return None, err
	}
	created, err := s.gh.CreateIssue(ctx, ref, api.NewIssue{Title: title(run.Repo.DefaultBranch), Body: body(run)})
	if err != nil {
		return None, fmt.Errorf("drift: open the issue: %w", err)
	}
	recorded, err := records.DriftOpened(ctx, s.store, store.Tenant(run.Repo.InstallationID), run.Repo.FullName,
		created.Number, run.Head)
	if err != nil {
		return None, fmt.Errorf("drift: record the issue: %w", err)
	}
	if recorded != created.Number {
		// Another delivery opened the issue first: this one closes, naming it.
		s.note(ctx, run.Repo, created.Number, duplicateNote(recorded))
		if err := s.gh.CloseIssue(ctx, ref, created.Number); err != nil {
			logFailure(ctx, run.Repo, "close a second issue", err)
		}
		return None, nil
	}
	s.triage(ctx, run.Repo, created.Number, owners)
	return Opened, nil
}

func (s *Service) update(ctx context.Context, run Run, issue int) (string, error) {
	ref := run.Repo.RepoRef
	if err := s.gh.UpdateIssueBody(ctx, ref, issue, body(run)); err != nil {
		return None, fmt.Errorf("drift: edit the issue: %w", err)
	}
	// Drift is never accepted by silence (design 7.2): an issue someone closed opens again while drift is left.
	if err := s.gh.ReopenIssue(ctx, ref, issue); err != nil {
		return None, fmt.Errorf("drift: reopen the issue: %w", err)
	}
	if err := records.DriftUpdated(ctx, s.store, store.Tenant(run.Repo.InstallationID), run.Repo.FullName, issue, run.Head); err != nil {
		return None, fmt.Errorf("drift: record the issue: %w", err)
	}
	return Updated, nil
}

func (s *Service) close(ctx context.Context, run Run, issue int) (string, error) {
	ref := run.Repo.RepoRef
	if err := s.gh.CloseIssue(ctx, ref, issue); err != nil {
		return None, fmt.Errorf("drift: close the issue: %w", err)
	}
	if err := records.DriftClosed(ctx, s.store, store.Tenant(run.Repo.InstallationID), run.Repo.FullName, issue); err != nil {
		return None, fmt.Errorf("drift: record the closed issue: %w", err)
	}
	s.note(ctx, run.Repo, issue, closedNote(run.Repo.DefaultBranch, run.Head))
	return Closed, nil
}

// owners are the owners of the policy at head. A policy that cannot be read assigns nobody: the issue still opens.
func (s *Service) owners(ctx context.Context, ref api.RepoRef, head string) ([]string, error) {
	raw, _, err := s.gh.File(ctx, ref, head, policyPath)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("drift: read the policy: %w", err)
	}
	pol, err := policy3.Decode(raw)
	if err != nil {
		return nil, nil
	}
	return pol.Owners, nil
}

// triage assigns the owners and sets the label. It is a notification, never authority: a failure is logged. Owners
// GitHub cannot assign (no access to the repository) leave the label alone.
func (s *Service) triage(ctx context.Context, r api.Repo, issue int, owners []string) {
	var logins []string
	for _, o := range owners {
		login := strings.TrimPrefix(strings.TrimSpace(o), "@")
		if login != "" && !strings.Contains(login, "/") && !slices.Contains(logins, login) && len(logins) < maxAssignees {
			logins = append(logins, login)
		}
	}
	_, _, err := s.gh.SetAssigneesAndLabels(ctx, r.RepoRef, issue, logins, []string{Label})
	if errors.Is(err, api.ErrUnprocessable) {
		_, _, err = s.gh.SetAssigneesAndLabels(ctx, r.RepoRef, issue, nil, []string{Label})
	}
	if err != nil {
		logFailure(ctx, r, "assign the owners", err)
	}
}

// note posts a comment on the issue. It is a notification: a failure is logged.
func (s *Service) note(ctx context.Context, r api.Repo, issue int, text string) {
	if _, err := s.gh.CreateComment(ctx, r.RepoRef, issue, text); err != nil {
		logFailure(ctx, r, "note on the issue", err)
	}
}

// logFailure logs a failed notification step: no repository name, only ids and the HTTP status.
func logFailure(ctx context.Context, r api.Repo, step string, err error) {
	slog.Default().WarnContext(ctx, "drift step failed", "installation_id", r.InstallationID,
		"repository_id", r.ID, "step", step, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
}
