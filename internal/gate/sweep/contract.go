// Package sweep repairs gate state that webhooks and uploads missed.
package sweep

import (
	"context"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
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
	RateLimited                     bool // sweep stopped early; the next sweep continues
}

type Sweeper interface {
	// Run re-derives the expected check state of every open PR from GitHub and repairs differences.
	Run(ctx context.Context, now time.Time) (Summary, error)
	// RunRepo is the same pass limited to one repository (used when the protected ref changes).
	RunRepo(ctx context.Context, r api.Repo, now time.Time) (Summary, error)
}

func New(repos api.Repos, pulls api.Pulls, actions api.Actions, r inputs.Resolver, p feedback.Publisher, c Config) Sweeper {
	return nil
}
