package approvals

import (
	"context"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

type observedLease struct {
	store.Store
	read chan struct{}
}

func (s *observedLease) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	raw, err := s.Store.Get(ctx, t, key)
	if key == records.ProposalLeaseKey(testRepo.FullName) {
		select {
		case s.read <- struct{}{}:
		default:
		}
	}
	return raw, err
}

func TestEditRetirementWaitsForInFlightCheckPublication(t *testing.T) {
	w := newWorld(t)
	w.edited()
	clock := func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC) }
	w.svc.now = clock
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	release, err := proposals.Hold(ctx, testRepo, w.mem, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	observer := &observedLease{Store: w.mem, read: make(chan struct{}, 1)}
	w.svc.store = observer
	w.gh.commit("code", policyV(0))
	done := make(chan error, 1)
	go func() { done <- w.svc.retireMovedEdits(ctx, testRepo) }()
	select {
	case <-observer.read:
	case err := <-done:
		t.Fatalf("retirement bypassed the publisher's lease: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var a records.Approval
	if err := records.Load(ctx, w.mem, w.tenant(), records.ApprovalKey(testRepo.FullName, "edit"), &a); err != nil {
		t.Fatal(err)
	}
	if a.State != records.StateOpen || len(w.gh.checks) != 0 {
		t.Fatal("retirement ran before publication finished")
	}
	if _, err := w.gh.CreateCheckRun(ctx, testRepo.RepoRef, api.CheckRun{Name: proposals.GateCheck, HeadSHA: "edit-head",
		ExternalID: proposals.ExternalID("edit-proposal"), Status: "completed", Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := records.Load(ctx, w.mem, w.tenant(), records.ApprovalKey(testRepo.FullName, "edit"), &a); err != nil {
		t.Fatal(err)
	}
	if a.State != records.StateVoid || len(w.gh.checks) < 2 || w.gh.checks[len(w.gh.checks)-1].Conclusion == "success" {
		t.Fatal("late publication restored success on a void edit")
	}
}
