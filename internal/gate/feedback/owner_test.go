package feedback

import (
	"context"
	"errors"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestOwnerVerdictIsResolvedInsideThePublicationLease(t *testing.T) {
	h := newHarness(headB)
	called := false
	h.pub = New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now,
		func(ctx context.Context, r api.Repo, pull int, head string) (*api.CheckRun, error) {
			called = true
			if _, err := h.store.Get(ctx, store.Tenant(r.InstallationID), leaseKeyB); err != nil {
				t.Errorf("owner verdict read without head lease: %v", err)
			}
			if pull != 7 || head != headB {
				t.Errorf("target = %d, %s", pull, head)
			}
			return &api.CheckRun{Name: gateCheck, HeadSHA: head, ExternalID: "approval:a1",
				Status: "completed", Conclusion: "success", Title: "owner approved", Summary: "not an engine pass"}, nil
		}, nil)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "policy_differs"))
	if err := h.pub.Publish4(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("owner verdict not consulted")
	}
	check := h.checks.latest(t, headB, gateCheck)
	if check.Conclusion != "success" || check.ExternalID != "approval:a1" || check.Summary != "not an engine pass" {
		t.Errorf("check = %+v", check)
	}
}

func TestOwnerVerdictReadFailureWritesNothing(t *testing.T) {
	h := newHarness(headB)
	failed := errors.New("approval unavailable")
	h.pub = New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now,
		func(context.Context, api.Repo, int, string) (*api.CheckRun, error) { return nil, failed }, nil)
	err := h.pub.Publish4(context.Background(), pub4(prTarget(headB), 7, 1, engineResult(t, "pass")))
	if !errors.Is(err, failed) {
		t.Fatalf("Publish4 = %v", err)
	}
	if len(h.checks.calls) != 0 {
		t.Error("check written despite unreadable approval")
	}
	if _, err := h.store.Get(context.Background(), 1, leaseKeyB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("known-unwritten failure retained lease: %v", err)
	}
}

func TestOwnerCheckCannotWriteWithoutTheHeadLease(t *testing.T) {
	h := newHarness(headB)
	failed := errors.New("store unavailable")
	st := &faultyStore{Store: h.store, failSwap: failed}
	err := WriteOwnerCheck(context.Background(), h.checks, st, prTarget(headB).Repo,
		api.CheckRun{Name: gateCheck, HeadSHA: headB, ExternalID: "approval:a1", Status: "completed", Conclusion: "success"}, h.clock.now)
	if !errors.Is(err, failed) {
		t.Fatalf("WriteOwnerCheck = %v", err)
	}
	if len(h.checks.calls) != 0 {
		t.Error("owner check written without lease")
	}
}
