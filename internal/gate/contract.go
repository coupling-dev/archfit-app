// Package gate is the façade of the merge-gate path: upload handling, webhook reactions and the sweep.
// It re-publishes the child contracts that modules outside gate may use.
package gate

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

var errNotImplemented = errors.New("gate: not implemented")

// Deps are the only things the gate path may touch: GitHub, OIDC, the Store (read-only) and a clock.
type Deps struct {
	GitHub   *api.Client
	OIDC     oidc.Verifier
	Store    store.Store
	Audience string // the App URL; OIDC audience
	Sweep    Config
	Now      func() time.Time
}

type Gate struct{}

func New(d Deps) *Gate {
	return nil
}

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler {
	return nil
}

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// re-run on a review change when the last receipt was owner_approval_missing, and a one-repository
// sweep when a default-branch push changes a protected path.
func (g *Gate) HandleEvent(ctx context.Context, e webhook.Event) error {
	return errNotImplemented
}

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error) {
	return Summary{}, errNotImplemented
}

// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *api.Client, v oidc.Verifier, audience string) Receiver {
	return nil
}

// Re-published child contracts. The owning child's module.md is normative.

// EntitlementKey is owned by gate/inputs.
const EntitlementKey = inputs.EntitlementKey

// Entitlement is owned by gate/inputs.
type Entitlement = inputs.Entitlement

// Upload, Binding, Problem, Receiver and Rejection are owned by gate/ingest.
type (
	Binding   = ingest.Binding
	Problem   = ingest.Problem
	Upload    = ingest.Upload
	Rejection = ingest.Rejection
	Receiver  = ingest.Receiver
)

const (
	ProblemEnvelopeMismatch = ingest.ProblemEnvelopeMismatch
	ProblemUnsupportedEvent = ingest.ProblemUnsupportedEvent
	ProblemFork             = ingest.ProblemFork
	ProblemReportInvalid    = ingest.ProblemReportInvalid
)

// Config, Summary and Sweeper are owned by gate/sweep.
type (
	Config  = sweep.Config
	Summary = sweep.Summary
	Sweeper = sweep.Sweeper
)
