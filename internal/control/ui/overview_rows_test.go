package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// rowStates answers a row's state; the slow row waits until released.
type rowStates struct {
	mu      sync.Mutex
	calls   map[string]int
	slow    string
	release chan struct{}
}

func (r *rowStates) RepoState(_ context.Context, repo api.Repo) (string, error) {
	r.mu.Lock()
	r.calls[repo.FullName]++
	r.mu.Unlock()
	if repo.FullName == r.slow {
		<-r.release
	}
	return "approved", nil
}

// rowSetups counts the Setup probes per row and reports each one as it starts.
type rowSetups struct {
	mu      sync.Mutex
	calls   map[string]int
	started chan string
}

func (s *rowSetups) Setup(_ context.Context, repo api.Repo) (onboarding.Setup, error) {
	s.mu.Lock()
	s.calls[repo.FullName]++
	s.mu.Unlock()
	s.started <- repo.FullName
	return onboarding.Setup{Policy: true, Workflow: true, State: "approved"}, nil
}

func rowServer(e *env, states *rowStates, setups *rowSetups) *server {
	return &server{gh: github{users: e.gh}, admissions: e.admit, repoStates: states, setups: setups}
}

// A slow row does not hold the others: the fast row's probe starts while the slow row's state read is still waiting.
func TestASlowRowDoesNotHoldTheOthers(t *testing.T) {
	e := newEnv(t)
	e.addRepo("acme/slow", installA, 30)
	e.addRepo("acme/fast", installA, 31)
	e.grant("alice", "acme/slow", "read")
	e.grant("alice", "acme/fast", "read")
	states := &rowStates{calls: map[string]int{}, slow: "acme/slow", release: make(chan struct{})}
	setups := &rowSetups{calls: map[string]int{}, started: make(chan string, 8)}
	s := rowServer(e, states, setups)
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil).WithContext(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := s.apiRepos(req, scope{user: session.User{Token: e.user("alice").AccessToken}})
		done <- err
	}()
	select {
	case name := <-setups.started:
		if name != "acme/fast" {
			t.Fatalf("first probe was %q, want the fast row", name)
		}
	case <-time.After(5 * time.Second):
		close(states.release)
		t.Fatal("the fast row waited for the slow row")
	}
	close(states.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// One state read and one Setup probe per admitted row, none repeated.
func TestEachRowIsReadOnceOnTheProbePath(t *testing.T) {
	e := newEnv(t)
	names := []string{"acme/a", "acme/b", "acme/c"}
	for i, n := range names {
		e.addRepo(n, installA, int64(40+i))
		e.grant("alice", n, "read")
	}
	states := &rowStates{calls: map[string]int{}, release: make(chan struct{})}
	close(states.release)
	setups := &rowSetups{calls: map[string]int{}, started: make(chan string, 8)}
	s := rowServer(e, states, setups)
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)

	if _, err := s.apiRepos(req, scope{user: session.User{Token: e.user("alice").AccessToken}}); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if states.calls[n] != 1 || setups.calls[n] != 1 {
			t.Errorf("%s: state reads %d, probes %d; want 1 and 1", n, states.calls[n], setups.calls[n])
		}
	}
}

// getCounts counts store reads per key.
type getCounts struct {
	store.Store
	mu    sync.Mutex
	count map[string]int
}

func (c *getCounts) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	c.mu.Lock()
	c.count[key]++
	c.mu.Unlock()
	return c.Store.Get(ctx, t, key)
}

// Five rows of one installation read its admission once and each row's record once, not once per row and step.
func TestTheListReadsAdmissionAndRecordsOncePerRequest(t *testing.T) {
	e := newEnv(t)
	reads := &getCounts{Store: e.store, count: map[string]int{}}
	admissions := gate.NewAdmissions(reads, nil)
	if err := e.store.Put(t.Context(), store.Tenant(installC), inputs.AdmissionKey, []byte(`{"account":"octo","by":"op","by_id":1}`)); err != nil {
		t.Fatal(err)
	}
	names := []string{"octo/a", "octo/b", "octo/c", "octo/d", "octo/e"}
	for i, n := range names {
		e.addRepo(n, installC, int64(60+i))
		e.grant("alice", n, "read")
	}
	setups := &rowSetups{calls: map[string]int{}, started: make(chan string, 8)}
	s := &server{gh: github{users: e.gh}, admissions: admissions, repoStates: gate.NewRepoStates(reads, admissions), setups: setups}
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)

	if _, err := s.apiRepos(req, scope{user: session.User{Token: e.user("alice").AccessToken}}); err != nil {
		t.Fatal(err)
	}
	if got := reads.count[inputs.AdmissionKey]; got != 1 {
		t.Errorf("admission reads = %d, want 1", got)
	}
	for _, n := range names {
		if got := reads.count[records.RepoKey(n)]; got != 1 {
			t.Errorf("%s: record reads = %d, want 1", n, got)
		}
	}
}
