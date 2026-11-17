package gate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const workflowsTestPolicy = "version: 3\nowners: [alice]\nmodules:\n  app:\n    paths: [cmd/**]\n"

// timedHomeGitHub answers every read after latency of virtual time and counts the reads.
type timedHomeGitHub struct {
	pulls   []api.Pull
	latency time.Duration
	reads   atomic.Int64
	fail    map[string]error // ref + ":" + path
}

func (g *timedHomeGitHub) wait() {
	g.reads.Add(1)
	time.Sleep(g.latency)
}

func (g *timedHomeGitHub) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	g.wait()
	return "immutable-tip", nil
}

func (g *timedHomeGitHub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	g.wait()
	if err := g.fail[ref+":"+path]; err != nil {
		return nil, "", err
	}
	switch {
	case path == homePolicyPath:
		return []byte(workflowsTestPolicy), "", nil
	case path == approvals.WorkflowPath && ref == "immutable-tip":
		return []byte("current workflow"), "", nil
	case path == approvals.WorkflowPath:
		return []byte("changed workflow"), "", nil
	}
	return nil, "", api.ErrNotFound
}

func (g *timedHomeGitHub) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) {
	g.wait()
	return g.pulls, nil
}

func workflowsTestPulls(n int) []api.Pull {
	pulls := make([]api.Pull, n)
	for i := range pulls {
		pulls[i] = api.Pull{Number: i + 1, State: "open", BaseRef: "main", HeadSHA: fmt.Sprintf("%040x", i+1), HeadRepo: "acme/app"}
	}
	return pulls
}

// newWorkflowsTestHome is an approved repository whose policy digest matches, so Home lists the workflow changes.
func newWorkflowsTestHome(t *testing.T, gh homeGitHub) HomeViews {
	t.Helper()
	s := store.NewMemory()
	value, err := records.Encode(&records.Repo{ApprovedDigest: policy3.Digest([]byte(workflowsTestPolicy))})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(t.Context(), 1, records.RepoKey("acme/app"), value); err != nil {
		t.Fatal(err)
	}
	return NewHome(gh, s, NewAdmissions(s, []string{"acme"}))
}

var workflowsTestRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}, DefaultBranch: "main"}

// Eight open pull requests each take two reads, one after the other. Serially that is 20 reads in all, including tip,
// policy, open pulls and the current workflow: 2000 ms of virtual time at 100 ms a read. Four pull requests at a time
// make it 8 reads deep: 800 ms.
func TestHomeWorkflowChangesReadPullRequestsTogether(t *testing.T) {
	const latency = 100 * time.Millisecond
	synctest.Test(t, func(t *testing.T) {
		gh := &timedHomeGitHub{pulls: workflowsTestPulls(8), latency: latency}
		h := newWorkflowsTestHome(t, gh)
		start := time.Now()
		out, err := h.Home(t.Context(), workflowsTestRepo)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("reads = %d, response time = %v", gh.reads.Load(), elapsed)
		if want := []int{1, 2, 3, 4, 5, 6, 7, 8}; !reflect.DeepEqual(out.WorkflowPulls, want) {
			t.Fatalf("workflow pulls = %v, want %v", out.WorkflowPulls, want)
		}
		if got := gh.reads.Load(); got != 20 {
			t.Fatalf("reads = %d, want 20", got)
		}
		if elapsed != 8*latency {
			t.Fatalf("Home took %v of virtual time, want %v", elapsed, 8*latency)
		}
	})
}

// The first failing pull request in list order is the error, not the first one to fail in time.
func TestHomeWorkflowChangesReportTheFirstFailureInListOrder(t *testing.T) {
	rateLimited := fmt.Errorf("pull 2: %w", api.ErrRateLimited)
	gh := &timedHomeGitHub{pulls: workflowsTestPulls(8), fail: map[string]error{
		fmt.Sprintf("%040x:%s", 2, approvals.WorkflowPath): rateLimited,
		fmt.Sprintf("%040x:%s", 6, approvals.WorkflowPath): errors.New("unrelated failure"),
	}}
	h := newWorkflowsTestHome(t, gh)
	_, err := h.Home(t.Context(), workflowsTestRepo)
	if !errors.Is(err, api.ErrRateLimited) {
		t.Fatalf("Home error = %v, want the rate limit of pull 2", err)
	}
}
