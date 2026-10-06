// Package sweep repairs gate state that webhooks and uploads missed.
package sweep

import (
	"context"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	defaultMissingUploadAfter = 60 * time.Minute
	defaultWaiverWarnBefore   = 7 * 24 * time.Hour
)

type Config struct {
	MissingUploadAfter time.Duration // 60 minutes
	WaiverWarnBefore   time.Duration // 7 days
}

// Summary holds counts only; it is safe to log.
type Summary struct {
	Repos, Pulls                    int
	Pended, TimedOut, Rerun, Warned int
	Suspended                       int
	Errors                          int  // repositories or pulls skipped after an error; details are in the log
	RateLimited                     bool // the pass stopped early on a GitHub rate limit
}

type Sweeper interface {
	// Run re-derives the expected check state of every open PR from GitHub and repairs differences.
	// A rate limit stops the pass with a nil error and Summary.RateLimited: the next pass continues the work.
	Run(ctx context.Context, now time.Time) (Summary, error)
	// RunRepo is the same pass limited to one repository (used when the protected ref changes).
	// A rate limit stops it with Summary.RateLimited and an error that wraps api.ErrRateLimited, so the caller
	// retries the repository.
	RunRepo(ctx context.Context, r api.Repo, now time.Time) (Summary, error)
}

func New(repos api.Repos, pulls api.Pulls, actions api.Actions, r inputs.Resolver, p feedback.Publisher, c Config) Sweeper {
	if c.MissingUploadAfter == 0 {
		c.MissingUploadAfter = defaultMissingUploadAfter
	}
	if c.WaiverWarnBefore == 0 {
		c.WaiverWarnBefore = defaultWaiverWarnBefore
	}
	return &sweeper{repos: repos, pulls: pulls, actions: actions, resolver: r, publisher: p, cfg: c}
}
