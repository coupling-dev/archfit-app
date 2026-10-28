// Package api is the App's only GitHub REST client, offered as small role interfaces over one Client.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// RepoRef addresses a repository inside an authenticated installation.
// Callers build it only from a verified webhook, verified OIDC claims, or an authorized session.
type RepoRef struct {
	InstallationID int64
	FullName       string // "owner/name"
}

type Repo struct {
	RepoRef
	ID            int64
	Private       bool
	DefaultBranch string
}

var (
	ErrNotFound    = errors.New("github: not found")
	ErrRateLimited = errors.New("github: rate limit exhausted") // returned after bounded backoff

	// ErrUserTokenRejected is GitHub's 401 on a user's OAuth token: it expired or was revoked. The token cannot
	// recover, so the call is not repeated and the user must sign in again.
	ErrUserTokenRejected = errors.New("github: user token rejected")
)

// Status returns the HTTP status of a GitHub answer that mapped to no sentinel, or 0 for any other error.
func Status(err error) int {
	if se, ok := errors.AsType[*statusError](err); ok {
		return se.status
	}
	return 0
}

// Permanent reports whether err is a GitHub answer that the same call cannot change when it is repeated:
// ErrNotFound, or a 4xx status other than 401, 403, 408, 409 and 429. A 403 stays retryable because a secondary
// rate limit can arrive without the headers that mark it. Rate limits, 5xx, transport and every other error are
// not permanent.
func Permanent(err error) bool {
	if errors.Is(err, ErrNotFound) {
		return true
	}
	se, ok := errors.AsType[*statusError](err)
	if !ok || se.status < 400 || se.status > 499 {
		return false
	}
	switch se.status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return false
	}
	return true
}

type Installation struct {
	ID          int64
	Account     string    // organization or user login
	AccountType string    // "Organization" or "User"
	CreatedAt   time.Time // when the App was installed
}

// Repos reads installations, repositories and file contents.
type Repos interface {
	ListInstallations(ctx context.Context) ([]Installation, error)
	ListRepos(ctx context.Context, installationID int64) ([]Repo, error)
	// ResolveRepo finds the installation that covers fullName using the App JWT.
	ResolveRepo(ctx context.Context, fullName string) (Repo, error)
	// File returns the raw bytes and git blob SHA of path at ref. ErrNotFound if absent.
	File(ctx context.Context, r RepoRef, ref, path string) (content []byte, blobSHA string, err error)
	// BranchHead returns the head commit SHA of branch. ErrNotFound if the branch does not exist.
	BranchHead(ctx context.Context, r RepoRef, branch string) (string, error)
}

// BranchRules is what GitHub enforces on one branch, as far as this App can read it.
type BranchRules struct {
	// Contexts are the status checks that active rulesets, and classic branch protection when it is readable, require.
	Contexts []string
	// Protected reports that GitHub marks the branch protected but does not show which checks it requires: classic
	// branch protection may need the Administration permission to read, which the App does not hold. Contexts may then
	// lack a required check.
	Protected bool
}

// Rules reads what GitHub enforces on a branch.
type Rules interface {
	RequiredChecks(ctx context.Context, r RepoRef, branch string) (BranchRules, error)
}

type Pull struct {
	Number       int
	Title        string
	Author       string // the login of the user who opened it
	State        string // "open" | "closed"
	Merged       bool
	Draft        bool
	Body         string
	URL          string // html_url
	HeadSHA      string
	HeadRef      string
	HeadRepo     string // "owner/name"; differs from the base repository for forks
	BaseRef      string
	BaseSHA      string
	MergeBaseSHA string
	// MergeCommitSHA is GitHub's test merge commit of the head into the base: refs/pull/N/merge. "" while GitHub has not
	// computed it.
	MergeCommitSHA string
	// Mergeable is GitHub's answer whether the head merges cleanly; nil while GitHub has not computed it.
	Mergeable *bool
	UpdatedAt time.Time
}

type Review struct {
	Reviewer  string // login
	State     string // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "DISMISSED"
	CommitSHA string // the head SHA the review was submitted on
	// SubmittedAt is when the review was submitted; zero when GitHub did not say. A dismissed review keeps the time it
	// was submitted, not the time it was dismissed.
	SubmittedAt time.Time
}

type Pulls interface {
	Pull(ctx context.Context, r RepoRef, number int) (Pull, error)
	OpenPulls(ctx context.Context, r RepoRef) ([]Pull, error)
	// PullByHead returns the newest PR, in any state, whose head is branch in this repository. ErrNotFound if none.
	PullByHead(ctx context.Context, r RepoRef, branch string) (Pull, error)
	// ChangedFiles lists changed paths; a rename lists both the old and the new path.
	// truncated is true when GitHub's file list was cut off: callers must assume any path changed.
	ChangedFiles(ctx context.Context, r RepoRef, number int) (paths []string, truncated bool, err error)
	Reviews(ctx context.Context, r RepoRef, number int) ([]Review, error)
	// TeamMember reports whether login belongs to "org/team-slug".
	TeamMember(ctx context.Context, r RepoRef, team, login string) (bool, error)
}

type Annotation struct {
	Path    string
	Line    int
	Level   string // "failure" | "warning" | "notice"
	Message string
}

type CheckRun struct {
	ID          int64 // 0 on create
	Name        string
	HeadSHA     string
	ExternalID  string
	DetailsURL  string // the "Details" link GitHub shows on the check; "" = GitHub's default
	Status      string // "in_progress" | "completed"
	Conclusion  string // "" unless completed
	Title       string
	Summary     string // markdown, max 65535 bytes
	Annotations []Annotation
	StartedAt   time.Time
	// AnnotationCount is read-only: the annotations GitHub holds for the run, set by CheckRuns.
	// Create and update never send it; GitHub appends the Annotations of each request.
	AnnotationCount int
}

type Checks interface {
	// CheckRuns returns only check runs created by this App.
	CheckRuns(ctx context.Context, r RepoRef, headSHA, name string) ([]CheckRun, error)
	CreateCheckRun(ctx context.Context, r RepoRef, c CheckRun) (CheckRun, error)
	UpdateCheckRun(ctx context.Context, r RepoRef, c CheckRun) error
}

type Comment struct {
	ID   int64
	Body string
}

// Comments are PR conversation comments authored by the App.
type Comments interface {
	AppComments(ctx context.Context, r RepoRef, pull int) ([]Comment, error)
	CreateComment(ctx context.Context, r RepoRef, pull int, body string) (Comment, error)
	UpdateComment(ctx context.Context, r RepoRef, id int64, body string) error
}

type Issue struct {
	Number int
	Body   string
}

type NewIssue struct {
	Title, Body string
	Assignee    string // one login; "" assigns nobody
}

// Issues are repository issues opened by the App. A pull request is never one: GitHub lists pull requests among
// issues, and they are left out.
type Issues interface {
	// AppIssues lists the open issues the App opened.
	AppIssues(ctx context.Context, r RepoRef) ([]Issue, error)
	CreateIssue(ctx context.Context, r RepoRef, i NewIssue) (Issue, error)
	UpdateIssueBody(ctx context.Context, r RepoRef, number int, body string) error
	// CloseIssue closes the issue number. A number that names a pull request is an error, and nothing is closed.
	CloseIssue(ctx context.Context, r RepoRef, number int) error
}

type WorkflowRun struct {
	ID           int64
	Attempt      int
	Event        string
	HeadSHA      string
	HeadRepo     string
	WorkflowPath string // e.g. ".github/workflows/archfit.yaml"
	Status       string
	Conclusion   string
	PullRequests []int // PR numbers GitHub associates with the run
}

// RunFilter narrows LatestRun. Each non-empty field is sent as the GitHub query parameter of the same name.
type RunFilter struct {
	Branch string
	Event  string // e.g. "push", "workflow_dispatch"
	Status string // a run status or conclusion, e.g. "completed", "success"
}

type Actions interface {
	Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error)
	Rerun(ctx context.Context, r RepoRef, runID int64) error
	Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error
	// LatestRun is the newest run of the workflow that matches every non-empty field of f.
	LatestRun(ctx context.Context, r RepoRef, workflowPath string, f RunFilter) (WorkflowRun, error)
	// Artifact downloads the named artifact of a run, capped at maxBytes.
	Artifact(ctx context.Context, r RepoRef, runID int64, name string, maxBytes int64) ([]byte, error)
}

type FileChange struct {
	Path    string
	Content []byte
}

type NewPull struct {
	Title, Body string
	Head, Base  string
	Draft       bool
}

// Changes writes branches, commits and pull requests as the App.
type Changes interface {
	// EnsureBranch creates branch from fromRef if it does not exist.
	EnsureBranch(ctx context.Context, r RepoRef, branch, fromRef string) error
	Commit(ctx context.Context, r RepoRef, branch, message string, files []FileChange) (sha string, err error)
	// EnsurePull returns the open PR for Head, creating it if needed.
	EnsurePull(ctx context.Context, r RepoRef, p NewPull) (Pull, error)
	UpdatePullBody(ctx context.Context, r RepoRef, number int, body string) error
	MarkReady(ctx context.Context, r RepoRef, number int) error
}

// Users answers questions with a user's OAuth token; nothing here uses App credentials.
type Users interface {
	// Permission returns "admin" | "write" | "read" | "none" for the user on the repository.
	Permission(ctx context.Context, userToken, fullName string) (string, error)
	// UserRepos lists repositories the user can access through App installations.
	UserRepos(ctx context.Context, userToken string) ([]Repo, error)
}

var (
	_ Repos    = (*Client)(nil)
	_ Rules    = (*Client)(nil)
	_ Pulls    = (*Client)(nil)
	_ Checks   = (*Client)(nil)
	_ Comments = (*Client)(nil)
	_ Issues   = (*Client)(nil)
	_ Actions  = (*Client)(nil)
	_ Changes  = (*Client)(nil)
	_ Users    = (*Client)(nil)
)
