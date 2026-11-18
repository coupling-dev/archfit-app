package gate

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// inFlightGitHub records the most calls that were in flight at once. The read sleeps before it takes the fake's lock,
// so calls that overlap in time overlap here, whatever the fake does inside.
type inFlightGitHub struct {
	*fakeGitHub
	mu      sync.Mutex
	flight  int
	maxSeen int
}

func (g *inFlightGitHub) begin() func() {
	g.mu.Lock()
	g.flight++
	g.maxSeen = max(g.maxSeen, g.flight)
	g.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	return func() {
		g.mu.Lock()
		g.flight--
		g.mu.Unlock()
	}
}

func (g *inFlightGitHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	defer g.begin()()
	return g.fakeGitHub.File(ctx, r, ref, path)
}

func (g *inFlightGitHub) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	defer g.begin()()
	return g.fakeGitHub.BranchHead(ctx, r, branch)
}

func (g *inFlightGitHub) Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	defer g.begin()()
	return g.fakeGitHub.Pull(ctx, r, number)
}

func TestOwnerViewReadsItsPullRequestFilesTogether(t *testing.T) {
	w := ownerViewWorld(t, OwnerWorkflow, 7)
	probe := &inFlightGitHub{fakeGitHub: w.gh}
	w.gate = compose(probe, w.oidc, w.store, w.admit, testAudience, w.clock)

	v, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, OwnerAction{Kind: OwnerWorkflow, Pull: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Files) < 4 {
		t.Fatalf("view has %d files, want the pull request's files", len(v.Files))
	}
	if probe.maxSeen < 3 {
		t.Errorf("at most %d GitHub reads in flight at once; the pull request's reads should overlap", probe.maxSeen)
	}
}

// PullSummary is the fake's Pull: the fake keeps one count per read either way.
func (g *inFlightGitHub) PullSummary(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	return g.Pull(ctx, r, number)
}
