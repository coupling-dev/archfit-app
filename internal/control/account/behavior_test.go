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

func TestTrialGraceSuspensionTimeline(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	a, _, _, _ := newTest(s)
	mustNil(t, a.Observe(ctx, instA, true, day(0)))

	for h := range 40 * 24 {
		now := t0.Add(time.Duration(h) * time.Hour)
		d := h / 24
		mustNil(t, a.Refresh(ctx, instA, now))
		e := readEntitlement(t, s, instA)

		if got, want := gateSuspended(e, true, now), d >= 37; got != want {
			t.Fatalf("day %d hour %d: private suspended = %v, want %v", d, h%24, got, want)
		}
		if gateSuspended(e, false, now) {
			t.Fatalf("day %d: public repository suspended", d)
		}
		visible := e.Notice != "" && !e.WarnFrom.IsZero() && !now.Before(e.WarnFrom)
		if want := d >= 23; visible != want {
			t.Fatalf("day %d hour %d: notice visible = %v, want %v", d, h%24, visible, want)
		}
		if got, want := strings.HasSuffix(e.Notice, "1 day left"), d == 29; got != want {
			t.Fatalf("day %d hour %d: Notice = %q, 1-day suffix want %v", d, h%24, e.Notice, want)
		}
	}
}

func TestRestoredEntitlementOnlyMovesTheDate(t *testing.T) {
	ctx := context.Background()
	s := newSpy()
	a, _, _, _ := newTest(s)
	mustNil(t, a.Observe(ctx, instA, true, day(0)))
	now := day(45)
	mustNil(t, a.Refresh(ctx, instA, now))
	if !gateSuspended(readEntitlement(t, s.Store, instA), true, now) {
		t.Fatal("setup: not suspended")
	}
	callsBefore := s.count()

	until := day(120)
	mustNil(t, a.Grant(ctx, instA, until, now))

	e := readEntitlement(t, s.Store, instA)
	wantTime(t, "SuspendAfter", e.SuspendAfter, until)
	if gateSuspended(e, true, now) {
		t.Error("still suspended after the grant")
	}
	for _, c := range s.calls[callsBefore:] {
		if c.tenant != store.Tenant(instA) || (c.key != recordKeyName && c.key != gate.EntitlementKey) {
			t.Errorf("Grant touched %s tenant=%d key=%q", c.op, c.tenant, c.key)
		}
	}
}

func TestBudgetExhaustionTouchesNothingElse(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	a, c, b, _ := newTest(s)
	now := day(3)
	mustNil(t, a.Observe(ctx, instA, true, now))
	mustNil(t, c.Record(ctx, instA, Consent{Kind: ConsentManagedLLM, By: "octocat", At: now}))
	before := rawKey(t, s, instA, gate.EntitlementKey)

	mustNil(t, b.Reserve(ctx, capMicroUSD, now))
	if err := b.Reserve(ctx, 1, now); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}

	mustNil(t, a.Observe(ctx, instA, true, now))
	mustNil(t, a.Refresh(ctx, instA, now))
	st, err := a.Status(ctx, instA, now)
	mustNil(t, err)
	if st.Plan != PlanTrial {
		t.Errorf("Plan = %q, want trial", st.Plan)
	}
	if !c.Has(ctx, instA, ConsentManagedLLM) {
		t.Error("Has = false")
	}
	if !bytes.Equal(rawKey(t, s, instA, gate.EntitlementKey), before) {
		t.Error("entitlement record changed")
	}
}

func TestReinstallStartsNewTenant(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	a, _, _, _ := newTest(s)
	mustNil(t, a.Observe(ctx, instA, true, day(0)))
	mustNil(t, a.Forget(ctx, instA))

	mustNil(t, a.Observe(ctx, instB, true, day(50)))
	st, err := a.Status(ctx, instB, day(50))
	mustNil(t, err)
	if st.Plan != PlanTrial {
		t.Errorf("B: Plan = %q, want trial", st.Plan)
	}
	wantTime(t, "B TrialEnds", st.TrialEnds, day(80))

	if rawKey(t, s, instA, recordKeyName) != nil || rawKey(t, s, instA, gate.EntitlementKey) != nil {
		t.Error("A has keys after Forget")
	}
	if st, _ := a.Status(ctx, instA, day(50)); st.Plan != PlanPublic {
		t.Errorf("A: Plan = %q, want public", st.Plan)
	}
}
