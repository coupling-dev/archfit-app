// Package api is the App's only GitHub REST client, offered as small role interfaces over one Client.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/auth"
)

var errNotImplemented = errors.New("api: not implemented")

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
)

type Installation struct {
	ID      int64
	Account string // organization or user login
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

type Pull struct {
	Number       int
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
	UpdatedAt    time.Time
}

type Review struct {
	Reviewer  string // login
	State     string // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "DISMISSED"
	CommitSHA string // the head SHA the review was submitted on
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
	Status      string // "in_progress" | "completed"
	Conclusion  string // "" unless completed
	Title       string
	Summary     string // markdown, max 65535 bytes
	Annotations []Annotation
	StartedAt   time.Time
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

type Actions interface {
	Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error)
	Rerun(ctx context.Context, r RepoRef, runID int64) error
	Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error
	LatestRun(ctx context.Context, r RepoRef, workflowPath, branch string) (WorkflowRun, error)
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

// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct { /* unexported */
}

func New(ts auth.TokenSource, hc *http.Client) *Client {
	return nil
}

var (
	_ Repos    = (*Client)(nil)
	_ Pulls    = (*Client)(nil)
	_ Checks   = (*Client)(nil)
	_ Comments = (*Client)(nil)
	_ Actions  = (*Client)(nil)
	_ Changes  = (*Client)(nil)
	_ Users    = (*Client)(nil)
)

func (*Client) ListInstallations(ctx context.Context) ([]Installation, error) {
	return nil, errNotImplemented
}

func (*Client) ListRepos(ctx context.Context, installationID int64) ([]Repo, error) {
	return nil, errNotImplemented
}

func (*Client) ResolveRepo(ctx context.Context, fullName string) (Repo, error) {
	return Repo{}, errNotImplemented
}

func (*Client) File(ctx context.Context, r RepoRef, ref, path string) (content []byte, blobSHA string, err error) {
	return nil, "", errNotImplemented
}

func (*Client) BranchHead(ctx context.Context, r RepoRef, branch string) (string, error) {
	return "", errNotImplemented
}

func (*Client) Pull(ctx context.Context, r RepoRef, number int) (Pull, error) {
	return Pull{}, errNotImplemented
}

func (*Client) OpenPulls(ctx context.Context, r RepoRef) ([]Pull, error) {
	return nil, errNotImplemented
}

func (*Client) PullByHead(ctx context.Context, r RepoRef, branch string) (Pull, error) {
	return Pull{}, errNotImplemented
}

func (*Client) ChangedFiles(ctx context.Context, r RepoRef, number int) (paths []string, truncated bool, err error) {
	return nil, false, errNotImplemented
}

func (*Client) Reviews(ctx context.Context, r RepoRef, number int) ([]Review, error) {
	return nil, errNotImplemented
}

func (*Client) TeamMember(ctx context.Context, r RepoRef, team, login string) (bool, error) {
	return false, errNotImplemented
}

func (*Client) CheckRuns(ctx context.Context, r RepoRef, headSHA, name string) ([]CheckRun, error) {
	return nil, errNotImplemented
}

func (*Client) CreateCheckRun(ctx context.Context, r RepoRef, c CheckRun) (CheckRun, error) {
	return CheckRun{}, errNotImplemented
}

func (*Client) UpdateCheckRun(ctx context.Context, r RepoRef, c CheckRun) error {
	return errNotImplemented
}

func (*Client) AppComments(ctx context.Context, r RepoRef, pull int) ([]Comment, error) {
	return nil, errNotImplemented
}

func (*Client) CreateComment(ctx context.Context, r RepoRef, pull int, body string) (Comment, error) {
	return Comment{}, errNotImplemented
}

func (*Client) UpdateComment(ctx context.Context, r RepoRef, id int64, body string) error {
	return errNotImplemented
}

func (*Client) Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error) {
	return WorkflowRun{}, errNotImplemented
}

func (*Client) Rerun(ctx context.Context, r RepoRef, runID int64) error {
	return errNotImplemented
}

func (*Client) Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error {
	return errNotImplemented
}

func (*Client) LatestRun(ctx context.Context, r RepoRef, workflowPath, branch string) (WorkflowRun, error) {
	return WorkflowRun{}, errNotImplemented
}

func (*Client) Artifact(ctx context.Context, r RepoRef, runID int64, name string, maxBytes int64) ([]byte, error) {
	return nil, errNotImplemented
}

func (*Client) EnsureBranch(ctx context.Context, r RepoRef, branch, fromRef string) error {
	return errNotImplemented
}

func (*Client) Commit(ctx context.Context, r RepoRef, branch, message string, files []FileChange) (sha string, err error) {
	return "", errNotImplemented
}

func (*Client) EnsurePull(ctx context.Context, r RepoRef, p NewPull) (Pull, error) {
	return Pull{}, errNotImplemented
}

func (*Client) UpdatePullBody(ctx context.Context, r RepoRef, number int, body string) error {
	return errNotImplemented
}

func (*Client) MarkReady(ctx context.Context, r RepoRef, number int) error {
	return errNotImplemented
}

func (*Client) Permission(ctx context.Context, userToken, fullName string) (string, error) {
	return "", errNotImplemented
}

func (*Client) UserRepos(ctx context.Context, userToken string) ([]Repo, error) {
	return nil, errNotImplemented
}
