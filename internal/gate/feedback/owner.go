package feedback

import (
	"context"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// OwnerCheckWriter is the GitHub role needed to publish a recorded owner approval.
type OwnerCheckWriter interface {
	CreateCheckRun(context.Context, api.RepoRef, api.CheckRun) (api.CheckRun, error)
}

// WriteOwnerCheck publishes a recorded approval under the same head lease as run feedback.
// An upload already publishing finishes first; an upload after this write re-reads the approval.
func WriteOwnerCheck(ctx context.Context, gh OwnerCheckWriter, st store.Store, r api.Repo, check api.CheckRun, now func() time.Time) error {
	s := serialized{store: st, now: now, wait: pollWait}
	return s.hold(ctx, inputs.Target{Repo: r, HeadSHA: check.HeadSHA}, func(ctx context.Context) error {
		_, err := gh.CreateCheckRun(ctx, r.RepoRef, check)
		return err
	})
}
