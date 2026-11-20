package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/envelope"
)

// pullPageChainGitHub is the GitHub side of a pull page with the virtual round trip of each of its reads.
type pullPageChainGitHub struct {
	*pullReadGitHub
	log *chainLog
}

func (g pullPageChainGitHub) PullSummary(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	var pl api.Pull
	var err error
	g.log.run(fmt.Sprintf("GitHub PullSummary #%d", number), func() {
		time.Sleep(chainGitHubRTT)
		pl, err = g.pullReadGitHub.PullSummary(ctx, r, number)
	})
	return pl, err
}

func (g pullPageChainGitHub) CheckRuns(ctx context.Context, r api.RepoRef, head, name string) ([]api.CheckRun, error) {
	var checks []api.CheckRun
	var err error
	g.log.run("GitHub CheckRuns "+head, func() {
		time.Sleep(chainGitHubRTT)
		checks, err = g.pullReadGitHub.CheckRuns(ctx, r, head, name)
	})
	return checks, err
}

func (g pullPageChainGitHub) Artifact(ctx context.Context, r api.RepoRef, run int64, name string, limit int64) ([]byte, error) {
	var raw []byte
	var err error
	g.log.run(fmt.Sprintf("GitHub Artifact run %d", run), func() {
		time.Sleep(chainGitHubRTT)
		raw, err = g.pullReadGitHub.Artifact(ctx, r, run, name, limit)
	})
	return raw, err
}

// The serial chain of a pull page whose result is in the artifact, and of one whose result is already published. The
// elapsed time is the length of the longest chain of reads that wait for each other.
func TestPullPageChainOfReads(t *testing.T) {
	raw, err := os.ReadFile("../wire/result/testdata/result/pass.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := envelope.PayloadDigest(raw)
	for _, tc := range []struct {
		name                  string
		published             bool
		wantGitHub, wantStore int
		wantElapsed           time.Duration
	}{
		// Before: PullSummary, CheckRuns, record, publication, index, two requests and the artifact in a row, 1 s.
		// Now the requests load beside PullSummary and CheckRuns beside the record, the publication and the artifact.
		{name: "artifact read", published: false, wantGitHub: 3, wantStore: 5, wantElapsed: chainGitHubRTT + 2*chainStoreRTT + chainGitHubRTT},
		// Before 750 ms: the same without the artifact.
		{name: "published result", published: true, wantGitHub: 2, wantStore: 5, wantElapsed: 2 * chainGitHubRTT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				mem := store.NewMemory()
				r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
				put := func(key string, v records.Value) {
					t.Helper()
					b, err := records.Encode(v)
					if err != nil {
						t.Fatal(err)
					}
					if err := mem.Put(ctx, store.Tenant(1), key, b); err != nil {
						t.Fatal(err)
					}
				}
				put(records.PullKey(r.FullName, 4), &records.Pull{Schema: records.SchemaPull, HeadSHA: "head", RunID: 5, RunAttempt: 1,
					Status: "pass", PolicyDigest: "accepted", PayloadDigest: digest})
				if tc.published {
					put(records.PublicationKey(r.FullName, 4, "head"), &records.Publication{HeadSHA: "head", RunID: 5, RunAttempt: 1,
						Result: raw, Digest: digest})
				}
				index := &records.Index{Schema: records.SchemaRequestIndex}
				for _, n := range []int{1, 2} {
					index.Entries = append(index.Entries, records.Entry{Key: records.RequestKey(r.FullName, n), ID: fmt.Sprint(n)})
					put(records.RequestKey(r.FullName, n), &records.Request{Schema: records.SchemaRequest, State: "open"})
				}
				put(records.RequestIndexKey(r.FullName), index)

				log := &chainLog{start: time.Now()}
				gh := pullPageChainGitHub{&pullReadGitHub{pull: api.Pull{HeadSHA: "head"}, artifact: raw}, log}
				got, err := NewPullViews(gh, chainStore{Store: mem, log: log}).PullPage(ctx, r, 4)
				elapsed := time.Since(log.start)
				if err != nil {
					t.Fatal(err)
				}
				if got.View == nil || got.View.Status != "pass" {
					t.Fatalf("view = %+v, want pass", got.View)
				}
				gitHub, stored := log.report(t)
				t.Logf("GitHub reads = %d, Store reads = %d, elapsed = %v", gitHub, stored, elapsed)
				if gitHub != tc.wantGitHub || stored != tc.wantStore {
					t.Errorf("reads = %d GitHub and %d Store, want %d and %d", gitHub, stored, tc.wantGitHub, tc.wantStore)
				}
				if elapsed != tc.wantElapsed {
					t.Errorf("elapsed = %v, want %v", elapsed, tc.wantElapsed)
				}
			})
		})
	}
}

// pullPageFaults fails the reads a test names and counts the request records read.
type pullPageFaults struct {
	pullReadGitHub
	summaryErr, checksErr error
}

func (g *pullPageFaults) PullSummary(ctx context.Context, r api.RepoRef, n int) (api.Pull, error) {
	if g.summaryErr != nil {
		return api.Pull{}, g.summaryErr
	}
	return g.pullReadGitHub.PullSummary(ctx, r, n)
}

func (g *pullPageFaults) CheckRuns(ctx context.Context, r api.RepoRef, head, name string) ([]api.CheckRun, error) {
	if g.checksErr != nil {
		return nil, g.checksErr
	}
	return g.pullReadGitHub.CheckRuns(ctx, r, head, name)
}

// readsStore counts Get by key.
type readsStore struct {
	store.Store
	mu    sync.Mutex
	reads map[string]int
}

func (s *readsStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.mu.Lock()
	s.reads[key]++
	s.mu.Unlock()
	return s.Store.Get(ctx, t, key)
}

func TestPullPageRequestsAndFailures(t *testing.T) {
	r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
	errPull, errChecks, errArtifact := errors.New("pull"), errors.New("checks"), errors.New("artifact")
	for _, tc := range []struct {
		name string
		// ids are the entries of the request index; waiting lists the pull requests each request record names; a request with no record is unreadable.
		ids         []string
		waiting     map[int][]int
		summaryErr  error
		checksErr   error
		withRecord  bool // a record with a run whose artifact fails
		wantWaiting []int
		wantErr     string
		wantErrIs   error
		notRead     []int
	}{
		{name: "sorted once", ids: []string{"9", "3", "9", "5"}, waiting: map[int][]int{9: {4}, 3: {4, 7}, 5: {8}}, wantWaiting: []int{3, 9}},
		{name: "no request waits", ids: []string{"3"}, waiting: map[int][]int{3: {8}}, wantWaiting: []int{}},
		{name: "unreadable request", ids: []string{"3", "5"}, waiting: map[int][]int{3: {4}}, wantErrIs: records.ErrNoRecord},
		{name: "entry that names no request stops the page", ids: []string{"3", "x", "5"}, waiting: map[int][]int{3: {4}, 5: {4}}, wantErr: "pull: invalid request index", notRead: []int{5}},
		{name: "unreadable request before an invalid entry wins", ids: []string{"3", "x"}, wantErrIs: records.ErrNoRecord},
		{name: "pull request error", summaryErr: errPull, ids: []string{"3"}, waiting: map[int][]int{3: {4}}, wantErrIs: errPull},
		{name: "check run error beats a stored result error", checksErr: errChecks, withRecord: true, wantErrIs: errChecks},
		{name: "stored result error", withRecord: true, wantErrIs: errArtifact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				mem := &readsStore{Store: store.NewMemory(), reads: map[string]int{}}
				put := func(key string, v records.Value) {
					t.Helper()
					b, err := records.Encode(v)
					if err != nil {
						t.Fatal(err)
					}
					if err := mem.Put(ctx, store.Tenant(1), key, b); err != nil {
						t.Fatal(err)
					}
				}
				index := &records.Index{Schema: records.SchemaRequestIndex}
				for i, id := range tc.ids {
					index.Entries = append(index.Entries, records.Entry{Key: fmt.Sprint("key", i), ID: id})
				}
				put(records.RequestIndexKey(r.FullName), index)
				for n, pulls := range tc.waiting {
					put(records.RequestKey(r.FullName, n), &records.Request{Schema: records.SchemaRequest, State: "open", Waiting: pulls})
				}
				if tc.withRecord {
					put(records.PullKey(r.FullName, 4), &records.Pull{Schema: records.SchemaPull, HeadSHA: "head", RunID: 5, RunAttempt: 1, Status: "pass", PolicyDigest: "accepted"})
				}
				gh := &pullPageFaults{pullReadGitHub: pullReadGitHub{pull: api.Pull{HeadSHA: "head"}, artifactErr: errArtifact}, summaryErr: tc.summaryErr, checksErr: tc.checksErr}
				got, err := NewPullViews(gh, mem).PullPage(ctx, r, 4)
				switch {
				case tc.wantErrIs != nil:
					if !errors.Is(err, tc.wantErrIs) {
						t.Fatalf("error = %v, want %v", err, tc.wantErrIs)
					}
				case tc.wantErr != "":
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("error = %v, want %q", err, tc.wantErr)
					}
				case err != nil:
					t.Fatal(err)
				default:
					if !slices.Equal(got.Waiting, tc.wantWaiting) {
						t.Fatalf("waiting = %v, want %v", got.Waiting, tc.wantWaiting)
					}
				}
				for n, count := range map[int]int{3: 1, 9: 1} {
					if got := mem.reads[records.RequestKey(r.FullName, n)]; got > count {
						t.Errorf("request %d read %d times, want at most once", n, got)
					}
				}
				for _, n := range tc.notRead {
					if got := mem.reads[records.RequestKey(r.FullName, n)]; got != 0 {
						t.Errorf("request %d read %d times, want none after the invalid entry", n, got)
					}
				}
			})
		})
	}
}
