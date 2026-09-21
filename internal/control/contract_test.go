package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

const day = 24 * time.Hour

var ctx = context.Background()

func TestSweepRefreshesEveryInstallation(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t)
	for _, id := range []int64{1, 2, 3} {
		h.gh.install(id, ghr(id*10, "secret-org/secret-repo", true))
	}
	// Installation 2 is known already; its cached record is lost and GitHub cannot list its repositories.
	if err := h.c.HandleEvent(ctx, webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 2}); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if err := h.store.Delete(ctx, 2, gate.EntitlementKey); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	h.gh.set(func(g *fakeGitHub) { g.reposStatus[2] = http.StatusForbidden })
	h.store.set(func(s *flakyStore) { s.failPut = map[store.Tenant]bool{3: true} })

	if err := h.c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep = %v, want nil", err)
	}

	if _, ok := h.entitlement(1); !ok {
		t.Error("installation 1 was not observed")
	}
	if _, ok := h.entitlement(2); !ok {
		t.Error("installation 2 was not refreshed")
	}
	if _, ok := h.entitlement(3); ok {
		t.Error("installation 3 has a record although Put failed")
	}
	out := logs.String()
	for _, want := range []string{`"installations":3`, `"observed":1`, `"refreshed":1`, `"failed":1`, `"installation_id":3`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
	for _, leak := range []string{"secret-org", "secret-repo", "org1", "tok-"} {
		if strings.Contains(out, leak) {
			t.Errorf("log leaks %q:\n%s", leak, out)
		}
	}
}

func TestSweepBackfillsMissedInstallation(t *testing.T) {
	t.Run("private repository starts the trial at sweep time", func(t *testing.T) {
		h := newHarness(t)
		h.gh.install(7, ghr(70, "org7/app", true))

		if err := h.c.Sweep(ctx); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if _, ok := h.entitlement(7); !ok {
			t.Fatal("no entitlement record after the first sweep")
		}
		first, err := h.status.Status(ctx, 7, h.clock.Now())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if first.Plan != account.PlanTrial || !first.TrialEnds.Equal(t0.Add(30*day)) {
			t.Fatalf("status = %+v, want trial ending %v", first, t0.Add(30*day))
		}

		h.clock.Add(time.Hour)
		if err := h.c.Sweep(ctx); err != nil {
			t.Fatalf("second Sweep: %v", err)
		}
		second, err := h.status.Status(ctx, 7, h.clock.Now())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !second.TrialEnds.Equal(first.TrialEnds) {
			t.Errorf("TrialEnds moved from %v to %v", first.TrialEnds, second.TrialEnds)
		}
	})
	t.Run("public repositories only never suspend", func(t *testing.T) {
		h := newHarness(t)
		h.gh.install(7, ghr(70, "org7/site", false))

		if err := h.c.Sweep(ctx); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		e, ok := h.entitlement(7)
		if !ok {
			t.Fatal("no entitlement record")
		}
		if !e.SuspendAfter.IsZero() {
			t.Errorf("SuspendAfter = %v, want zero", e.SuspendAfter)
		}
	})
}

func TestSweepForgetsOrphanTenants(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t)
	for _, id := range []int64{1, 2, 3} {
		h.gh.install(id, ghr(id*10, "org/app", true))
		if err := h.c.HandleEvent(ctx, webhook.Event{Kind: webhook.InstallationAdded, InstallationID: id}); err != nil {
			t.Fatalf("HandleEvent: %v", err)
		}
	}
	if err := h.c.budget.Reserve(ctx, 1000, h.clock.Now()); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	before1, _ := h.entitlement(1)
	h.gh.uninstall(2) // the installation_removed delivery was missed

	if err := h.c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	got := h.tenants()
	if got[2] || !got[1] || !got[3] {
		t.Errorf("tenants = %v, want 1 and 3 only", got)
	}
	if after1, _ := h.entitlement(1); after1 != before1 {
		t.Errorf("tenant 1 changed: %+v -> %+v", before1, after1)
	}
	if !h.budgetBooked(1000) {
		t.Error("the SystemTenant budget counter changed")
	}
	out := logs.String()
	if !strings.Contains(out, `"forgotten":1`) || !strings.Contains(out, `"installation_id":2`) {
		t.Errorf("log lacks the forgotten tenant:\n%s", out)
	}
}

func TestSweepStopsWhenInstallationsUnavailable(t *testing.T) {
	st := &flakyStore{Store: store.NewMemory()}
	acct, _, _ := account.New(st)
	repos := &fakeRepos{installErr: api.ErrRateLimited}
	c := newControl(acct, repos, st, okHandler, okHandler, newClock().Now)

	err := c.Sweep(ctx)

	if !errors.Is(err, api.ErrRateLimited) {
		t.Errorf("Sweep = %v, want ErrRateLimited", err)
	}
	if st.writes != 0 {
		t.Errorf("%d records written, want none", st.writes)
	}
}

func TestGrantRewritesEntitlement(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t)
	h.gh.install(7, ghr(70, "org7/app", true))
	if err := h.c.HandleEvent(ctx, webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7}); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	h.clock.Add(40 * day)
	if err := h.c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if s, err := h.status.Status(ctx, 7, h.clock.Now()); err != nil || s.Plan != account.PlanSuspended {
		t.Fatalf("status = %+v, %v; want suspended", s, err)
	}
	until := h.clock.Now().Add(90 * day)
	logged := len(logs.String())

	if err := h.c.Grant(ctx, 7, until); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	e, ok := h.entitlement(7)
	if !ok || !e.SuspendAfter.Equal(until) {
		t.Errorf("SuspendAfter = %v, want %v", e.SuspendAfter, until)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()[logged:]), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], ":7") {
		t.Errorf("audit log = %q, want one line with the installation ID", lines)
	}
}

func TestHandlersAreMounted(t *testing.T) {
	h := newHarness(t)
	h.receiver.answer(gate.Upload{}, &gate.Rejection{Status: http.StatusUnauthorized, Code: "oidc_invalid"})

	t.Run("UI asks an anonymous visitor to sign in", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.c.UIHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/r/org7/app", nil))
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/login") {
			t.Errorf("GET /r/org7/app = %d to %q, want 303 to /auth/login", rec.Code, rec.Header().Get("Location"))
		}
		rec = httptest.NewRecorder()
		h.c.UIHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/auth/login") {
			t.Errorf("GET / = %d, want the landing page with a sign-in link", rec.Code)
		}
	})
	t.Run("discovery answers the Receiver's rejection", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.c.DiscoveryHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/discoveries", strings.NewReader("{}")))
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "oidc_invalid") {
			t.Errorf("POST /v1/discoveries = %d %q, want 401 with oidc_invalid", rec.Code, rec.Body.String())
		}
		if h.receiver.calls != 1 {
			t.Errorf("Receiver calls = %d, want 1", h.receiver.calls)
		}
	})
}

func TestSeamsDefaultToNotAvailable(t *testing.T) {
	h := newHarness(t, withoutSandbox, withoutProvider)
	h.gh.install(7, ghr(70, "org7/app", true))
	r := repo(7, "org7/app", true)
	for _, k := range []account.ConsentKind{account.ConsentSourceProcessing, account.ConsentManagedLLM} {
		if err := h.c.consents.Record(ctx, 7, account.Consent{Kind: k, By: "alice", At: h.clock.Now()}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	if _, err := h.c.onboarding.StartManaged(ctx, r, []string{"@org7/owners"}); !errors.Is(err, ErrNoSandbox) {
		t.Errorf("StartManaged = %v, want ErrNoSandbox", err)
	}
	_, err := h.c.assist.Propose(ctx, assist.ModeManaged, assist.Request{
		Repo: r, Branch: "archfit/policy", HeadSHA: strings.Repeat("a", 40), Instruction: "allow ui to import store", By: "alice",
	})
	if !errors.Is(err, assist.ErrManagedUnavailable) {
		t.Errorf("Propose = %v, want ErrManagedUnavailable", err)
	}
	if n := h.gh.writeCount(); n != 0 {
		t.Errorf("%d GitHub writes, want no PR", n)
	}
	if h.store.adds != 0 {
		t.Errorf("%d budget reservations, want none", h.store.adds)
	}
}
