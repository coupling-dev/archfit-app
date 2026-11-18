package gate

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// pullCounter counts the GETs of a pull request.
type pullCounter struct {
	*fakeGitHub
	pulls atomic.Int32
}

func (g *pullCounter) Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	g.pulls.Add(1)
	return g.fakeGitHub.Pull(ctx, r, number)
}

// The workflow review of pull request 7 with the pull request already listed by Home: the review takes it from the
// listing and makes no GET; a listing entry without a field the review reads is not used, and the GET answers.
func TestOpenPullListingSeedsTheReviewOnlyWhenItIsComplete(t *testing.T) {
	for _, tt := range []struct {
		name    string
		blank   func(*api.Pull)
		wantGET int32
	}{
		{"complete entry skips the GET", func(*api.Pull) {}, 0},
		{"entry without a head repository is read again", func(p *api.Pull) { p.HeadRepo = "" }, 1},
		{"entry without a base SHA is read again", func(p *api.Pull) { p.BaseSHA = "" }, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := ownerViewWorld(t, OwnerWorkflow, 7)
			probe := &pullCounter{fakeGitHub: w.gh}
			w.gate = compose(probe, w.oidc, w.store, w.admit, testAudience, w.clock)
			listed := w.gh.pulls[pullKey(w.shop.FullName, 7)]
			tt.blank(&listed)
			ctx := WithReviewReads(t.Context())
			SeedOpenPulls(ctx, w.shop.RepoRef, []api.Pull{listed})

			err := w.gate.OwnerReview(ctx, Target{Repo: w.shop}, OwnerAction{Kind: OwnerWorkflow, Pull: 7}, OwnerUser{Login: "olivia"})
			if tt.wantGET == 0 && err != nil {
				t.Fatalf("review = %v, want nil", err)
			}
			if got := probe.pulls.Load(); got != tt.wantGET {
				t.Fatalf("GETs of #7 = %d, want %d", got, tt.wantGET)
			}
		})
	}
}

// PullSummary is the fake's Pull: the fake keeps one count per read either way.
func (g *pullCounter) PullSummary(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	return g.Pull(ctx, r, number)
}
