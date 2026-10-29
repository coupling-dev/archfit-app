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
	mu   sync.Mutex
	list []api.Comment
	next int64
}

func (f *fakeComments) AppComments(context.Context, api.RepoRef, int) ([]api.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.Comment(nil), f.list...), nil
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

func newRequests() (*Requests, *fakeComments, store.Store) {
	s, c := store.NewMemory(), &fakeComments{}
	return New(s, c), c, s
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
		if err := r.Answer(ctx, target(7), fmt.Sprintf("%032x", n), hex64, decision.StatusDeclined, "why"); err != nil {
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
