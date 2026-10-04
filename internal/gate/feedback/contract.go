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
	RunID                int64              `json:"run_id"`
	RunAttempt           int                `json:"run_attempt"`
	ReportDigest         string             `json:"report_digest"`
	PolicyDigest         string             `json:"policy_digest"`
	BaselineDigest       string             `json:"baseline_digest"`
	LabelsDigest         string             `json:"labels_digest"`
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
	Report   *report.Report // nil when there is no usable report
	Trusted  inputs.Trusted
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

// CheckState is the gate check currently on a head SHA.
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
	// Warn adds or refreshes the waiver-expiry notice in the PR comment. Idempotent.
	Warn(ctx context.Context, t inputs.Target, expiry time.Time) error
}

// New returns a Publisher that is safe for concurrent use, also across instances that share s: Publish, Pending and
// Warn hold a per-head publication lease in s, so that two publishers for one head SHA never interleave their reads
// and writes. It keeps no other state between calls; GitHub holds the receipts.
func New(checks api.Checks, comments api.Comments, pulls api.Pulls, s store.Store, now func() time.Time) Publisher {
	return &serialized{
		next:  &publisher{checks: checks, comments: comments, pulls: pulls, now: now},
		store: s,
		now:   now,
		wait:  pollWait,
	}
}
