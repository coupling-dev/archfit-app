package passkey

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// fileReader is the part of api.Repos the owners port reads.
type fileReader interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) (content []byte, blobSHA string, err error)
}

// issueCreator is the part of api.Issues the issues port calls.
type issueCreator interface {
	CreateIssue(ctx context.Context, r api.RepoRef, i api.NewIssue) (api.Issue, error)
}

// NewOwners is the Owners port over GitHub: the owners of the policy file on the default branch.
func NewOwners(files fileReader) Owners { return policyOwners{files: files} }

type policyOwners struct{ files fileReader }

// Owners reads .archfit.yaml at the default branch. A schema 3 file gives its owners. No file, or an Archfit 3
// (version 2) file, is "before the first policy": the onboarding pull request brings the first schema 3 policy, and
// a repository administrator approves it (design 9.2). Any other file is ErrPolicy.
func (o policyOwners) Owners(ctx context.Context, r api.Repo) ([]string, bool, error) {
	if r.DefaultBranch == "" {
		return nil, false, errors.New("passkey: the repository has no default branch")
	}
	raw, _, err := o.files.File(ctx, r.RepoRef, r.DefaultBranch, policy.PolicyPath)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("passkey: read the policy: %w", err)
	}
	if f, err := policy3.Decode(raw); err == nil {
		return f.Owners, true, nil
	}
	// The Archfit 3 reader goes with Archfit 3 (M2 task 25); this check goes with it.
	if policy.Validate(raw) == nil {
		return nil, false, nil
	}
	return nil, false, ErrPolicy
}

// NewIssues is the Issues port over GitHub: the App opens the issue, then assigns it.
func NewIssues(issues issueCreator, triage api.IssueTriage) Issues {
	return appIssues{issues: issues, triage: triage}
}

type appIssues struct {
	issues issueCreator
	triage api.IssueTriage
}

// Open opens the issue and sets its assignees. GitHub drops an assignee it does not allow without an error, so an
// owner without access to the repository is simply not assigned.
func (o appIssues) Open(ctx context.Context, r api.RepoRef, title, body string, assignees []string) error {
	issue, err := o.issues.CreateIssue(ctx, r, api.NewIssue{Title: title, Body: body})
	if err != nil {
		return fmt.Errorf("passkey: open the issue: %w", err)
	}
	if _, _, err := o.triage.SetAssigneesAndLabels(ctx, r, issue.Number, assignees, nil); err != nil {
		return fmt.Errorf("passkey: assign issue %d: %w", issue.Number, err)
	}
	return nil
}
