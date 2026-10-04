package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

var leaseKeyB = leasePrefix + headB

// faultyStore fails the named operation; everything else reaches the memory store.
type faultyStore struct {
	store.Store
	failGet, failSwap error
}

func (s *faultyStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if s.failGet != nil {
		return nil, s.failGet
	}
	return s.Store.Get(ctx, t, key)
}

func (s *faultyStore) CompareAndSwap(ctx context.Context, t store.Tenant, key string, old, value []byte) (bool, error) {
	if s.failSwap != nil {
		return false, s.failSwap
	}
	return s.Store.CompareAndSwap(ctx, t, key, old, value)
}

func putLease(t *testing.T, s store.Store, raw []byte) {
	t.Helper()
	if err := s.Put(context.Background(), 1, leaseKeyB, raw); err != nil {
		t.Fatalf("Put lease: %v", err)
	}
}

func leaseBytes(t *testing.T, token string, expires time.Time) []byte {
	t.Helper()
	raw, err := json.Marshal(lease{Token: token, Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A lease another holder still holds is waited for; one that expired, or that cannot be read, is taken over.
func TestLeaseTakeover(t *testing.T) {
	for _, tt := range []struct {
		name      string
		stored    func(*testing.T) []byte
		wantWaits bool
	}{
		{"no lease", nil, false},
		{"expired lease", func(t *testing.T) []byte { return leaseBytes(t, "other", t0) }, false},
		{"unreadable lease", func(*testing.T) []byte { return []byte("not json") }, false},
		{"empty lease value", func(*testing.T) []byte { return []byte{} }, false},
		{"held lease", func(t *testing.T) []byte { return leaseBytes(t, "other", t0.Add(time.Second)) }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			if tt.stored != nil {
				putLease(t, h.store, tt.stored(t))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waited := false
			h.onContention(func() {
				waited = true
				cancel()
			})
			err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1))
			if waited != tt.wantWaits {
				t.Errorf("waited = %v, want %v", waited, tt.wantWaits)
			}
			if tt.wantWaits {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Publish error = %v, want context.Canceled", err)
				}
				if n := len(h.checks.calls); n != 0 {
					t.Errorf("%d GitHub calls while another holder had the lease, want 0", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if got := h.checks.latest(t, headB, gateCheck); got.Status != statusCompleted {
				t.Errorf("gate check status %s, want completed", got.Status)
			}
			if _, err := h.store.Get(ctx, 1, leaseKeyB); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("lease left behind: %v", err)
			}
		})
	}
}

// A held lease is waited for until it expires, by the clock the lease was written with.
func TestLeaseWaitsUntilExpiry(t *testing.T) {
	h := newHarness(headB)
	putLease(t, h.store, leaseBytes(t, "other", t0.Add(leaseTTL)))
	waits := 0
	h.onContention(func() {
		waits++
		h.clock.set(t0.Add(time.Duration(waits) * 20 * time.Second))
	})
	if err := h.pub.Publish(context.Background(), blockedPublication(t, headB, 7, 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if waits != 3 {
		t.Errorf("%d waits, want 3: the lease expires at 60 s", waits)
	}
}

// Without a lease nothing is written: a Store fault, a target without a slot, or a failed release never turn into
// an unserialized write.
func TestLeaseFailsClosed(t *testing.T) {
	boom := errors.New("store down")
	noInstallation := prTarget(headB)
	noInstallation.Repo.InstallationID = 0
	noHead := prTarget(headB)
	noHead.HeadSHA = ""
	for _, tt := range []struct {
		name   string
		target inputs.Target
		store  func(store.Store) store.Store
	}{
		{"Get fails", prTarget(headB), func(s store.Store) store.Store { return &faultyStore{Store: s, failGet: boom} }},
		{"CompareAndSwap fails", prTarget(headB), func(s store.Store) store.Store { return &faultyStore{Store: s, failSwap: boom} }},
		{"no installation", noInstallation, func(s store.Store) store.Store { return s }},
		{"no head", noHead, func(s store.Store) store.Store { return s }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			pub := New(h.checks, h.comments, h.pulls, tt.store(h.store), h.clock.now)
			p := blockedPublication(t, headB, 7, 1)
			p.Target = tt.target
			ctx := context.Background()
			for name, call := range map[string]func() error{
				"Publish": func() error { return pub.Publish(ctx, p) },
				"Pending": func() error { return pub.Pending(ctx, tt.target) },
				"Warn":    func() error { return pub.Warn(ctx, tt.target, t0.Add(48*time.Hour)) },
			} {
				if err := call(); err == nil {
					t.Errorf("%s: want an error", name)
				}
			}
			if n := len(h.checks.calls) + len(h.comments.calls); n != 0 {
				t.Errorf("%d GitHub calls without a lease, want 0", n)
			}
		})
	}
}

// The holder's work stops before the lease can be taken over, by elapsed time and not by the injected clock; the
// release survives the end of the request context and never deletes a lease another holder took over.
func TestLeaseHolding(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	s := h.pub.(*serialized)

	start := time.Now()
	err := s.hold(ctx, prTarget(headB), func(work context.Context) error {
		deadline, ok := work.Deadline()
		if !ok || deadline.After(start.Add(leaseTTL-leaseMargin+time.Second)) || deadline.Before(start) {
			t.Errorf("work deadline %v (set %v), want about %v from now", deadline, ok, leaseTTL-leaseMargin)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	reqCtx, cancel := context.WithCancel(ctx)
	if err := s.hold(reqCtx, prTarget(headB), func(context.Context) error { cancel(); return nil }); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := h.store.Get(ctx, 1, leaseKeyB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("lease not released after the request context ended: %v", err)
	}

	theirs := leaseBytes(t, "other", t0.Add(time.Hour))
	if err := s.hold(ctx, prTarget(headB), func(context.Context) error { putLease(t, h.store, theirs); return nil }); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if got, err := h.store.Get(ctx, 1, leaseKeyB); err != nil || string(got) != string(theirs) {
		t.Errorf("lease after a takeover = %q, %v, want the other holder's", got, err)
	}

	// Leases are per head: while head B is held, head A of the same repository is not blocked.
	otherCtx, stop := context.WithCancel(ctx)
	defer stop()
	h.onContention(func() {
		t.Error("waited for another head's lease")
		stop()
	})
	if err := s.hold(otherCtx, prTarget(headA), func(context.Context) error { return nil }); err != nil {
		t.Errorf("hold on another head: %v", err)
	}
}

// A call that failed may have sent a write GitHub still applies, so its lease stays until it expires and the next
// holder waits for it. A call that failed before any write releases at once.
func TestLeaseKeptAfterFailedCall(t *testing.T) {
	for _, tt := range []struct {
		name     string
		err      error
		wantKept bool
	}{
		{"uncertain write", context.DeadlineExceeded, true},
		{"nothing written", ErrStaleInputs, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(headB)
			s := h.pub.(*serialized)
			if err := s.hold(ctx, prTarget(headB), func(context.Context) error { return tt.err }); !errors.Is(err, tt.err) {
				t.Fatalf("hold error = %v, want %v", err, tt.err)
			}
			raw, err := h.store.Get(ctx, 1, leaseKeyB)
			if kept := err == nil && held(raw, t0); kept != tt.wantKept {
				t.Fatalf("lease kept = %v (%v), want %v", kept, err, tt.wantKept)
			}
			var waited time.Duration
			h.onContention(func() {
				waited += 20 * time.Second
				h.clock.set(t0.Add(waited))
			})
			if err := s.hold(ctx, prTarget(headB), func(context.Context) error { return nil }); err != nil {
				t.Fatalf("next hold: %v", err)
			}
			if want := map[bool]time.Duration{true: leaseTTL, false: 0}[tt.wantKept]; waited != want {
				t.Errorf("next holder waited %v, want %v", waited, want)
			}
		})
	}
}

// An upload decided from policy P1 waits while a sweep, which already reads P2, corrects the check to stale_inputs
// under the same run identity. Inside the lease the upload's inputs are read again: a decision from inputs that are
// no longer trusted writes nothing, so the correction stays. Any change counts, not only a digest: an owner approval
// dismissed between the two reads leaves every digest accepted, yet the decision no longer holds.
func TestStaleDecisionNeverReplacesNewerInputs(t *testing.T) {
	const p1, p2 = policyDigest, "5555555555555555555555555555555555555555555555555555555555555555"
	ctx := context.Background()
	trustedWith := func(policy string) inputs.Trusted {
		tr := trusted(settings.GateRequired)
		tr.PolicyDigests = []string{policy}
		return tr
	}
	approved := func(approval inputs.Approval, by string) inputs.Trusted {
		tr := trustedWith(p2)
		tr.ProtectedPaths, tr.Approval, tr.ApprovedBy = []string{".github/archfit.yaml"}, approval, by
		return tr
	}
	boom := errors.New("protected ref unreadable")
	for _, tt := range []struct {
		name                       string
		uploadPolicy, sweptPolicy  string
		decided, fresh             inputs.Trusted
		resolveErr                 error
		wantErr                    error
		wantConclusion, wantReason string
	}{
		{"inputs changed after the decision", p1, p2, trustedWith(p1), trustedWith(p2), nil, ErrStaleInputs, "action_required", "stale_inputs"},
		{"inputs unreadable again", p1, p2, trustedWith(p1), inputs.Trusted{}, boom, boom, "action_required", "stale_inputs"},
		{"owner approval dismissed after the decision", p2, p1, approved(inputs.ApprovalGranted, "alice"), approved(inputs.ApprovalMissing, ""), nil, ErrStaleInputs, "action_required", "stale_inputs"},
		{"decision from the current inputs", p2, p1, trustedWith(p2), trustedWith(p2), nil, nil, "success", "healthy"},
		{"approval unchanged since the decision", p2, p1, approved(inputs.ApprovalGranted, "alice"), approved(inputs.ApprovalGranted, "alice"), nil, nil, "success", "healthy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			stale := verdict.Without(verdict.ReasonStaleInputs)
			swept := receiptFor(stale, 7, 1)
			swept.PolicyDigest = tt.sweptPolicy
			correction := Publication{Target: prTarget(headB), Outcome: stale, Receipt: swept, Trusted: trustedWith(tt.sweptPolicy)}
			if err := h.pub.Publish(ctx, correction); err != nil {
				t.Fatalf("sweep correction: %v", err)
			}

			healthy := outcome(verdict.Success, reasonHealthy)
			decided := receiptFor(healthy, 7, 1)
			decided.PolicyDigest = tt.uploadPolicy
			upload := Publication{
				Target: prTarget(headB), Identity: fullIdentity(), Outcome: healthy, Receipt: decided,
				Report: loadReport(t, "healthy"), Trusted: tt.decided,
				Resolve: func(context.Context) (inputs.Trusted, error) { return tt.fresh, tt.resolveErr },
			}
			if err := h.pub.Publish(ctx, upload); !errors.Is(err, tt.wantErr) {
				t.Errorf("upload Publish error = %v, want %v", err, tt.wantErr)
			}
			gate := h.checks.latest(t, headB, gateCheck)
			if got, _ := parseReceipt(gate.Summary); gate.Conclusion != tt.wantConclusion || string(got.Reason) != tt.wantReason {
				t.Errorf("gate shows %s/%s, want %s/%s", gate.Conclusion, got.Reason, tt.wantConclusion, tt.wantReason)
			}
			if _, err := h.store.Get(ctx, 1, leaseKeyB); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("lease kept after the upload: %v", err)
			}
		})
	}
}
