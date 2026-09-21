// Package onboarding drives customer and managed onboarding; stages are derived from GitHub.
package onboarding

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
)

// Sandbox is the only platform-dependent seam of managed discovery: one ephemeral, isolated
// container job. Implementations must destroy the container and its disk on every exit path,
// must not execute repository scripts, and must give the job no credential except Token.
type Sandbox interface {
	Discover(ctx context.Context, job DiscoveryJob) (DiscoveryResult, error)
}

type DiscoveryJob struct {
	CloneURL    string
	Token       string // short-lived installation token, contents:read
	Ref         string
	ImageDigest string // engine image from the locked manifest
	Timeout     time.Duration
}

type DiscoveryResult struct {
	DraftPolicy []byte   // engine-generated policy YAML
	Notes       []string // engine-authored, no-source
}

// ErrNoSandbox is returned by the default Sandbox until a platform implementation is configured.
var ErrNoSandbox = errors.New("onboarding: managed discovery is not available")

type Stage string

const (
	StageInstalled      Stage = "installed"
	StageWorkflowMerged Stage = "workflow_merged"
	StageReportReceived Stage = "report_received"
	StagePolicyMerged   Stage = "policy_merged"
	StageAdvisory       Stage = "gate_advisory"
	StageRequired       Stage = "gate_required"
)

// Progress is derived from GitHub on every call; nothing is stored.
type Progress struct {
	Stage      Stage
	Diagnostic string // what to do next
	OpenDraft  *proposal.Draft
}

type Onboarding interface {
	Progress(ctx context.Context, r api.Repo) (Progress, error)
	// StartCustomer opens PR 1: the workflow, the settings file with owners, and CODEOWNERS entries.
	StartCustomer(ctx context.Context, r api.Repo, owners []string) (proposal.Draft, error)
	// StartManaged requires ConsentSourceProcessing, opens the onboarding PR and runs the sandbox job.
	StartManaged(ctx context.Context, r api.Repo, owners []string) (proposal.Draft, error)
	// DiscoveryHandler serves POST /v1/discoveries and opens PR 2 with the proposed policy.
	DiscoveryHandler() http.Handler
}

type Deps struct {
	GitHub    *api.Client
	Tokens    auth.TokenSource // RepoReadToken for the sandbox clone
	Receiver  gate.Receiver
	Proposals proposal.Proposals
	Consents  account.Consents
	Sandbox   Sandbox
	BaseURL   string // upload endpoint and OIDC audience written into the generated workflow
	Now       func() time.Time
}

func New(d Deps) Onboarding {
	return nil
}
