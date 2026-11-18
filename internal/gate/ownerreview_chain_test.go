package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// The owner review of a workflow need on an approved repository, as Home runs it: three probes per need, all three
// needs at once. Every GitHub read takes chainGitHubRTT of virtual time and every Store read chainStoreRTT, so the
// elapsed time is the length of the longest chain of reads that wait for each other.
const (
	chainGitHubRTT = 250 * time.Millisecond
	chainStoreRTT  = 50 * time.Millisecond
)

// chainLog records every read of one review with its virtual start and end.
type chainLog struct {
	mu    sync.Mutex
	start time.Time
	spans []chainSpan
}

type chainSpan struct {
	name       string
	begin, end time.Duration
}

func (l *chainLog) run(name string, read func()) {
	begin := time.Since(l.start)
	read()
	end := time.Since(l.start)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spans = append(l.spans, chainSpan{name, begin, end})
}

// report logs the reads in start order and returns the GitHub and Store counts.
func (l *chainLog) report(t *testing.T) (gitHub, stored int) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	spans := slices.Clone(l.spans)
	slices.SortStableFunc(spans, func(a, b chainSpan) int { return int(a.begin - b.begin) })
	for _, s := range spans {
		if strings.HasPrefix(s.name, "store ") {
			stored++
		} else {
			gitHub++
		}
		t.Logf("%8v → %8v  %s", s.begin, s.end, s.name)
	}
	return gitHub, stored
}

type chainGitHub struct {
	*fakeGitHub
	log *chainLog
}

func (g chainGitHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	var raw []byte
	var blob string
	var err error
	g.log.run(fmt.Sprintf("GitHub File %s @%s", path, ref), func() {
		time.Sleep(chainGitHubRTT)
		raw, blob, err = g.fakeGitHub.File(ctx, r, ref, path)
	})
	return raw, blob, err
}

func (g chainGitHub) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	var sha string
	var err error
	g.log.run("GitHub BranchHead "+branch, func() {
		time.Sleep(chainGitHubRTT)
		sha, err = g.fakeGitHub.BranchHead(ctx, r, branch)
	})
	return sha, err
}

// Pull is the GET and then the compare that gives MergeBaseSHA: two round trips in a row.
func (g chainGitHub) Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	var pl api.Pull
	var err error
	g.log.run(fmt.Sprintf("GitHub Pull #%d", number), func() {
		time.Sleep(2 * chainGitHubRTT)
		pl, err = g.fakeGitHub.Pull(ctx, r, number)
	})
	return pl, err
}

// PullSummary is the GET alone.
func (g chainGitHub) PullSummary(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	var pl api.Pull
	var err error
	g.log.run(fmt.Sprintf("GitHub PullSummary #%d", number), func() {
		time.Sleep(chainGitHubRTT)
		pl, err = g.fakeGitHub.PullSummary(ctx, r, number)
	})
	return pl, err
}

type chainStore struct {
	store.Store
	log *chainLog
}

func (s chainStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	var raw []byte
	var err error
	s.log.run("store Get "+key, func() {
		time.Sleep(chainStoreRTT)
		raw, err = s.Store.Get(ctx, t, key)
	})
	return raw, err
}

// Before the reads were shared and the binding overlapped its reads, the same three needs made 54 GitHub reads and 21
// Store reads in 3.5 s: each probe read the pull request, the default branch and the files again, one after another.
func TestOwnerReviewsOfThreeWorkflowNeedsReadEachFileOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := ownerViewWorld(t, OwnerWorkflow, 7)
		for _, n := range []int{8, 9} {
			p := w.openPull(w.shop, n)
			w.setFile(w.shop, p.head, policyPath, []byte(policy4))
			w.setFile(w.shop, p.head, approvals.WorkflowPath, workflow4(actionBumped, " # v4.0.1"))
		}
		log := &chainLog{start: time.Now()}
		w.gate = compose(chainGitHub{w.gh, log}, w.oidc, chainStore{w.store, log}, w.admit, testAudience, w.clock)
		user := OwnerUser{ID: 1, Login: "olivia", Admin: true}
		target := Target{Repo: w.shop}
		ctx := WithReviewReads(t.Context())

		// As the Home response does: for each need, first on the pull request, first on the default branch, and
		// the workflow change; the needs run at once, and so do their probes.
		var probes sync.WaitGroup
		errs := map[OwnerAction]error{}
		var mu sync.Mutex
		for _, pull := range []int{7, 8, 9} {
			for _, a := range []OwnerAction{{Kind: OwnerFirst, Pull: pull}, {Kind: OwnerFirst}, {Kind: OwnerWorkflow, Pull: pull}} {
				probes.Go(func() {
					err := w.gate.OwnerReview(ctx, target, a, user)
					mu.Lock()
					errs[a] = err
					mu.Unlock()
				})
			}
		}
		probes.Wait()
		elapsed := time.Since(log.start)

		for _, pull := range []int{7, 8, 9} {
			if err := errs[OwnerAction{Kind: OwnerWorkflow, Pull: pull}]; err != nil {
				t.Errorf("workflow review of #%d = %v, want nil", pull, err)
			}
			if err := errs[OwnerAction{Kind: OwnerFirst, Pull: pull}]; !errors.Is(err, ErrOwnerNotDue) {
				t.Errorf("first review of #%d = %v, want not due on an approved repository", pull, err)
			}
		}
		gitHub, stored := log.report(t)
		t.Logf("GitHub reads = %d, Store reads = %d, elapsed = %v", gitHub, stored, elapsed)
		// The critical path: the record, the pull request and the default branch head, the files at the head and the
		// default branch, the base file, and the proposal index.
		if gitHub != 12 || stored != 2 {
			t.Errorf("reads = %d GitHub and %d Store, want 12 and 2", gitHub, stored)
		}
		if want := 2*chainGitHubRTT + 2*chainStoreRTT; elapsed != want {
			t.Errorf("elapsed = %v, want %v: two rounds of GitHub reads and two of Store reads", elapsed, want)
		}
	})
}
