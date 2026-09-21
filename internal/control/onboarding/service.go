package onboarding

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
)

const (
	onboardingBranch = "archfit/onboarding"
	policyBranch     = "archfit/policy"
	workflowPath     = ".github/workflows/archfit.yaml"
	stateCheckName   = "archfit/state" // published only by the App, only for an accepted upload

	sandboxTimeout = 10 * time.Minute
	stalledAfter   = sandboxTimeout + time.Minute
	writeTimeout   = time.Minute // GitHub writes that report a job outcome, after the job context is spent
	maxManagedJobs = 2           // per process; ceiling: one instance (see module.md, Change Vectors)
)

var errNoOwners = errors.New("onboarding: at least one owner is required")

// roles are the GitHub role interfaces this module reads through; *api.Client implements all of them.
type roles struct {
	repos   api.Repos
	actions api.Actions
	checks  api.Checks
	pulls   api.Pulls
}

type service struct {
	gh         roles
	tokens     auth.TokenSource
	receiver   gate.Receiver
	proposals  proposal.Proposals
	consents   account.Consents
	sandbox    Sandbox
	baseURL    string
	now        func() time.Time
	jobTimeout time.Duration
	slots      chan struct{} // one token per running managed job
	jobs       sync.WaitGroup
}

func newService(d Deps, gh roles) *service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &service{
		gh:         gh,
		tokens:     d.Tokens,
		receiver:   d.Receiver,
		proposals:  d.Proposals,
		consents:   d.Consents,
		sandbox:    d.Sandbox,
		baseURL:    d.BaseURL,
		now:        now,
		jobTimeout: sandboxTimeout,
		slots:      make(chan struct{}, maxManagedJobs),
	}
}

func changeFor(r api.Repo, branch, title, body, message string, files []api.FileChange) proposal.Change {
	return proposal.Change{Repo: r.RepoRef, Branch: branch, Base: r.DefaultBranch, Title: title, Body: body, Message: message, Files: files}
}

// file reads path at the default branch; absent is (nil, false, nil).
func (s *service) file(ctx context.Context, r api.Repo, path string) ([]byte, bool, error) {
	raw, _, err := s.gh.repos.File(ctx, r.RepoRef, r.DefaultBranch, path)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return raw, true, nil
}

// errClass is what logs say about an error: error text may carry repository content.
func errClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, api.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, api.ErrNotFound):
		return "not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "other"
}
