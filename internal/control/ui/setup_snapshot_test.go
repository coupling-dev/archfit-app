package ui

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

type tipCounter struct{ reads atomic.Int32 }

func (c *tipCounter) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	c.reads.Add(1)
	return sha(1), nil
}
func (*tipCounter) File(context.Context, api.RepoRef, string, string) ([]byte, string, error) {
	return nil, "", api.ErrNotFound
}

// The setup page lets everything it reads for one request share the default branch's tip: the setup probe and the
// approval summary under it ask for the tip through the request's snapshot, so GitHub is asked once.
func TestSetupAPIGivesSetupOneSnapshotOfTheDefaultBranch(t *testing.T) {
	e := newEnv(t)
	var tips tipCounter
	e.setups.onCtx = func(ctx context.Context, repo api.Repo) {
		for range 2 { // the probe and the approval summary
			if _, err := gate.ReadHomeSnapshot(ctx, &tips, repo); err != nil {
				t.Error(err)
			}
		}
	}
	wantStatus(t, e.as("alice", permRead).get(setupAPI), http.StatusOK)
	if got := tips.reads.Load(); got != 1 {
		t.Fatalf("default branch head read %d times, want once", got)
	}
}
