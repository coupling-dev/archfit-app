package records_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

type confirmDuringSwap struct {
	store.Store
	confirm func()
}

func (s *confirmDuringSwap) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	if s.confirm != nil {
		f := s.confirm
		s.confirm = nil
		f()
	}
	return s.Store.CompareAndSwapAll(ctx, t, swaps)
}

func TestSweepOutsideCannotUndoAConcurrentConfirmation(t *testing.T) {
	forEachBackend(t, 1, func(t *testing.T, clients []store.Store) {
		s := clients[0]
		expected := records.Repo{ApprovedDigest: "approved", ApprovedCommit: "c0", OutsideFlow: &records.OutsideFlow{Digest: "new", Issue: 9}}
		put(t, s, records.RepoKey(repoName), &expected)
		racing := &confirmDuringSwap{Store: s, confirm: func() {
			if _, err := records.Confirm(t.Context(), s, tenant, repoName, "confirmation", ownerApproval(records.KindConfirm), "c2", "new"); err != nil {
				t.Fatal(err)
			}
		}}
		push := records.Push{Before: "c0", After: "c2"}
		_, _, err := records.OutsideIf(t.Context(), racing, tenant, repoName, push, "new", expected)
		if !errors.Is(err, records.ErrMoved) {
			t.Fatalf("OutsideIf = %v, want moved", err)
		}
		r := load[records.Repo](t, s, records.RepoKey(repoName))
		if r.OutsideFlow != nil || r.ApprovedDigest != "new" || r.ApprovedCommit != "c2" {
			t.Fatalf("confirmation undone: %+v", r)
		}
		if _, err := s.Get(t.Context(), tenant, records.PolicyPushKey(repoName, push)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("stale sweep wrote a judgement: %v", err)
		}
	})
}
