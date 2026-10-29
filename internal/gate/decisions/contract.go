// Package decisions keeps the decision requests unattended agents file on pull requests: one record per request,
// and one status comment per record. A record grants nothing; an owner decides.
package decisions

import (
	"context"
	"errors"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

const (
	// MaxPendingPerPull and MaxPendingPerRepo cap the pending records, so a flood of requests fills neither the
	// Store nor the thread.
	MaxPendingPerPull = 20
	MaxPendingPerRepo = 200
	// maxStatusComments caps the status comments of one pull request, answers without a record included.
	maxStatusComments = 40

	ownerSentence = "Only an architecture owner can approve this request."
)

var (
	// ErrNoRecord is Settle's answer for a request that has no record.
	ErrNoRecord = errors.New("decisions: no such request")
	// ErrNotPending is Settle's answer for a record that is not pending: it never moves again.
	ErrNotPending = errors.New("decisions: request is not pending")
	// ErrTooLarge means the record would not fit one Store value.
	ErrTooLarge = errors.New("decisions: request is too large to store")
	// ErrChanged is SettleAt's answer for a record whose head is not the one the caller saw: it settled or was
	// refiled meanwhile, and the caller's reason no longer holds.
	ErrChanged    = errors.New("decisions: request changed")
	errContention = errors.New("decisions: request records changed concurrently")
)

// Record is one request, as decoded: not the comment it came in. The proposed policy is kept beside it (Policy).
type Record struct {
	PullRequest       int             `json:"pull_request"`
	FindingID         string          `json:"finding_id"`
	RuleID            string          `json:"rule_id"`
	EdgeFrom          string          `json:"edge_from"`
	EdgeTo            string          `json:"edge_to"`
	HeadSHA           string          `json:"head_sha"`
	PolicyDigest      string          `json:"policy_digest"`
	Why               string          `json:"why"`
	AlternativesTried []string        `json:"alternatives_tried"`
	ProposalDigest    string          `json:"proposal_digest"` // policy.Digest of the proposed policy
	Requester         string          `json:"requester"`       // whoever commented; never an approval
	CommentID         int64           `json:"comment_id"`
	Status            decision.Status `json:"status"`
	Reason            string          `json:"reason,omitempty"` // the stale reason, or why a request was declined
	ProposalPR        int             `json:"proposal_pr,omitempty"`
	StatusCommentID   int64           `json:"status_comment_id,omitempty"`
	Published         string          `json:"published,omitempty"` // the status line last written to the thread
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// Filing is a decoded request and who filed it.
type Filing struct {
	Request   decision.Request
	Requester string
	CommentID int64
}

// Outcome says what File did.
type Outcome string

const (
	Filed     Outcome = "filed"     // a new record
	Refiled   Outcome = "refiled"   // the record was replaced by a new head and is pending again
	Duplicate Outcome = "duplicate" // the same request on the same head: nothing written
	Sticky    Outcome = "sticky"    // declined or approved: it stays until the policy digest changes
	Capped    Outcome = "capped"    // too many pending requests: ignored
)

// Requests reads and writes the records and the status comments.
type Requests struct {
	store    store.Store
	comments api.Comments
	pulls    api.Pulls
	now      func() time.Time // the clock of the status lease only
	// wait pauses between polls of a held lease. Tests replace it to observe contention.
	wait func(context.Context) error
}

// New returns Requests over the Store, the App's comments and the pull requests. pulls is read to confirm that a
// head or a close still holds before a request goes stale for it; now clocks the status lease.
func New(s store.Store, c api.Comments, p api.Pulls, now func() time.Time) *Requests {
	return &Requests{store: s, comments: c, pulls: p, now: now, wait: pollWait}
}

// Revalidate is the sweep's rule for one pull request: a pending request whose policy digest the protected ref no
// longer accepts goes stale (policy changed), and one whose head is not the pull request's live head goes stale (head
// moved). The live head is read, not taken from t: the sweep lists the pull requests once and reaches this one later,
// so t.HeadSHA can be older than a request filed since. It also writes any status comment that is missing or out of date.
func (r *Requests) Revalidate(ctx context.Context, t inputs.Target, tr inputs.Trusted, now time.Time) error {
	return r.walkLive(ctx, t, func(rec Record, live api.Pull) (bool, error) {
		switch {
		case headMoved(rec, live): // a missed or racing synchronize
			return r.settleIfStill(ctx, t, rec, live, headMoved, decision.ReasonHeadMoved, now)
		case !inputs.Accepts(tr.PolicyDigests, rec.PolicyDigest):
			return true, r.SettleAt(ctx, t, rec.FindingID, rec.PolicyDigest, rec.HeadSHA, decision.StatusStale, decision.ReasonPolicyChanged, 0, now)
		}
		return false, nil
	})
}
