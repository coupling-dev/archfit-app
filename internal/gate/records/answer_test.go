package records_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

const requestKey = "graph a b"

// seedRequest stores request 431 open, with its request index entry.
func seedRequest(t *testing.T, s store.Store) {
	t.Helper()
	if err := openRequest(context.Background(), s, 431, requestKey); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, s store.Store, keys ...string) [][]byte {
	t.Helper()
	out := make([][]byte, len(keys))
	for i, k := range keys {
		v, err := s.Get(context.Background(), tenant, k)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

func TestAnswerRecordsTheApprovalTheRequestAndANewProposalInOneSwap(t *testing.T) {
	s := store.NewMemory()
	seedRequest(t, s)
	pin := &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h1", Digest: "d1"}
	if err := records.Answer(context.Background(), s, tenant, repoName, "a1", *approval(""), pin); err != nil {
		t.Fatal(err)
	}
	a := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1"))
	req := load[records.Request](t, s, records.RequestKey(repoName, 431))
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if a.State != records.StateOpen || a.Proposal != "p1" || req.State != records.StateAnswered {
		t.Errorf("approval %+v, request state %q", a, req.State)
	}
	if !slices.Equal(p.Approvals, []string{"a1"}) || p.PullRequest != 12 || p.HeadSHA != "h1" || p.ExpectedDigest != "d1" ||
		p.State != records.StateOpen {
		t.Errorf("proposal = %+v", p)
	}
	// Until its proposal is consumed the answered request covers its key: a failing pull request waits on it.
	if x := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex); !slices.Equal(x.Entries,
		[]records.Entry{{Key: requestKey, ID: "431"}}) {
		t.Errorf("request index = %v, want the answered request kept", x.Entries)
	}
	if id, ok := (&records.Index{Entries: loadIndex(t, s, records.ProposalIndexKey(repoName), records.SchemaProposalIndex).Entries}).Lookup("12"); !ok || id != "p1" {
		t.Errorf("proposal index entry of #12 = %q, %v", id, ok)
	}
}

func TestAnswerJoinsAnOpenProposalAndPinsIt(t *testing.T) {
	s := store.NewMemory()
	seedRequest(t, s)
	openProposal(t, s, "p1", 12, "d0")
	pin := &records.Pin{Proposal: "p1", HeadSHA: "h2", Digest: "d2"}
	if err := records.Answer(context.Background(), s, tenant, repoName, "a1", *approval(""), pin); err != nil {
		t.Fatal(err)
	}
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if !slices.Equal(p.Approvals, []string{"a1"}) || p.HeadSHA != "h2" || p.ExpectedDigest != "d2" || p.PullRequest != 12 {
		t.Errorf("proposal = %+v", p)
	}
}

func TestAnswerWithoutAProposal(t *testing.T) {
	s := store.NewMemory()
	seedRequest(t, s)
	if err := records.Answer(context.Background(), s, tenant, repoName, "a1", *approval(""), nil); err != nil {
		t.Fatal(err)
	}
	a := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1"))
	if a.Proposal != "" || a.State != records.StateOpen {
		t.Errorf("approval = %+v", a)
	}
	if req := load[records.Request](t, s, records.RequestKey(repoName, 431)); req.State != records.StateAnswered {
		t.Errorf("request state = %q", req.State)
	}
	if x := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex); len(x.Entries) != 0 {
		t.Errorf("an answer that joins no proposal left its request in the index: %v", x.Entries)
	}
}

func TestAnswerRefusesAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	full := func(s store.Store) {
		p := &records.Proposal{PullRequest: 12, ExpectedDigest: "d", State: records.StateOpen}
		for i := range records.MaxApprovals {
			p.Approvals = append(p.Approvals, "x"+strconv.Itoa(i))
		}
		put(t, s, records.ProposalKey(repoName, "p1"), p)
	}
	tests := []struct {
		name  string
		seed  func(s store.Store)
		id    string
		pin   *records.Pin
		want  error
		plain bool // an error of the arguments, not a sentinel
	}{
		{"an answered request", func(s store.Store) {
			put(t, s, records.RequestKey(repoName, 431), &records.Request{Key: requestKey, State: records.StateAnswered})
		}, "a1", nil, records.ErrAnswered, false},
		{"no request", func(store.Store) {}, "a1", nil, records.ErrNoRecord, false},
		{"an approval id in use", func(s store.Store) {
			seedRequest(t, s)
			put(t, s, records.ApprovalKey(repoName, "a1"), approval(records.StateOpen))
		}, "a1", nil, records.ErrExists, false},
		{"a merged proposal", func(s store.Store) {
			seedRequest(t, s)
			put(t, s, records.ProposalKey(repoName, "p1"), &records.Proposal{PullRequest: 12, ExpectedDigest: "d", State: records.StateMerged})
		}, "a1", &records.Pin{Proposal: "p1", HeadSHA: "h", Digest: "d"}, records.ErrMoved, false},
		{"a full proposal", func(s store.Store) { seedRequest(t, s); full(s) },
			"a1", &records.Pin{Proposal: "p1", HeadSHA: "h", Digest: "d"}, records.ErrFull, false},
		{"a new proposal without a pull request", func(s store.Store) { seedRequest(t, s) },
			"a1", &records.Pin{Proposal: "p1", HeadSHA: "h", Digest: "d"}, records.ErrNoRecord, false},
		{"a pull request another proposal holds", func(s store.Store) { seedRequest(t, s); openProposal(t, s, "p0", 12, "d") },
			"a1", &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h", Digest: "d"}, records.ErrExists, false},
		{"an empty digest", func(s store.Store) { seedRequest(t, s) },
			"a1", &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h"}, nil, true},
	}
	keys := []string{records.ApprovalKey(repoName, "a1"), records.RequestKey(repoName, 431), records.RequestIndexKey(repoName),
		records.ProposalKey(repoName, "p1"), records.ProposalIndexKey(repoName)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			tt.seed(s)
			before := snapshot(t, s, keys...)
			err := records.Answer(ctx, s, tenant, repoName, tt.id, *approval(""), tt.pin)
			if tt.plain && err == nil || !tt.plain && !errors.Is(err, tt.want) {
				t.Fatalf("Answer = %v, want %v", err, tt.want)
			}
			if after := snapshot(t, s, keys...); !slices.EqualFunc(before, after, bytes.Equal) {
				t.Error("a refused answer wrote a key")
			}
		})
	}
}

func TestVoidReopensTheRequest(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	seedRequest(t, s)
	if err := records.Answer(ctx, s, tenant, repoName, "a1", *approval(""), &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h", Digest: "d"}); err != nil {
		t.Fatal(err)
	}
	request, reopened, err := records.Void(ctx, s, tenant, repoName, "a1")
	if err != nil || request != 431 || !reopened {
		t.Fatalf("Void = %d, %v, %v", request, reopened, err)
	}
	if a := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1")); a.State != records.StateVoid || a.Proposal != "p1" {
		t.Errorf("approval = %+v", a)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); len(p.Approvals) != 0 || p.State != records.StateOpen {
		t.Errorf("proposal = %+v; a void approval leaves its list and frees its place", p)
	}
	if req := load[records.Request](t, s, records.RequestKey(repoName, 431)); req.State != records.StateOpen {
		t.Errorf("request state = %q, want open", req.State)
	}
	x := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex)
	if id, ok := x.Lookup(requestKey); !ok || id != "431" {
		t.Errorf("request index = %v", x.Entries)
	}

	again := snapshot(t, s, records.ApprovalKey(repoName, "a1"), records.RequestKey(repoName, 431))
	if _, reopened, err := records.Void(ctx, s, tenant, repoName, "a1"); err != nil || reopened {
		t.Errorf("Void again = %v, %v", reopened, err)
	}
	if after := snapshot(t, s, records.ApprovalKey(repoName, "a1"), records.RequestKey(repoName, 431)); !slices.EqualFunc(again, after, bytes.Equal) {
		t.Error("a second Void changed a key")
	}
}

func TestVoidLeavesTheRequestAnsweredWhenAnotherHoldsItsKey(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	seedRequest(t, s)
	if err := records.Answer(ctx, s, tenant, repoName, "a1", *approval(""), nil); err != nil {
		t.Fatal(err)
	}
	if err := openRequest(ctx, s, 500, requestKey); err != nil {
		t.Fatal(err)
	}
	if _, reopened, err := records.Void(ctx, s, tenant, repoName, "a1"); err != nil || reopened {
		t.Fatalf("Void = %v, %v; want not reopened", reopened, err)
	}
	if req := load[records.Request](t, s, records.RequestKey(repoName, 431)); req.State != records.StateAnswered {
		t.Errorf("request state = %q, want answered", req.State)
	}
	if a := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1")); a.State != records.StateVoid {
		t.Errorf("approval state = %q", a.State)
	}
}

func TestVoidProposal(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	openProposal(t, s, "p1", 12, "d")
	if err := records.VoidProposal(ctx, s, tenant, repoName, "p1"); err != nil {
		t.Fatal(err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateVoid {
		t.Errorf("state = %q", p.State)
	}
	if x := loadIndex(t, s, records.ProposalIndexKey(repoName), records.SchemaProposalIndex); len(x.Entries) != 0 {
		t.Errorf("index = %v", x.Entries)
	}
	if err := records.VoidProposal(ctx, s, tenant, repoName, "p1"); err != nil {
		t.Errorf("again: %v", err)
	}
	put(t, s, records.ProposalKey(repoName, "p2"), &records.Proposal{PullRequest: 13, ExpectedDigest: "d", State: records.StateMerged})
	if err := records.VoidProposal(ctx, s, tenant, repoName, "p2"); !errors.Is(err, records.ErrMoved) {
		t.Errorf("a merged proposal: %v, want ErrMoved", err)
	}
}

func TestARequestIsAnsweredOnce(t *testing.T) {
	forEachBackend(t, 2, func(t *testing.T, clients []store.Store) {
		ctx := context.Background()
		if err := openRequest(ctx, clients[0], 431, requestKey); err != nil {
			t.Fatal(err)
		}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, id := range []string{"a1", "a2"} {
			wg.Go(func() { errs[i] = records.Answer(ctx, clients[i], tenant, repoName, id, *approval(""), nil) })
		}
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("answers = %v, %v; want exactly one to win", errs[0], errs[1])
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, records.ErrAnswered) {
				t.Errorf("the losing answer = %v, want ErrAnswered", err)
			}
		}
	})
}
