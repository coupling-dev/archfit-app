package account

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestObserveStartsTrialOnce(t *testing.T) {
	ctx := context.Background()
	a, _, _, _ := newTest(store.NewMemory())

	mustNil(t, a.Observe(ctx, instA, false, day(0)))
	st, err := a.Status(ctx, instA, day(0))
	mustNil(t, err)
	if st.Plan != PlanPublic || !st.TrialEnds.IsZero() {
		t.Fatalf("after Observe(false): %+v, want public", st)
	}

	steps := []struct {
		private bool
		at      int
	}{{true, 0}, {true, 5}, {false, 6}}
	for _, step := range steps {
		mustNil(t, a.Observe(ctx, instA, step.private, day(step.at)))
		st, err := a.Status(ctx, instA, day(step.at))
		mustNil(t, err)
		if st.Plan != PlanTrial {
			t.Errorf("day %d: Plan = %q, want trial", step.at, st.Plan)
		}
		wantTime(t, "TrialEnds", st.TrialEnds, day(30))
	}
}

func TestObserveBackfillsMissingRecord(t *testing.T) {
	ctx := context.Background()
	t1 := day(12).Add(90 * time.Minute)

	t.Run("private", func(t *testing.T) {
		s := store.NewMemory()
		a, _, _, _ := newTest(s)
		mustNil(t, a.Observe(ctx, instA, true, t1))
		if rawKey(t, s, instA, recordKeyName) == nil {
			t.Error("account key missing")
		}
		e := readEntitlement(t, s, instA)
		wantTime(t, "SuspendAfter", e.SuspendAfter, t1.Add(37*24*time.Hour))
		st, err := a.Status(ctx, instA, t1)
		mustNil(t, err)
		if st.Plan != PlanTrial {
			t.Errorf("Plan = %q, want trial", st.Plan)
		}
		wantTime(t, "TrialEnds", st.TrialEnds, t1.Add(30*24*time.Hour))
	})

	t.Run("public", func(t *testing.T) {
		s := store.NewMemory()
		a, _, _, _ := newTest(s)
		mustNil(t, a.Observe(ctx, instA, false, t1))
		if e := readEntitlement(t, s, instA); e != (gate.Entitlement{}) {
			t.Errorf("entitlement = %+v, want the zero record", e)
		}
	})
}

func TestGrantOverridesAndLapses(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	a, _, _, logs := newTest(s)
	mustNil(t, a.Observe(ctx, instA, true, day(0)))
	now := day(40)
	if st, _ := a.Status(ctx, instA, now); st.Plan != PlanSuspended {
		t.Fatalf("Plan = %q, want suspended", st.Plan)
	}

	until := now.Add(90 * 24 * time.Hour)
	mustNil(t, a.Grant(ctx, instA, until, now))
	st, err := a.Status(ctx, instA, now)
	mustNil(t, err)
	if st.Plan != PlanGranted {
		t.Errorf("Plan = %q, want granted", st.Plan)
	}
	wantTime(t, "Status.SuspendAfter", st.SuspendAfter, until)
	wantTime(t, "Entitlement.SuspendAfter", readEntitlement(t, s, instA).SuspendAfter, until)

	mustNil(t, a.Refresh(ctx, instA, until))
	if st, _ := a.Status(ctx, instA, until); st.Plan != PlanSuspended {
		t.Errorf("after the grant: Plan = %q, want suspended", st.Plan)
	}
	if !gateSuspended(readEntitlement(t, s, instA), true, until) {
		t.Error("gate rule: not suspended after the grant ended")
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	var audit int
	for _, l := range lines {
		if strings.Contains(l, `"installation_id":42`) && strings.Contains(l, "grant") {
			audit++
		}
	}
	if audit != 1 {
		t.Errorf("audit lines = %d, want 1; log:\n%s", audit, logs)
	}
}

func TestRefreshIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newSpy()
	a, _, _, _ := newTest(s)
	mustNil(t, a.Observe(ctx, instA, true, day(0)))
	recordBytes := rawKey(t, s, instA, recordKeyName)
	entWrites := len(s.writes(gate.EntitlementKey))

	mustNil(t, a.Refresh(ctx, instA, day(5)))
	mustNil(t, a.Refresh(ctx, instA, day(5)))

	writes := s.writes(gate.EntitlementKey)
	if len(writes) != entWrites+2 {
		t.Fatalf("entitlement writes = %d, want %d", len(writes), entWrites+2)
	}
	if !bytes.Equal(writes[len(writes)-1].value, writes[len(writes)-2].value) {
		t.Errorf("entitlement bytes differ:\n%s\n%s", writes[len(writes)-2].value, writes[len(writes)-1].value)
	}
	// Refresh checks the record it derived the entitlement from and writes it back unchanged.
	for _, w := range s.writes(recordKeyName) {
		if w.op == "Put" || (w.op == opSwap && !bytes.Equal(w.value, recordBytes)) {
			t.Errorf("account record written: %s %q", w.op, w.value)
		}
	}
	if !bytes.Equal(rawKey(t, s, instA, recordKeyName), recordBytes) {
		t.Error("account record changed")
	}
}

func TestConsentRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, c, _, _ := newTest(store.NewMemory())
	k := ConsentManagedLLM

	if c.Has(ctx, instA, k) {
		t.Error("Has before Record = true")
	}
	if _, ok, err := c.Get(ctx, instA, k); ok || err != nil {
		t.Errorf("Get before Record = %v, %v; want false, nil", ok, err)
	}
	mustNil(t, c.Record(ctx, instA, Consent{Kind: k, By: "octocat", ByID: 1, At: day(1)}))
	if !c.Has(ctx, instA, k) {
		t.Error("Has after Record = false")
	}
	if got, ok, err := c.Get(ctx, instA, k); !ok || err != nil || got != (Consent{Kind: k, By: "octocat", ByID: 1, At: day(1).UTC()}) {
		t.Errorf("Get after Record = %+v, %v, %v; want octocat (1) at %v", got, ok, err, day(1))
	}
	if c.Has(ctx, instA, ConsentSourceProcessing) {
		t.Error("Has for the other kind = true")
	}
	if c.Has(ctx, instB, k) {
		t.Error("Has for another installation = true")
	}
	mustNil(t, c.Revoke(ctx, instA, k))
	if c.Has(ctx, instA, k) {
		t.Error("Has after Revoke = true")
	}
	if _, ok, err := c.Get(ctx, instA, k); ok || err != nil {
		t.Errorf("Get after Revoke = %v, %v; want false, nil", ok, err)
	}
	mustNil(t, c.Revoke(ctx, instA, k))
}

func TestBudgetReserveSettle(t *testing.T) {
	ctx := context.Background()
	const month = "2026-09"
	now := day(3)

	t.Run("cap", func(t *testing.T) {
		s := store.NewMemory()
		_, _, b, _ := newTest(s)
		for i := range 4 {
			if err := b.Reserve(ctx, 5_000_000, now); err != nil {
				t.Fatalf("reserve %d: %v", i+1, err)
			}
		}
		if err := b.Reserve(ctx, 5_000_000, now); !errors.Is(err, ErrBudgetExhausted) {
			t.Fatalf("fifth reserve: %v, want ErrBudgetExhausted", err)
		}
		if got := counter(t, s, month); got != capMicroUSD {
			t.Errorf("counter = %d, want %d", got, capMicroUSD)
		}
		mustNil(t, b.Settle(ctx, 5_000_000, 1_000_000, now))
		if got := counter(t, s, month); got != 16_000_000 {
			t.Errorf("counter after settle = %d, want 16000000", got)
		}
		mustNil(t, b.Reserve(ctx, 4_000_000, now))
		if got := counter(t, s, month); got != capMicroUSD {
			t.Errorf("counter = %d, want %d", got, capMicroUSD)
		}
	})

	settles := []struct {
		name   string
		actual int64
		delta  int64
	}{
		{"actual 0", 0, -5_000_000},
		{"actual 2000000", 2_000_000, -3_000_000},
		{"failed call, outcome unknown", 5_000_000, 0},
		{"actual above the reservation", 6_000_000, 1_000_000},
	}
	for _, tt := range settles {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			_, _, b, _ := newTest(s)
			mustNil(t, b.Reserve(ctx, 5_000_000, now))
			before := counter(t, s, month)
			mustNil(t, b.Settle(ctx, 5_000_000, tt.actual, now))
			if got := counter(t, s, month) - before; got != tt.delta {
				t.Errorf("delta = %d, want %d", got, tt.delta)
			}
		})
	}
}

func TestForgetDeletesTenantOnly(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	a, c, b, _ := newTest(s)
	keys := []string{recordKeyName, gate.EntitlementKey, "consent/managed_llm", "consent/source_processing"}
	for _, id := range []int64{instA, instB} {
		mustNil(t, a.Observe(ctx, id, true, day(0)))
		mustNil(t, c.Record(ctx, id, Consent{Kind: ConsentManagedLLM, By: "octocat", ByID: 1, At: day(0)}))
		mustNil(t, c.Record(ctx, id, Consent{Kind: ConsentSourceProcessing, By: "octocat", ByID: 1, At: day(0)}))
	}
	mustNil(t, b.Reserve(ctx, 3_000_000, day(0)))
	wantB := map[string][]byte{}
	for _, k := range keys {
		wantB[k] = rawKey(t, s, instB, k)
		if wantB[k] == nil {
			t.Fatalf("setup: B has no %q", k)
		}
	}

	mustNil(t, a.Forget(ctx, instA))
	mustNil(t, a.Forget(ctx, instA))

	tenants, err := s.Tenants(ctx)
	mustNil(t, err)
	if len(tenants) != 1 || tenants[0] != store.Tenant(instB) {
		t.Errorf("tenants = %v, want [%d]", tenants, instB)
	}
	for _, k := range keys {
		if rawKey(t, s, instA, k) != nil {
			t.Errorf("A still has %q", k)
		}
		if !bytes.Equal(rawKey(t, s, instB, k), wantB[k]) {
			t.Errorf("B's %q changed", k)
		}
	}
	if got := counter(t, s, "2026-09"); got != 3_000_000 {
		t.Errorf("counter = %d, want 3000000", got)
	}
}
