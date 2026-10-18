// Package gate is the façade of the merge-gate path: upload handling, webhook reactions and the sweep.
// It re-publishes the child contracts that modules outside gate may use.
package gate

import (
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/store"
)

// Deps are the only things the gate path may touch: GitHub, OIDC, the Store and a clock. On the Store the gate
// path reads the entitlement and writes only gate/feedback's publication leases.
type Deps struct {
	GitHub   *api.Client
	OIDC     oidc.Verifier
	Store    store.Store
	Audience string // the App URL; OIDC audience
	Sweep    Config
	Now      func() time.Time
}

// Gate composes the five children. It holds no state of its own: GitHub holds the dedup keys.
type Gate struct {
	receiver  ingest.Receiver
	resolver  inputs.Resolver
	publisher feedback.Publisher
	sweeper   sweep.Sweeper
	actions   api.Actions
	pulls     api.Pulls
	now       func() time.Time
}

// github is every role of github/api the gate path uses. *api.Client implements it; the façade
// hands the same client to each child under the role it needs.
type github interface {
	api.Repos
	api.Pulls
	api.Actions
	api.Checks
	api.Comments
}

func New(d Deps) *Gate {
	return compose(d.GitHub, d.OIDC, d.Store, d.Audience, d.Sweep, d.Now)
}

// compose wires the children once. A nil clock means time.Now.
func compose(gh github, v oidc.Verifier, s store.Store, audience string, c Config, now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	resolver := inputs.New(gh, gh, s)
	publisher := feedback.New(gh, gh, gh, s, audience, now) // the audience is the App's base URL
	return newGate(newReceiver(gh, v, audience), resolver, publisher,
		sweep.New(gh, gh, gh, resolver, publisher, c), gh, gh, now)
}

func newGate(rc ingest.Receiver, rs inputs.Resolver, p feedback.Publisher, sw sweep.Sweeper, a api.Actions, pl api.Pulls, now func() time.Time) *Gate {
	return &Gate{receiver: rc, resolver: rs, publisher: p, sweeper: sw, actions: a, pulls: pl, now: now}
}

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler {
	return http.HandlerFunc(g.serveUpload)
}

// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *api.Client, v oidc.Verifier, audience string) Receiver {
	return newReceiver(gh, v, audience)
}

func newReceiver(gh github, v oidc.Verifier, audience string) Receiver {
	return ingest.New(gh, gh, gh, v, audience)
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
