package feedback

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	reportDigest = "4444444444444444444444444444444444444444444444444444444444444444"
)

// TestMain keeps publication logs out of the test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

var t0 = time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

// clock is a settable fixed clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type harness struct {
	checks   *fakeChecks
	comments *fakeComments
	pulls    *fakePulls
	clock    *clock
	store    store.Store
	pub      Publisher4
}

// appURL is the App base URL the harness publisher links to.
const appURL = "https://app.example"

func newHarness(prHead string) *harness {
	h := &harness{
		checks:   &fakeChecks{},
		comments: &fakeComments{},
		pulls:    &fakePulls{headSHA: prHead},
		clock:    &clock{t: t0},
		store:    store.NewMemory(),
	}
	h.pub = New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now)
	return h
}

func prTarget(head string) inputs.Target {
	return inputs.Target{
		Repo:        api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/shop"}, ID: 42, DefaultBranch: "main"},
		PullRequest: 7,
		HeadSHA:     head,
		BaseRef:     "main",
	}
}

func pushTarget(head string) inputs.Target {
	t := prTarget(head)
	t.PullRequest = 0
	return t
}

func groupTarget(head string) inputs.Target {
	t := pushTarget(head)
	t.MergeGroup = true
	return t
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body lacks %q", w)
		}
	}
}

var update = flag.Bool("update", false, "rewrite the golden files")

func (h *harness) onContention(f func()) {
	h.pub.(*serialized).wait = func(ctx context.Context) error { f(); return ctx.Err() }
}
