// Package webhook verifies and normalizes GitHub webhook deliveries.
package webhook

import "errors"

var errNotImplemented = errors.New("webhook: not implemented")

type EventKind string

const (
	PullOpened          EventKind = "pull_opened"       // opened, reopened, ready_for_review
	PullSynchronized    EventKind = "pull_synchronized" // new head SHA
	PullClosed          EventKind = "pull_closed"
	ReviewChanged       EventKind = "review_changed" // submitted, edited, dismissed
	DefaultBranchPush   EventKind = "default_branch_push"
	InstallationAdded   EventKind = "installation_added"   // created, or repositories added
	InstallationRemoved EventKind = "installation_removed" // deleted
	ReposRemoved        EventKind = "repos_removed"
	Ignored             EventKind = "ignored"
)

// Event is the normalized form of a verified delivery. Identity fields come from the signed payload only.
type Event struct {
	Kind           EventKind
	DeliveryID     string
	InstallationID int64
	Repo           string // "owner/name", "" for installation events
	RepoPrivate    bool
	PullRequest    int
	HeadSHA        string
	HeadRepo       string // differs from Repo for forks
	BaseRef        string
	ChangedPaths   []string // default_branch_push only
}

// Verify checks X-Hub-Signature-256 over body in constant time.
func Verify(secret []byte, signatureHeader string, body []byte) error {
	return errNotImplemented
}

// Parse normalizes a verified delivery. Unknown event types yield Kind == Ignored, not an error.
func Parse(eventType, deliveryID string, body []byte) (Event, error) {
	return Event{}, errNotImplemented
}
