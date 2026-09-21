package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

var (
	t0      = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: t0} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeAccount records the façade's calls as "observe:7:true", "refresh:7", "forget:7", "grant:7".
type fakeAccount struct {
	calls     []string
	times     []time.Time
	grantTo   time.Time
	observeEr map[int64]error
	forgetErr map[int64]error
}

func (f *fakeAccount) Observe(_ context.Context, id int64, hasPrivate bool, now time.Time) error {
	f.calls = append(f.calls, fmt.Sprintf("observe:%d:%t", id, hasPrivate))
	f.times = append(f.times, now)
	return f.observeEr[id]
}

func (f *fakeAccount) Status(context.Context, int64, time.Time) (account.Status, error) {
	return account.Status{}, nil
}

func (f *fakeAccount) Grant(_ context.Context, id int64, until, now time.Time) error {
	f.calls = append(f.calls, fmt.Sprintf("grant:%d", id))
	f.times = append(f.times, now)
	f.grantTo = until
	return nil
}

func (f *fakeAccount) Refresh(_ context.Context, id int64, now time.Time) error {
	f.calls = append(f.calls, fmt.Sprintf("refresh:%d", id))
	f.times = append(f.times, now)
	return nil
}

func (f *fakeAccount) Forget(_ context.Context, id int64) error {
	f.calls = append(f.calls, fmt.Sprintf("forget:%d", id))
	return f.forgetErr[id]
}

// fakeRepos is the api.Repos role; only the two list methods are used by the façade.
type fakeRepos struct {
	api.Repos
	installations []api.Installation
	installErr    error
	repos         map[int64][]api.Repo
	reposErr      map[int64]error
	calls         []string
	deadlines     []bool
}

func (f *fakeRepos) ListInstallations(context.Context) ([]api.Installation, error) {
	f.calls = append(f.calls, "installations")
	return f.installations, f.installErr
}

func (f *fakeRepos) ListRepos(ctx context.Context, id int64) ([]api.Repo, error) {
	f.calls = append(f.calls, fmt.Sprintf("repos:%d", id))
	_, ok := ctx.Deadline()
	f.deadlines = append(f.deadlines, ok)
	return f.repos[id], f.reposErr[id]
}

type fakeTenants struct {
	tenants []store.Tenant
	err     error
	calls   int
}

func (f *fakeTenants) Tenants(context.Context) ([]store.Tenant, error) {
	f.calls++
	return f.tenants, f.err
}

func installations(ids ...int64) []api.Installation {
	out := make([]api.Installation, len(ids))
	for i, id := range ids {
		out[i] = api.Installation{ID: id, Account: fmt.Sprintf("org%d", id)}
	}
	return out
}

func repo(id int64, fullName string, private bool) api.Repo {
	return api.Repo{
		RepoRef: api.RepoRef{InstallationID: id, FullName: fullName},
		ID:      id * 100, Private: private, DefaultBranch: "main",
	}
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
