package control

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	onboardingBranch = "archfit/onboarding"
	workflowPath     = ".github/workflows/archfit.yaml"
	policyPath       = ".archfit.yaml"
	monthlyBudget    = 20_000_000 // micro-USD: the App-wide managed-LLM cap of account
)

func (h *harness) rawEntitlement(id int64) string {
	h.t.Helper()
	raw, err := h.store.Store.Get(ctx, store.Tenant(id), gate.EntitlementKey)
	if err != nil {
		h.t.Fatalf("entitlement of %d: %v", id, err)
	}
	return string(raw)
}

func (h *harness) discoveryUpload(r gate.Binding) int {
	h.t.Helper()
	h.receiver.answer(gate.Upload{Binding: r, Payload: []byte(policyDraft)}, nil)
	return h.upload("{}").Code
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

func TestBudgetExhaustionStopsOnlyAssist(t *testing.T) {
	h := newHarness(t)
	r := repo(7, "org7/app", true)
	h.gh.install(7, ghr(70, r.FullName, true))
	h.gh.allow("alice", r.FullName, "admin")
	h.provider.maxCost = 1000
	h.provider.answer = Completion{Text: policyDraft, CostMicroUSD: 10}
	h.sandbox.result = DiscoveryResult{DraftPolicy: []byte(policyDraft)}
	if err := h.c.budget.Reserve(ctx, monthlyBudget, h.clock.Now()); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	h.event(webhook.InstallationAdded, 7)
	if err := h.c.Sweep(ctx); err != nil {
		t.Errorf("Sweep: %v", err)
	}
	h.consent(7, account.ConsentSourceProcessing, account.ConsentManagedLLM)
	if _, err := h.c.onboarding.StartManaged(ctx, r, []string{"org7/owners"}); err != nil {
		t.Fatalf("StartManaged: %v", err)
	}
	h.gh.awaitManagedJob(r.FullName, onboardingBranch, policyPath)
	h.gh.merge(r.FullName, onboardingBranch)

	b := newBrowser(t, h.c.UIHandler())
	b.signIn("alice")
	page := policyPage(t, b, "")
	save := map[string]string{"branch": page.Branch, "head_sha": page.HeadSHA, "content": policyDraft + "# reviewed\n"}
	if rec := b.postJSON("/api/repos/org7/app/policy", save); rec.Code != http.StatusOK {
		t.Fatalf("policy save = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	branch := page.Branch
	saved, ok := h.gh.file(r.FullName, branch, policyPath)
	if !ok || !strings.Contains(saved, "# reviewed") {
		t.Fatalf("policy save did not reach the draft branch %q", branch)
	}

	head := h.gh.pulls(r.FullName)
	if len(head) != 2 {
		t.Fatalf("%d pull requests, want the onboarding and the policy draft", len(head))
	}
	draft := policyPage(t, b, "?branch="+url.QueryEscape(branch))
	_, err := h.c.assist.Propose(ctx, assist.ModeManaged, assist.Request{
		Repo: r, Branch: branch, HeadSHA: draft.HeadSHA, Instruction: "allow ui to import store", By: "alice",
	})
	if !errors.Is(err, account.ErrBudgetExhausted) {
		t.Errorf("Propose = %v, want ErrBudgetExhausted", err)
	}
	if h.provider.count() != 0 {
		t.Errorf("%d LLM calls, want none", h.provider.count())
	}
	if !h.budgetBooked(monthlyBudget) {
		t.Error("the budget counter moved")
	}
}

func TestBothOnboardingModesThroughTheFacade(t *testing.T) {
	owners := []string{"org7/owners"}
	t.Run("customer mode opens exactly two pull requests", func(t *testing.T) {
		h := newHarness(t)
		r := repo(7, "org7/app", true)
		h.gh.install(7, ghr(70, r.FullName, true))
		binding := gate.Binding{Repo: r, Event: "workflow_dispatch", BaseRef: "main", WorkflowPath: workflowPath}

		for range 2 { // a retry never opens a second pull request
			if _, err := h.c.onboarding.StartCustomer(ctx, r, owners); err != nil {
				t.Fatalf("StartCustomer: %v", err)
			}
		}
		if got := len(h.gh.pulls(r.FullName)); got != 1 {
			t.Fatalf("%d pull requests after start and retry, want 1", got)
		}
		h.gh.merge(r.FullName, onboardingBranch)
		for range 2 {
			if code := h.discoveryUpload(binding); code >= 300 {
				t.Fatalf("discovery upload = %d", code)
			}
		}
		pulls := h.gh.pulls(r.FullName)
		if len(pulls) != 2 || !pulls[1].Draft {
			t.Fatalf("pull requests = %+v, want PR 1 and one draft PR 2", pulls)
		}
		if got, _ := h.gh.file(r.FullName, pulls[1].Head, policyPath); got != policyDraft {
			t.Errorf("PR 2 policy = %q, want the uploaded discovery", got)
		}
		h.gh.merge(r.FullName, pulls[1].Head)
		if got := len(h.gh.pulls(r.FullName)); got != 2 {
			t.Errorf("%d pull requests, want exactly 2", got)
		}
		if h.sandbox.count() != 0 {
			t.Error("customer mode ran a sandbox job")
		}
	})
	t.Run("managed mode without consent runs no job", func(t *testing.T) {
		h := newHarness(t)
		r := repo(7, "org7/app", true)
		h.gh.install(7, ghr(70, r.FullName, true))

		_, err := h.c.onboarding.StartManaged(ctx, r, owners)

		if !errors.Is(err, account.ErrNoConsent) {
			t.Errorf("StartManaged = %v, want ErrNoConsent", err)
		}
		if h.sandbox.count() != 0 || h.gh.writeCount() != 0 {
			t.Errorf("sandbox jobs = %d, GitHub writes = %d; want none", h.sandbox.count(), h.gh.writeCount())
		}
	})
	t.Run("managed mode puts the policy on the onboarding pull request", func(t *testing.T) {
		h := newHarness(t)
		r := repo(7, "org7/app", true)
		h.gh.install(7, ghr(70, r.FullName, true))
		h.consent(7, account.ConsentSourceProcessing)
		h.sandbox.result = DiscoveryResult{DraftPolicy: []byte(policyDraft), Notes: []string{"two modules found"}}

		if _, err := h.c.onboarding.StartManaged(ctx, r, owners); err != nil {
			t.Fatalf("StartManaged: %v", err)
		}

		if got := h.gh.awaitManagedJob(r.FullName, onboardingBranch, policyPath); got != policyDraft {
			t.Errorf("policy = %q, want the sandbox draft", got)
		}
		if got := len(h.gh.pulls(r.FullName)); got != 1 {
			t.Errorf("%d pull requests, want the onboarding pull request only", got)
		}
		if h.sandbox.count() != 1 || h.sandbox.jobs[0].Token != "read-7" {
			t.Errorf("sandbox jobs = %+v, want one job with the repository read token", h.sandbox.jobs)
		}
	})
	t.Run("managed mode without a sandbox is not available", func(t *testing.T) {
		h := newHarness(t, withoutSandbox)
		r := repo(7, "org7/app", true)
		h.gh.install(7, ghr(70, r.FullName, true))
		h.consent(7, account.ConsentSourceProcessing)

		if _, err := h.c.onboarding.StartManaged(ctx, r, owners); !errors.Is(err, ErrNoSandbox) {
			t.Errorf("StartManaged = %v, want ErrNoSandbox", err)
		}
	})
}

func TestControlFailureNeverReachesTheGate(t *testing.T) {
	h := newHarness(t)
	r := repo(7, "org7/app", true)
	h.gh.install(7, ghr(70, r.FullName, true))
	h.gh.allow("alice", r.FullName, "admin")
	h.event(webhook.InstallationAdded, 7)
	h.consent(7, account.ConsentManagedLLM)
	b := newBrowser(t, h.c.UIHandler())
	b.signIn("alice")
	lastGood := h.rawEntitlement(7)

	h.store.set(func(s *flakyStore) { s.failAll = true })
	h.gh.set(func(g *fakeGitHub) { g.down = true })
	h.provider.err = errBoom
	h.receiver.answer(gate.Upload{}, &gate.Rejection{Status: http.StatusServiceUnavailable, Code: "github_unavailable"})
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
	if rec := b.get("/r/org7/app"); rec.Code < 400 {
		t.Errorf("UI page = %d, want an error page", rec.Code)
	}
	if rec := h.upload("{}"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("discovery upload = %d, want 503", rec.Code)
	}
	if _, err := h.c.assist.Propose(ctx, assist.ModeManaged, assist.Request{
		Repo: r, Branch: "archfit/edit-alice", HeadSHA: strings.Repeat("a", 40), Instruction: "tidy", By: "alice",
	}); err == nil {
		t.Error("Propose = nil, want an error")
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
