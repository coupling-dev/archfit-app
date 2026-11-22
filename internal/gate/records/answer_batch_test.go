package records_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestAnswerBatchPinsBothApprovalsTogether(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	for _, r := range []struct {
		id  int
		key string
	}{{431, "first"}, {432, "second"}} {
		if err := openRequest(ctx, s, r.id, r.key); err != nil {
			t.Fatal(err)
		}
	}
	a1, a2 := *approval(""), *approval("")
	a1.Request, a2.Request = 431, 432
	pin := &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h1", Digest: "d1"}
	items := []records.BatchAnswer{{ApprovalID: "a1", Approval: a1, Join: true}, {ApprovalID: "a2", Approval: a2, Join: true}}
	if err := records.AnswerBatch(ctx, s, tenant, repoName, items, pin); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{431, 432} {
		if req := load[records.Request](t, s, records.RequestKey(repoName, id)); req.State != records.StateAnswered {
			t.Errorf("request %d state = %q", id, req.State)
		}
	}
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if !slices.Equal(p.Approvals, []string{"a1", "a2"}) {
		t.Errorf("approvals = %v", p.Approvals)
	}
}

func TestAnswerBatchMixedAndDuplicates(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	for _, r := range []struct {
		id  int
		key string
	}{{431, "first"}, {432, "second"}} {
		if err := openRequest(ctx, s, r.id, r.key); err != nil {
			t.Fatal(err)
		}
	}
	a, b := *approval(""), *approval("")
	a.Request, b.Request = 431, 432
	pin := &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h", Digest: "d"}
	items := []records.BatchAnswer{{ApprovalID: "a", Approval: a}, {ApprovalID: "b", Approval: b, Join: true}}
	for _, bad := range [][]records.BatchAnswer{{items[0], items[0]}, {items[0], {ApprovalID: "a", Approval: b}}} {
		if err := records.AnswerBatch(ctx, s, tenant, repoName, bad, pin); err == nil {
			t.Fatal("duplicate accepted")
		}
	}
	if err := records.AnswerBatch(ctx, s, tenant, repoName, items, pin); err != nil {
		t.Fatal(err)
	}
	x := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex)
	if _, ok := x.Lookup("first"); ok {
		t.Error("unjoined request remains indexed")
	}
	if id, ok := x.Lookup("second"); !ok || id != "432" {
		t.Errorf("joined request = %q, %v", id, ok)
	}
}

func TestAnswerBatchRefusalIsAtomic(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(*testing.T, store.Store)
		want error
	}{
		{"answered", func(t *testing.T, s store.Store) {
			put(t, s, records.RequestKey(repoName, 432), &records.Request{Key: "second", State: records.StateAnswered})
		}, records.ErrAnswered},
		{"outside", func(t *testing.T, s store.Store) {
			put(t, s, records.RepoKey(repoName), &records.Repo{OutsideFlow: &records.OutsideFlow{Digest: "other"}})
		}, records.ErrOutsideFlow},
		{"moved", func(t *testing.T, s store.Store) {
			put(t, s, records.ProposalKey(repoName, "p"), &records.Proposal{State: records.StateOpen, EditEpoch: 1})
		}, records.ErrMoved},
		{"full", func(t *testing.T, s store.Store) {
			put(t, s, records.ProposalKey(repoName, "p"), &records.Proposal{State: records.StateOpen, Approvals: make([]string, records.MaxApprovals)})
		}, records.ErrFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.NewMemory()
			ctx := context.Background()
			for _, r := range []struct {
				id  int
				key string
			}{{431, "first"}, {432, "second"}} {
				if err := openRequest(ctx, s, r.id, r.key); err != nil {
					t.Fatal(err)
				}
			}
			tc.seed(t, s)
			a, b := *approval(""), *approval("")
			a.Request, b.Request = 431, 432
			keys := []string{records.ApprovalKey(repoName, "a"), records.ApprovalKey(repoName, "b"), records.RequestKey(repoName, 431), records.RequestKey(repoName, 432), records.RequestIndexKey(repoName), records.ProposalKey(repoName, "p"), records.ProposalIndexKey(repoName), records.RepoKey(repoName)}
			before := snapshot(t, s, keys...)
			err := records.AnswerBatch(ctx, s, tenant, repoName, []records.BatchAnswer{{ApprovalID: "a", Approval: a, Join: true}, {ApprovalID: "b", Approval: b, Join: true}}, &records.Pin{Proposal: "p", PullRequest: 12, HeadSHA: "h", Digest: "d"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if !slices.EqualFunc(before, snapshot(t, s, keys...), bytes.Equal) {
				t.Fatal("partial write")
			}
		})
	}
}
func TestAnswerBatchConcurrentSharedRequest(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	for _, r := range []struct {
		id  int
		key string
	}{{431, "a"}, {432, "b"}, {433, "c"}} {
		if err := openRequest(ctx, s, r.id, r.key); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, b := *approval(""), *approval("")
			a.Request, b.Request = 431, 432+i
			errs[i] = records.AnswerBatch(ctx, s, tenant, repoName, []records.BatchAnswer{{ApprovalID: []string{"a", "b"}[i], Approval: a}, {ApprovalID: []string{"c", "d"}[i], Approval: b}}, nil)
		}(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("results %v", errs)
	}
}
