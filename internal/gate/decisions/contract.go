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
	ErrTooLarge   = errors.New("decisions: request is too large to store")
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
}

func New(s store.Store, c api.Comments) *Requests { return &Requests{store: s, comments: c} }

// Revalidate is the sweep's rule for one pull request: a pending request whose policy digest the protected ref no
// longer accepts goes stale (policy changed), and a pending request whose status comment is missing gets it.
func (r *Requests) Revalidate(ctx context.Context, t inputs.Target, tr inputs.Trusted, now time.Time) error {
	return r.eachPending(ctx, t, func(rec Record) error {
		if !inputs.Accepts(tr.PolicyDigests, rec.PolicyDigest) {
			return r.Settle(ctx, t, rec.FindingID, rec.PolicyDigest, decision.StatusStale, decision.ReasonPolicyChanged, 0, now)
		}
		if rec.StatusCommentID == 0 {
			return r.publish(ctx, t, rec)
		}
		return nil
	})
}
