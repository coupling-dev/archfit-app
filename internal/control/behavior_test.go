package control

import (
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

func (h *harness) rawEntitlement(id int64) string {
	h.t.Helper()
	raw, err := h.store.Store.Get(ctx, store.Tenant(id), gate.EntitlementKey)
	if err != nil {
		h.t.Fatalf("entitlement of %d: %v", id, err)
	}
	return string(raw)
}

func TestTrialLifecycleThroughTheFacade(t *testing.T) {
	h := newHarness(t)
	h.gh.install(7, ghr(70, "org7/app", true))
	h.event(webhook.InstallationAdded, 7)

	var noticeFrom, lastDayFrom, suspendedFrom time.Duration
	for hour := range 40 * 24 {
		h.clock.Add(time.Hour)
		if err := h.c.Sweep(ctx); err != nil {
			t.Fatalf("Sweep at hour %d: %v", hour, err)
		}
		e, ok := h.entitlement(7)
		if !ok {
			t.Fatalf("no record at hour %d", hour)
		}
		now, age := h.clock.Now(), h.clock.Now().Sub(t0)
		if noticeFrom == 0 && !e.WarnFrom.IsZero() && !now.Before(e.WarnFrom) {
			noticeFrom = age
		}
		if lastDayFrom == 0 && strings.Contains(e.Notice, "1 day left") {
			lastDayFrom = age
		}
		if suspendedFrom == 0 && !e.SuspendAfter.IsZero() && !now.Before(e.SuspendAfter) {
			suspendedFrom = age
		}
	}
	if noticeFrom != 23*day || lastDayFrom != 29*day || suspendedFrom != 37*day {
		t.Errorf("notice from %v, \"1 day left\" from %v, suspended from %v; want days 23, 29 and 37",
			noticeFrom, lastDayFrom, suspendedFrom)
	}

	until := h.clock.Now().Add(90 * day)
	if err := h.c.Grant(ctx, 7, until); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if e, _ := h.entitlement(7); !e.SuspendAfter.Equal(until) {
		t.Errorf("SuspendAfter = %v, want the grant end %v", e.SuspendAfter, until)
	}
	if n := h.gh.writeCount(); n != 0 {
		t.Errorf("%d GitHub writes, want none: control never touches a check run", n)
	}
}

func TestStoreOutageIsNotNonPayment(t *testing.T) {
	h := newHarness(t)
	h.gh.install(7, ghr(70, "org7/app", true))
	h.event(webhook.InstallationAdded, 7)
	h.clock.Add(36 * day)
	if err := h.c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	lastGood, writes := h.rawEntitlement(7), h.store.writes
	h.store.set(func(s *flakyStore) { s.failGet = true })

	for range 3 {
		h.clock.Add(day) // days 37, 38, 39: across the suspension date
		if err := h.c.Sweep(ctx); err != nil {
			t.Errorf("Sweep during the outage = %v, want nil", err)
		}
	}

	if h.store.writes != writes {
		t.Errorf("%d records written during the outage, want none", h.store.writes-writes)
	}
	if got := h.rawEntitlement(7); got != lastGood {
		t.Errorf("record changed during the outage:\n%s\n%s", lastGood, got)
	}
}

func TestControlFailureNeverReachesTheGate(t *testing.T) {
	h := newHarness(t)
	r := repo(7, "org7/app", true)
	h.gh.install(7, ghr(70, r.FullName, true))
	h.gh.allow("alice", r.FullName, "admin")
	h.event(webhook.InstallationAdded, 7)
	b := newBrowser(t, h.c.UIHandler())
	b.signIn("alice")
	lastGood := h.rawEntitlement(7)

	h.store.set(func(s *flakyStore) { s.failAll = true })
	h.gh.set(func(g *fakeGitHub) { g.down = true })
	h.clock.Add(time.Hour)

	for _, kind := range []webhook.EventKind{webhook.InstallationAdded, webhook.ReposRemoved, webhook.InstallationRemoved} {
		if err := h.c.HandleEvent(ctx, webhook.Event{Kind: kind, InstallationID: 7}); err == nil {
			t.Errorf("HandleEvent(%s) = nil, want the error returned to app", kind)
		}
	}
	if err := h.c.Sweep(ctx); err == nil {
		t.Error("Sweep = nil, want the error returned to app")
	}
	if err := h.c.Grant(ctx, 7, h.clock.Now().Add(day)); err == nil {
		t.Error("Grant = nil, want the error returned to app")
	}
	if rec := b.get("/api/repos/org7/app/policy"); rec.Code < 400 {
		t.Errorf("UI overview = %d, want an error", rec.Code)
	}

	if got := h.rawEntitlement(7); got != lastGood {
		t.Errorf("entitlement changed:\n%s\n%s", lastGood, got)
	}
	if touched := h.gh.writesMatching("check-runs", "comments", "statuses"); len(touched) != 0 {
		t.Errorf("control touched the gate's GitHub surface: %v", touched)
	}
	if n := h.gh.writeCount(); n != 0 {
		t.Errorf("%d GitHub writes, want none", n)
	}
}
