// Package gate is the façade of the merge-gate path: upload handling, webhook reactions and the sweep.
// It re-publishes the child contracts that modules outside gate may use.
package gate

import (
	"context"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/decisions"
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/store"
)

// Deps are the only things the gate path may touch: GitHub, OIDC, the Store and a clock. On the Store the gate
// path reads the entitlement and the admission, and writes only gate/feedback's publication leases, gate/inputs'
// pusher and owner approval records, and gate/decisions' request records.
type Deps struct {
	GitHub     *api.Client
	OIDC       oidc.Verifier
	Store      store.Store
	Admissions *Admissions // which installations the App serves; nothing runs for any other
	Audience   string      // the App URL; OIDC audience
	Sweep      Config
	Now        func() time.Time
}

// Gate composes the five children. It holds no state of its own: GitHub holds the dedup keys.
type Gate struct {
	receiver   ingest.Receiver
	resolver   inputs.Resolver
	requests   requests
	publisher  feedback.Publisher
	repairer   feedback.Repairer
	sweeper    sweep.Sweeper
	actions    api.Actions
	pulls      api.Pulls
	comments   api.Comments
	admissions admitter
	now        func() time.Time
}

// admitter is the one method of Admissions every gate path asks before it does anything for an installation.
type admitter interface {
	Admitted(ctx context.Context, installationID int64, owner string) (bool, error)
}

// github is every role of github/api the gate path uses. *api.Client implements it; the façade
// hands the same client to each child under the role it needs.
type github interface {
	api.Repos
	api.Pulls
	api.Actions
	api.Checks
	api.Comments
	api.Issues
}

func New(d Deps) *Gate {
	return compose(d.GitHub, d.OIDC, d.Store, d.Admissions, d.Audience, d.Sweep, d.Now)
}

// compose wires the children once. A nil clock means time.Now.
func compose(gh github, v oidc.Verifier, s store.Store, adm admitter, audience string, c Config, now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	resolver := inputs.New(gh, gh, s)
	reqs := decisions.New(s, gh, gh, now)
	publisher := feedback.New(gh, gh, gh, s, audience, now) // the audience is the App's base URL
	return newGate(newReceiver(gh, v, audience, adm), resolver, reqs, publisher, feedback.NewRepairer(gh, gh, gh, gh, s, now),
		sweep.New(gh, gh, gh, resolver, publisher, adm, reqs, c), gh, gh, gh, adm, now)
}

func newGate(rc ingest.Receiver, rs inputs.Resolver, rq requests, p feedback.Publisher, rp feedback.Repairer, sw sweep.Sweeper, a api.Actions, pl api.Pulls, cm api.Comments, adm admitter, now func() time.Time) *Gate {
	return &Gate{receiver: rc, resolver: rs, requests: rq, publisher: p, repairer: rp, sweeper: sw, actions: a, pulls: pl, comments: cm, admissions: adm, now: now}
}

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler {
	return http.HandlerFunc(g.serveUpload)
}

// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *api.Client, v oidc.Verifier, audience string, adm *Admissions) Receiver {
	return newReceiver(gh, v, audience, adm)
}

func newReceiver(gh github, v oidc.Verifier, audience string, adm admitter) Receiver {
	return ingest.New(gh, gh, gh, v, audience, adm)
}

// ClassifyDisplayReason republishes verdict's pure display-metadata classification through the gate facade.
// It does not decide an outcome, change admission, or validate report contents.
func ClassifyDisplayReason(code, category string) (reportDerived, known bool) {
	return verdict.ClassifyDisplayReason(verdict.ReasonCode(code), verdict.Category(category))
}

// Re-published child contracts. The owning child's module.md is normative.

// Admissions, Admission and AdmissionKey are owned by gate/inputs.
type (
	Admissions = inputs.Admissions
	Admission  = inputs.Admission
)

const AdmissionKey = inputs.AdmissionKey

// NewAdmissions is owned by gate/inputs.
func NewAdmissions(s store.Store, auto []string) *Admissions { return inputs.NewAdmissions(s, auto) }

// EntitlementKey is owned by gate/inputs.
const EntitlementKey = inputs.EntitlementKey

// Target, Candidate and the refusals of an owner approval are owned by gate/inputs.
type (
	Target    = inputs.Target
	Candidate = inputs.Candidate
)

var (
	ErrNothingToApprove = inputs.ErrNothingToApprove
	ErrForkPull         = inputs.ErrForkPull
	ErrAgentIdentity    = inputs.ErrAgentIdentity
	ErrNotOwner         = inputs.ErrNotOwner
	ErrDigestsChanged   = inputs.ErrDigestsChanged
)

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
