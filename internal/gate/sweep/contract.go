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
	NotAdmitted                     int  // installations skipped: no operator approved them
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
	// Recheck is rule 5c alone for r, after a push moved its default branch to tip: a passed pull request goes stale
	// when the moved target closes a module cycle with it. It does nothing when the branch is no longer at tip. A rate
	// limit stops it as it stops RunRepo.
	Recheck(ctx context.Context, r api.Repo, tip string, now time.Time) (Summary, error)
}

// Admitter answers whether the App serves an installation; owner is its account login. inputs.Admissions implements it.
type Admitter interface {
	Admitted(ctx context.Context, installationID int64, owner string) (bool, error)
}

// Decisions is the one rule of the sweep that touches decision requests (rule 8). decisions.Requests implements it.
type Decisions interface {
	// Revalidate marks the pending requests of t's pull request stale when their policy digest is no longer accepted.
	Revalidate(ctx context.Context, t inputs.Target, tr inputs.Trusted, now time.Time) error
}

func New(repos api.Repos, pulls api.Pulls, actions api.Actions, r inputs.Resolver, p feedback.Publisher, admit Admitter, d Decisions, c Config) Sweeper {
	if c.MissingUploadAfter == 0 {
		c.MissingUploadAfter = defaultMissingUploadAfter
	}
	if c.WaiverWarnBefore == 0 {
		c.WaiverWarnBefore = defaultWaiverWarnBefore
	}
	return &sweeper{repos: repos, pulls: pulls, actions: actions, resolver: r, publisher: p, admit: admit, decisions: d, cfg: c, graphs: &edgeCache{}}
}
