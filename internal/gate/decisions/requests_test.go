package decisions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

const (
	hex32 = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	hex40 = "1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
	hex64 = "5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b"
)

var t0 = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

// fakeComments is the App's comments on pull requests, in one list.
type fakeComments struct {
	mu        sync.Mutex
	list      []api.Comment
	next      int64
	updateErr error
	// landsThenErr applies the next edit and still returns this error, as a timed-out request that GitHub applied.
	landsThenErr error
	// meet makes the first n listings wait, after they took their snapshot, until n listings have arrived or a
	// timeout passes: two writers that are not serialized both see the thread as it was before either wrote.
	meet    int
	arrived int
	changed *sync.Cond
}

func (f *fakeComments) AppComments(context.Context, api.RepoRef, int) ([]api.Comment, error) {
	f.mu.Lock()
	snapshot := append([]api.Comment(nil), f.list...)
	if f.meet > 0 && f.arrived < f.meet {
		f.arrived++
		timedOut := false
		time.AfterFunc(300*time.Millisecond, func() {
			f.mu.Lock()
			timedOut = true
			f.changed.Broadcast()
			f.mu.Unlock()
		})
		for f.arrived < f.meet && !timedOut {
			f.changed.Wait()
		}
		f.changed.Broadcast()
	}
	f.mu.Unlock()
	return snapshot, nil
}

func (f *fakeComments) CreateComment(_ context.Context, _ api.RepoRef, _ int, body string) (api.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	c := api.Comment{ID: f.next, Body: body}
	f.list = append(f.list, c)
	return c, nil
}

func (f *fakeComments) UpdateComment(_ context.Context, _ api.RepoRef, id int64, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	if err := f.landsThenErr; err != nil {
		f.landsThenErr = nil
		for i := range f.list {
			if f.list[i].ID == id {
				f.list[i].Body = body
			}
		}
		return err
	}
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].Body = body
			return nil
		}
	}
	return api.ErrNotFound
}

func target(pull int) inputs.Target {
	return inputs.Target{Repo: api.Repo{RepoRef: api.RepoRef{InstallationID: 11, FullName: "Acme/Shop"}}, PullRequest: pull, HeadSHA: hex40}
}

func filing(finding string) Filing {
	return Filing{Requester: "agent", CommentID: 5, Request: decision.Request{
		SchemaVersion: decision.SchemaV1, FindingID: finding, RuleID: "r", Edge: decision.Edge{From: "a", To: "b"},
		HeadSHA: hex40, PolicyDigest: hex64, Why: "why", Proposal: decision.Proposal{Kind: decision.ProposalPolicyPatch, Policy: "version: 2\n"},
	}}
}

// fakePulls answers every pull request with one state.
type fakePulls struct {
	api.Pulls
	pull api.Pull
	// after, when set, answers every read but the first: the pull request has moved on since a walk first read it.
	after *api.Pull
	reads int
}

func (f *fakePulls) Pull(context.Context, api.RepoRef, int) (api.Pull, error) {
	f.reads++
	if f.after != nil && f.reads > 1 {
		return *f.after, nil
	}
	return f.pull, nil
}

func newRequests() (*Requests, *fakeComments, store.Store) {
	r, c, _, s := newRequestsWithPulls()
	return r, c, s
}

func newRequestsWithPulls() (*Requests, *fakeComments, *fakePulls, store.Store) {
	s, c, p := store.NewMemory(), &fakeComments{}, &fakePulls{pull: api.Pull{State: "open", HeadSHA: hex40}}
	c.changed = sync.NewCond(&c.mu)
	return New(s, c, p, func() time.Time { return t0 }), c, p, s
}

func TestFileConcurrentlyKeepsOneRecordAndOneIndexEntry(t *testing.T) {
	r, c, s := newRequests()
	var wg sync.WaitGroup
	outcomes := make(chan Outcome, 20)
	for range 20 {
		wg.Go(func() {
			o, err := r.File(context.Background(), target(7), filing(hex32), t0)
			if err != nil {
				t.Error(err)
			}
			outcomes <- o
		})
	}
	wg.Wait()
	close(outcomes)
	filed := 0
	for o := range outcomes {
		if o == Filed {
			filed++
		}
	}
	if filed != 1 {
		t.Errorf("%d goroutines filed the record, want 1", filed)
	}
	raw, err := s.Get(context.Background(), 11, "decision/acme/shop/pending")
	if err != nil || strings.Count(string(raw), hex32) != 1 {
		t.Errorf("pending list = %s, %v", raw, err)
	}
	if len(c.list) != 1 {
		t.Errorf("%d status comments, want 1", len(c.list))
	}
}

func TestFileRefusesARecordTooLargeForTheStore(t *testing.T) {
	r, c, s := newRequests()
	f := filing(hex32)
	f.Request.Why = strings.Repeat("w", 70<<10)
	if _, err := r.File(context.Background(), target(7), f, t0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("File = %v, want ErrTooLarge", err)
	}
	if _, err := s.Get(context.Background(), 11, "decision/acme/shop/pending"); !errors.Is(err, store.ErrNotFound) || len(c.list) != 0 {
		t.Errorf("a refused request left state behind (%v, %d comments)", err, len(c.list))
	}
}

func TestSettleMovesOnlyPendingRecords(t *testing.T) {
	r, _, _ := newRequests()
	ctx := context.Background()
	if err := r.Settle(ctx, target(7), hex32, hex64, decision.StatusDeclined, "", 0, t0); !errors.Is(err, ErrNoRecord) {
		t.Errorf("Settle of a missing request = %v", err)
	}
	if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Settle(ctx, target(7), hex32, hex64, decision.StatusApproved, "", 12, t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Settle(ctx, target(7), hex32, hex64, decision.StatusStale, decision.ReasonHeadMoved, 0, t0); !errors.Is(err, ErrNotPending) {
		t.Errorf("Settle of an approved request = %v", err)
	}
	rec, err := r.Get(ctx, target(7), hex32, hex64)
	if err != nil || rec.Status != decision.StatusApproved || rec.ProposalPR != 12 {
		t.Errorf("record = %+v, %v", rec, err)
	}
	// An approved request is not pending: it frees its place, and the stale rules skip it.
	if err := r.Closed(ctx, target(7), t0); err != nil {
		t.Errorf("Closed = %v", err)
	}
	if rec, _ := r.Get(ctx, target(7), hex32, hex64); rec.Status != decision.StatusApproved {
		t.Errorf("a closed pull request staled an approved request: %+v", rec)
	}
}

func TestStatusCommentsOfOnePullRequestAreCapped(t *testing.T) {
	r, c, _ := newRequests()
	ctx := context.Background()
	for n := range maxStatusComments + 5 {
		if _, err := r.Answer(ctx, target(7), fmt.Sprintf("%032x", n), hex64, decision.StatusDeclined, "why"); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.list) != maxStatusComments {
		t.Errorf("%d status comments, want %d", len(c.list), maxStatusComments)
	}
}

func TestUnreadableRecordsFailClosed(t *testing.T) {
	r, c, s := newRequests()
	ctx := context.Background()
	if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, 11, "decision/acme/shop/pending", []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	before := len(c.list)
	if _, err := r.File(ctx, target(7), filing("0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"), t0); err == nil {
		t.Error("File went on over an unreadable pending list")
	}
	if err := r.Closed(ctx, target(7), t0); err == nil {
		t.Error("Closed went on over an unreadable pending list")
	}
	if len(c.list) != before {
		t.Error("a comment was written over unreadable state")
	}
}

// bodyOf returns the text of the only status comment.
func bodyOf(t *testing.T, c *fakeComments) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) != 1 {
		t.Fatalf("%d status comments, want 1: %+v", len(c.list), c.list)
	}
	return c.list[0].Body
}

func statusLine(body string) string { return strings.Split(body, "\n")[1] }

func TestAnswerRacingFileLeavesThePendingCommentAlone(t *testing.T) {
	r, c, _ := newRequests()
	c.meet = 2
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := r.Answer(ctx, target(7), hex32, hex64, decision.StatusDeclined, "why: bad"); err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if got := statusLine(bodyOf(t, c)); got != "pending" {
		t.Errorf("status line = %q, want pending: an answer without a record overwrote a stored request", got)
	}
}

func TestStatusWritersOfOneRequestCreateOneComment(t *testing.T) {
	r, c, _ := newRequests()
	c.meet = 2
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	bodyOf(t, c)
}

func TestStatusCommentCapHoldsUnderConcurrency(t *testing.T) {
	r, c, _ := newRequests()
	var wg sync.WaitGroup
	for n := range maxStatusComments + 20 {
		wg.Go(func() {
			if _, err := r.Answer(context.Background(), target(7), fmt.Sprintf("%032x", n), hex64, decision.StatusDeclined, "why"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(c.list) != maxStatusComments {
		t.Errorf("%d status comments, want exactly %d", len(c.list), maxStatusComments)
	}
}

func TestSettledRequestStaysListedUntilItsCommentIsWritten(t *testing.T) {
	r, c, p, s := newRequestsWithPulls()
	ctx := context.Background()
	if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
		t.Fatal(err)
	}
	p.pull.HeadSHA = strings.Repeat("e", 40)
	c.updateErr = api.ErrRateLimited
	if err := r.HeadMoved(ctx, target(7), t0); !errors.Is(err, api.ErrRateLimited) {
		t.Fatalf("HeadMoved = %v, want the failed write", err)
	}
	rec, _ := r.Get(ctx, target(7), hex32, hex64)
	if rec.Status != decision.StatusStale || rec.Published != "pending" {
		t.Fatalf("record = %+v: stored status stale, published still pending", rec)
	}
	if got := statusLine(bodyOf(t, c)); got != "pending" {
		t.Fatalf("thread says %q", got)
	}
	raw, _ := s.Get(ctx, 11, "decision/acme/shop/pending")
	if !strings.Contains(string(raw), hex32) {
		t.Fatalf("the request left the list before its comment was written: %s", raw)
	}

	c.updateErr = nil
	if err := r.HeadMoved(ctx, target(7), t0); err != nil { // any later walk repairs it
		t.Fatal(err)
	}
	if got := statusLine(bodyOf(t, c)); got != "stale (head moved)" {
		t.Errorf("thread says %q after the repair", got)
	}
	raw, _ = s.Get(ctx, 11, "decision/acme/shop/pending")
	if strings.Contains(string(raw), hex32) {
		t.Errorf("a published request is still listed: %s", raw)
	}
}

func TestCapsCountOnlyPendingRequests(t *testing.T) {
	r, c, p, _ := newRequestsWithPulls()
	ctx := context.Background()
	for n := 1; n <= MaxPendingPerPull; n++ {
		if _, err := r.File(ctx, target(7), filing(fmt.Sprintf("%032x", n)), t0); err != nil {
			t.Fatal(err)
		}
	}
	p.pull.HeadSHA = strings.Repeat("e", 40)
	c.updateErr = api.ErrRateLimited
	_ = r.HeadMoved(ctx, target(7), t0) // all stale, none published
	c.updateErr = nil
	if o, err := r.File(ctx, target(7), filing(fmt.Sprintf("%032x", 99)), t0); err != nil || o != Filed {
		t.Errorf("File = %v, %v: unpublished stale requests must not count toward the cap", o, err)
	}
}

func TestSettleAtRefusesAChangedHead(t *testing.T) {
	r, _, _ := newRequests()
	ctx := context.Background()
	if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
		t.Fatal(err)
	}
	err := r.SettleAt(ctx, target(7), hex32, hex64, strings.Repeat("e", 40), decision.StatusStale, decision.ReasonHeadMoved, 0, t0)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("SettleAt = %v, want ErrChanged", err)
	}
	if rec, _ := r.Get(ctx, target(7), hex32, hex64); rec.Status != decision.StatusPending {
		t.Errorf("record = %+v", rec)
	}
}

func TestHeadMovedAndClosedReadTheLivePullRequest(t *testing.T) {
	tests := []struct {
		name string
		live api.Pull
		act  func(*Requests, context.Context) error
		want decision.Status
	}{
		{"head moved: live head differs", api.Pull{State: "open", HeadSHA: strings.Repeat("e", 40)}, func(r *Requests, ctx context.Context) error { return r.HeadMoved(ctx, target(7), t0) }, decision.StatusStale},
		{"head moved: live head is the request's", api.Pull{State: "open", HeadSHA: hex40}, func(r *Requests, ctx context.Context) error { return r.HeadMoved(ctx, target(7), t0) }, decision.StatusPending},
		{"head moved: pull request closed", api.Pull{State: "closed", HeadSHA: strings.Repeat("e", 40)}, func(r *Requests, ctx context.Context) error { return r.HeadMoved(ctx, target(7), t0) }, decision.StatusPending},
		{"closed: pull request closed", api.Pull{State: "closed", HeadSHA: hex40}, func(r *Requests, ctx context.Context) error { return r.Closed(ctx, target(7), t0) }, decision.StatusStale},
		{"closed: reopened", api.Pull{State: "open", HeadSHA: hex40}, func(r *Requests, ctx context.Context) error { return r.Closed(ctx, target(7), t0) }, decision.StatusPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, p, _ := newRequestsWithPulls()
			ctx := context.Background()
			if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
				t.Fatal(err)
			}
			p.pull = tt.live
			if err := tt.act(r, ctx); err != nil {
				t.Fatal(err)
			}
			if rec, _ := r.Get(ctx, target(7), hex32, hex64); rec.Status != tt.want {
				t.Errorf("status = %s, want %s", rec.Status, tt.want)
			}
		})
	}
}

func TestRepositoryCapCountsOnlyPendingRequests(t *testing.T) {
	r, c, p, _ := newRequestsWithPulls()
	ctx := context.Background()
	for pr := 1; pr <= MaxPendingPerRepo/MaxPendingPerPull; pr++ {
		for n := 1; n <= MaxPendingPerPull; n++ {
			if _, err := r.File(ctx, target(pr), filing(fmt.Sprintf("%032x", n)), t0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if o, _ := r.File(ctx, target(99), filing(hex32), t0); o != Capped {
		t.Fatalf("File at the repository cap = %v", o)
	}
	p.pull.State = "closed"
	c.updateErr = api.ErrRateLimited
	_ = r.Closed(ctx, target(1), t0) // 20 stale, none published
	c.updateErr = nil
	if o, err := r.File(ctx, target(99), filing(hex32), t0); err != nil || o != Filed {
		t.Errorf("File = %v, %v: unpublished stale requests must not count toward the repository cap", o, err)
	}
}

// losingStore applies the first n compare-and-swaps of several keys after arm, then loses the rest without writing.
type losingStore struct {
	store.Store
	armed bool
	calls int
	apply int
}

func (l *losingStore) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	if l.armed {
		l.calls++
		if l.calls > l.apply {
			return false, nil
		}
	}
	return l.Store.CompareAndSwapAll(ctx, t, swaps)
}

func refiling(head string) Filing {
	f := filing(hex32)
	f.Request.HeadSHA = head
	return f
}

func TestRefileDoesNotTrustAPublishedLineOlderThanTheThread(t *testing.T) {
	newHead := strings.Repeat("e", 40)
	tests := []struct {
		name string
		// strand makes the stale comment reach the thread without Published following.
		strand func(c *fakeComments, l *losingStore)
	}{
		{"the edit lands but returns an unknown outcome", func(c *fakeComments, l *losingStore) { c.landsThenErr = errors.New("timeout") }},
		{"the record write loses to a concurrent writer", func(c *fakeComments, l *losingStore) { l.armed, l.apply = true, 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := t0
			ls := &losingStore{Store: store.NewMemory()}
			c, p := &fakeComments{}, &fakePulls{pull: api.Pull{State: "open", HeadSHA: hex40}}
			c.changed = sync.NewCond(&c.mu)
			r := New(ls, c, p, func() time.Time { return clock })
			ctx := context.Background()
			if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
				t.Fatal(err)
			}
			p.pull.HeadSHA = newHead
			tt.strand(c, ls)
			if err := r.HeadMoved(ctx, target(7), t0); err == nil {
				t.Fatal("HeadMoved reported no failure")
			}
			if got := statusLine(bodyOf(t, c)); got != "stale (head moved)" {
				t.Fatalf("setup: thread says %q", got)
			}
			ls.armed = false
			clock = clock.Add(2 * time.Minute) // an unknown outcome keeps the lease for a minute

			if _, err := r.File(ctx, target(7), refiling(newHead), t0); err != nil {
				t.Fatal(err)
			}
			if got := statusLine(bodyOf(t, c)); got != "pending" {
				t.Errorf("thread says %q for a live pending request", got)
			}
		})
	}
}

func TestAnswerWritesOverAStaleRequestAndNeverOverALiveOne(t *testing.T) {
	tests := []struct {
		name     string
		status   decision.Status
		reason   string
		write    bool
		wantLine string
	}{
		{"pending", decision.StatusPending, "", false, "pending"},
		{"declined", decision.StatusDeclined, "", false, "declined"},
		{"approved", decision.StatusApproved, "", false, "approved (proposal PR #3)"},
		{"stale", decision.StatusStale, decision.ReasonHeadMoved, true, "declined (why: bad)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, c, _ := newRequests()
			ctx := context.Background()
			if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
				t.Fatal(err)
			}
			if tt.status != decision.StatusPending {
				if err := r.Settle(ctx, target(7), hex32, hex64, tt.status, tt.reason, 3, t0); err != nil {
					t.Fatal(err)
				}
			}
			written, err := r.Answer(ctx, target(7), hex32, hex64, decision.StatusDeclined, "why: bad")
			if err != nil || written != tt.write {
				t.Fatalf("Answer = %v, %v, want written %v", written, err, tt.write)
			}
			if got := statusLine(bodyOf(t, c)); got != tt.wantLine {
				t.Errorf("thread says %q, want %q", got, tt.wantLine)
			}
			rec, _ := r.Get(ctx, target(7), hex32, hex64)
			if tt.write && (rec.Status != decision.StatusStale || rec.Published != tt.wantLine) {
				t.Errorf("record = %+v: stale, and Published naming the line written", rec)
			}
			// A later walk must not put the stale line back over the answer.
			if err := r.Closed(ctx, target(7), t0); err != nil {
				t.Fatal(err)
			}
			if got := statusLine(bodyOf(t, c)); got != tt.wantLine {
				t.Errorf("after a walk the thread says %q, want %q", got, tt.wantLine)
			}
		})
	}
}

// The sweep lists the pull requests once and reaches this one later: its head is the live head only if the pull request
// is read again, never the head the listing held.
func TestRevalidateComparesTheLiveHeadNotTheListedOne(t *testing.T) {
	accepting := inputs.Trusted{PolicyDigests: []string{hex64}}
	tests := []struct {
		name       string
		live       api.Pull
		listedHead string
		want       decision.Status
	}{
		{"a push the sweep has not seen", api.Pull{State: "open", HeadSHA: strings.Repeat("e", 40)}, hex40, decision.StatusStale},
		{"a request filed after the listing", api.Pull{State: "open", HeadSHA: hex40}, strings.Repeat("d", 40), decision.StatusPending},
		{"the listing and the request agree", api.Pull{State: "open", HeadSHA: hex40}, hex40, decision.StatusPending},
		{"a closed pull request is the close rule's", api.Pull{State: "closed", HeadSHA: strings.Repeat("e", 40)}, hex40, decision.StatusPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, c, p, _ := newRequestsWithPulls()
			ctx := context.Background()
			if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
				t.Fatal(err)
			}
			p.pull = tt.live
			tg := target(7)
			tg.HeadSHA = tt.listedHead
			if err := r.Revalidate(ctx, tg, accepting, t0); err != nil {
				t.Fatal(err)
			}
			rec, _ := r.Get(ctx, target(7), hex32, hex64)
			if rec.Status != tt.want {
				t.Errorf("status = %s, want %s", rec.Status, tt.want)
			}
			if tt.want == decision.StatusStale && statusLine(bodyOf(t, c)) != "stale (head moved)" {
				t.Errorf("thread says %q", statusLine(bodyOf(t, c)))
			}
		})
	}
}

// A walk reads the live pull request once and loads the records one by one. A request filed for the new head while the
// walk is under way looks stale against the first read: it is confirmed against the pull request as it is now.
func TestAStaleVerdictIsConfirmedAgainstTheLivePullRequest(t *testing.T) {
	newHead := strings.Repeat("e", 40)
	accepting := inputs.Trusted{PolicyDigests: []string{hex64}}
	for _, tt := range []struct {
		name string
		act  func(*Requests, context.Context) error
	}{
		{"head moved", func(r *Requests, ctx context.Context) error { return r.HeadMoved(ctx, target(7), t0) }},
		{"sweep", func(r *Requests, ctx context.Context) error { return r.Revalidate(ctx, target(7), accepting, t0) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, _, p, _ := newRequestsWithPulls()
			ctx := context.Background()
			if _, err := r.File(ctx, target(7), filing(hex32), t0); err != nil {
				t.Fatal(err)
			}
			other := strings.Repeat("c", 32)
			f := filing(other)
			f.Request.HeadSHA = newHead
			if _, err := r.File(ctx, target(7), f, t0); err != nil {
				t.Fatal(err)
			}
			// The walk's first read still sees the first head; the pull request is at the new head by the next read.
			p.after = &api.Pull{State: "open", HeadSHA: newHead}
			if err := tt.act(r, ctx); err != nil {
				t.Fatal(err)
			}
			if rec, _ := r.Get(ctx, target(7), other, hex64); rec.Status != decision.StatusPending {
				t.Errorf("the request for the live head is %s, want pending", rec.Status)
			}
		})
	}
}
