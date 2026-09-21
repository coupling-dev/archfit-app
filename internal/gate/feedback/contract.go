// Package feedback publishes a gate outcome as check runs and one PR comment.
package feedback

import (
	"context"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
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
}

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

func New(checks api.Checks, comments api.Comments, pulls api.Pulls, now func() time.Time) Publisher {
	return nil
}
