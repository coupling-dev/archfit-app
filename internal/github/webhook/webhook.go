// Package webhook verifies and normalizes GitHub webhook deliveries.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

type EventKind string

const (
	PullOpened          EventKind = "pull_opened"       // opened, reopened, ready_for_review
	PullSynchronized    EventKind = "pull_synchronized" // new head SHA
	PullClosed          EventKind = "pull_closed"
	ReviewChanged       EventKind = "review_changed" // submitted, edited, dismissed
	DefaultBranchPush   EventKind = "default_branch_push"
	BranchPush          EventKind = "branch_push"          // a push to a branch other than the default branch
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
	Branch         string   // push kinds only: the branch, without "refs/heads/"
	Pusher         string   // push kinds only: the login that moved the branch; never a git author or committer
	PusherType     string   // push kinds only: the payload's pusher_type ("user", or a deploy key); "" when absent
	ChangedPaths   []string // default_branch_push only
}

// Header names of a GitHub webhook delivery. Callers read these headers and pass their values to VerifyAny and Parse.
const (
	HeaderEvent     = "X-GitHub-Event"
	HeaderDelivery  = "X-GitHub-Delivery"
	HeaderSignature = "X-Hub-Signature-256"
)

const signaturePrefix = "sha256="

// Fixed texts only: an error never holds the secret, a digest, or payload bytes.
var (
	errNoSecret           = errors.New("webhook: empty secret")
	errSignatureMalformed = errors.New("webhook: malformed signature header")
	errSignatureMismatch  = errors.New("webhook: signature mismatch")
)

// VerifyAny checks X-Hub-Signature-256 over body in constant time. It accepts a body signed with any of secrets:
// the current secret and, during a rotation, the previous one.
// It checks every secret, so the time taken does not show which one matched. An empty list or an empty secret
// fails closed.
func VerifyAny(secrets [][]byte, signatureHeader string, body []byte) error {
	if len(secrets) == 0 {
		return errNoSecret
	}
	for _, secret := range secrets {
		if len(secret) == 0 {
			return errNoSecret
		}
	}
	digest, ok := strings.CutPrefix(signatureHeader, signaturePrefix)
	if !ok || len(digest) != hex.EncodedLen(sha256.Size) {
		return errSignatureMalformed
	}
	got, err := hex.DecodeString(digest)
	if err != nil {
		return errSignatureMalformed
	}
	matched := false
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		matched = hmac.Equal(got, mac.Sum(nil)) || matched
	}
	if !matched {
		return errSignatureMismatch
	}
	return nil
}
