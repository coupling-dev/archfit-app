// Package gate is the façade of the merge-gate path: upload handling, webhook reactions and the sweep.
// It re-publishes the child contracts that modules outside gate may use.
package gate

import (
	"context"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/decisions"
	"github.com/alexei-led/archfit-app/internal/gate/drift"
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests" // gate's own requests type is Archfit 3's
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/store"
)

// Deps are the only things the gate path may touch: GitHub, OIDC, the Store and a clock. The Store holds entitlements,
// admissions, pusher attribution, bounded gate/inputs owner decisions and reviewer-request notifications, gate/decisions
// agent requests, and gate/feedback publication leases.
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
	actions    sweep.Runs
	repos      api.Repos
	pulls      api.Pulls
	queues     api.MergeQueues
	comments   api.Comments
	admissions admitter
	now        func() time.Time
	v4         *upload4           // the envelope 2 upload path
	asks       *asks.Service      // request lines: issues and the status waiting (M2 task 16)
	proposals  *proposals.Service // owners' answers and the policy pull request (M2 task 19)
	approvals  *approvals.Service // the judgement of a default-branch push that changes the policy (M2 task 20)
	recheckGH  sweep.AnswerGitHub // the re-check after an answer (M2 task 21)
	drift      *drift.Service     // the drift issue from a push run's repository block (M2 task 22)
	store      store.Store
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
	api.MergeQueues
	api.WorkflowRuns
	api.Commits
	api.IssueTriage
	api.Collaborators
	api.Changes
	api.Merges
}

func New(d Deps) *Gate {
	return compose(d.GitHub, d.OIDC, d.Store, d.Admissions, d.Audience, d.Sweep, d.Now)
}

// compose wires the children once. A nil clock means time.Now.
func compose(gh github, v oidc.Verifier, s store.Store, adm admitter, audience string, c Config, now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	resolver := inputs.New(gh, gh, gh, s)
	reqs := decisions.New(s, gh, gh, now)
	publisher := feedback.New(gh, gh, gh, s, audience, now) // the audience is the App's base URL
	g := newGate(newReceiver(gh, v, audience, adm), resolver, reqs, publisher, feedback.NewRepairer(gh, gh, gh, gh, s, now),
		sweep.New(gh, gh, gh, resolver, publisher, adm, reqs, c), gh, gh, gh, gh, gh, adm, now)
	// One Archfit 4 publisher, so an upload and a request line take the same head lease.
	publisher4 := feedback.New4(gh, gh, gh, s, audience, now)
	g.v4 = newUpload4(ingest.NewV2(gh, gh, gh, gh, gh, v, audience, adm), gh, gh, s, publisher4)
	g.asks = asks.New(gh, s, publisher4)
	g.proposals = proposals.New(gh, s, now)
	g.approvals = approvals.New(gh, s)
	g.drift = drift.New(gh, s)
	g.recheckGH, g.store = gh, s
	return g
}

func newGate(rc ingest.Receiver, rs inputs.Resolver, rq requests, p feedback.Publisher, rp feedback.Repairer, sw sweep.Sweeper, a sweep.Runs, repos api.Repos, pl api.Pulls, q api.MergeQueues, cm api.Comments, adm admitter, now func() time.Time) *Gate {
	return &Gate{receiver: rc, resolver: rs, requests: rq, publisher: p, repairer: rp, sweeper: sw, actions: a, repos: repos, pulls: pl, queues: q, comments: cm, admissions: adm, now: now}
}

// UploadHandler serves POST /v1/reports: envelope 2 uploads on the Archfit 4 path, every other upload on the
// envelope 1 path.
func (g *Gate) UploadHandler() http.Handler {
	return http.HandlerFunc(g.serveReports)
}

// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *api.Client, v oidc.Verifier, audience string, adm *Admissions) Receiver {
	return newReceiver(gh, v, audience, adm)
}

func newReceiver(gh github, v oidc.Verifier, audience string, adm admitter) Receiver {
	return ingest.New(gh, gh, gh, gh, v, audience, adm)
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
	Target          = inputs.Target
	Candidate       = inputs.Candidate
	DecisionAction  = inputs.DecisionAction
	Objection       = inputs.Objection
	DecisionHistory = inputs.DecisionHistory
)

var (
	ErrNothingToApprove     = inputs.ErrNothingToApprove
	ErrForkPull             = inputs.ErrForkPull
	ErrAgentIdentity        = inputs.ErrAgentIdentity
	ErrNotOwner             = inputs.ErrNotOwner
	ErrDigestsChanged       = inputs.ErrDigestsChanged
	ErrActiveObjections     = inputs.ErrActiveObjections
	ErrOwnerDecisionStale   = inputs.ErrOwnerDecisionStale
	ErrDecisionReason       = inputs.ErrDecisionReason
	ErrNoApprovalToWithdraw = inputs.ErrNoApprovalToWithdraw
	ErrDecisionInvalid      = inputs.ErrDecisionInvalid
	ErrDecisionHistoryFull  = inputs.ErrDecisionHistoryFull
)

const (
	DecisionApprove        = inputs.DecisionApprove
	DecisionRequestChanges = inputs.DecisionRequestChanges
	DecisionWithdraw       = inputs.DecisionWithdraw
	DecisionOverride       = inputs.DecisionOverride
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
