// Package feedback publishes a gate outcome as check runs and one PR comment.
package feedback

import (
	"context"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// Receipt is what a published gate check says about itself. It is stored only in GitHub:
// the check run external_id is "v1:<run_id>:<attempt>:<report_digest>", the rest is a hidden
// JSON marker in the check summary. The check is its own receipt.
type Receipt struct {
	RunID          int64  `json:"run_id"`
	RunAttempt     int    `json:"run_attempt"`
	ReportDigest   string `json:"report_digest"`
	PolicyDigest   string `json:"policy_digest"`
	BaselineDigest string `json:"baseline_digest"`
	LabelsDigest   string `json:"labels_digest"`
	// IdentityKey names what the engine measured and under which inputs (the report's result identity); "" for a
	// report without one. It is a fact a later decision compares (a moved target, a repair task); it never suppresses
	// a publication: the identity is the engine's own claim in a customer-attested report.
	IdentityKey          string             `json:"identity_key,omitempty"`
	EarliestWaiverExpiry time.Time          `json:"earliest_waiver_expiry"`
	Conclusion           verdict.Conclusion `json:"conclusion"`
	Reason               verdict.ReasonCode `json:"reason"`
}

// Publication is one complete piece of feedback for one head SHA.
type Publication struct {
	Target   inputs.Target
	Identity manifest.Identity // claimed engine and image identity; zero when there was no upload
	Outcome  verdict.Outcome
	Receipt  Receipt
	Report   *report.Report // decoded claims; presentation separately checks source authority from all reason codes
	// PolicyChange is the engine's classification of a change to the protected policy, for the policy change card;
	// nil when the pull request changes no policy or the upload carries none. Display only: it decides nothing.
	PolicyChange *report.PolicyChange
	Trusted      inputs.Trusted
	stateCheckID int64 // display-only ID returned by this publication's successful check write; never an admission input
	// Resolve, when set, reads the trusted inputs again inside the head's publication lease, before anything is
	// written. When the result differs from Trusted in any field (a digest, the owner approval, the settings) or no
	// longer accepts a Receipt digest, the decision was computed from inputs that are no longer current: Publish
	// writes nothing and returns ErrStaleInputs, so it never replaces a publication computed from newer inputs. nil
	// skips the check; sweep corrections, which are never a success, leave it nil.
	Resolve func(context.Context) (inputs.Trusted, error)
}

// ErrStaleInputs is returned by Publish when Publication.Resolve shows that the trusted inputs changed after the
// decision. Nothing was written; the caller decides again from the new inputs.
var ErrStaleInputs = fmt.Errorf("feedback: trusted inputs changed after the decision: %w", errUnwritten)

// CheckState is the gate check (or, from AdvisoryState, the state check) currently on a head SHA.
type CheckState struct {
	Exists    bool
	Pending   bool // in_progress, no upload yet
	StartedAt time.Time
	Receipt   Receipt // zero when Pending
}

type Publisher interface {
	// Publish writes archfit/state, archfit/gate (only when Settings.Gate is required) and the
	// single PR comment. It is idempotent per Receipt and never overwrites feedback of a newer head or run.
	Publish(ctx context.Context, p Publication) error
	// Pending creates the in_progress gate check if none exists on the head SHA.
	Pending(ctx context.Context, t inputs.Target) error
	State(ctx context.Context, t inputs.Target) (CheckState, error)
	// AdvisoryState is State for archfit/state: an advisory pull request has no archfit/gate, and its receipt is the
	// one on the state check. It is never Pending: only archfit/gate is created ahead of an upload.
	AdvisoryState(ctx context.Context, t inputs.Target) (CheckState, error)
	// Warn adds or refreshes the waiver-expiry notice in the PR comment. Idempotent.
	Warn(ctx context.Context, t inputs.Target, expiry time.Time) error
	// Reconcile reads the gate check and calls decide with it, then publishes what decide returns, all inside the
	// head's publication lease: no other publication for the head can land between the read, the decision and the
	// write. A nil publication writes nothing. decide reads everything else it needs itself, inside the lease.
	Reconcile(ctx context.Context, t inputs.Target, decide Decision) error
}

// Decision decides, from the gate check as it is inside the publication lease, what Reconcile publishes; nil
// publishes nothing. An error writes nothing and is returned by Reconcile.
type Decision func(ctx context.Context, current CheckState) (*Publication, error)

// New returns a Publisher that is safe for concurrent use, also across instances that share s: Publish, Pending,
// Warn and Reconcile hold a per-head publication lease in s, so that two publishers for one head SHA never interleave
// their reads and writes. It keeps no other state between calls; GitHub holds the receipts.
//
// appURL is the App's base URL without a trailing slash (ARCHFIT_BASE_URL); the PR comment links to the App's pull
// request page under it. "" writes no link.
func New(checks api.Checks, comments api.Comments, pulls api.Pulls, s store.Store, appURL string, now func() time.Time) Publisher {
	return &serialized{
		next:  &publisher{checks: checks, comments: comments, pulls: pulls, appURL: appURL, now: now},
		store: s,
		now:   now,
		wait:  pollWait,
	}
}

// Repairer keeps the repair issue of the default branch: one open issue per branch that says what a push broke and
// closes when a push passes. It is a notification and never a gate.
type Repairer interface {
	// Repair opens, updates or closes the repair issue for p, the publication of a push to the default branch, once p
	// is published. It never fails: GitHub's answer (the Issues permission missing, a rate limit) is logged, and what
	// was published stays as it is.
	Repair(ctx context.Context, p Publication)
}

// NewRepairer returns a Repairer that is safe for concurrent use: Repair holds the head's publication lease in s, as
// Publish does, so two publications of one head never interleave their issue reads and writes.
func NewRepairer(checks api.Checks, comments api.Comments, issues api.Issues, repos api.Repos, s store.Store, now func() time.Time) Repairer {
	return &repairer{
		lease:    &serialized{next: &publisher{checks: checks, now: now}, store: s, now: now, wait: pollWait},
		comments: comments,
		issues:   issues,
		repos:    repos,
	}
}
