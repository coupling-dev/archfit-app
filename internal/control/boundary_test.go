package control

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

// account is the login GitHub reports for the installation: the owner of its first repository.
func (h *harness) account(id int64) string {
	var owner string
	h.gh.set(func(g *fakeGitHub) {
		if repos := g.repos[id]; len(repos) > 0 {
			owner, _, _ = strings.Cut(repos[0].FullName, "/")
		}
	})
	return owner
}

func (h *harness) event(kind webhook.EventKind, id int64) {
	h.t.Helper()
	if err := h.c.HandleEvent(ctx, webhook.Event{Kind: kind, InstallationID: id, Account: h.account(id), DeliveryID: "delivery"}); err != nil {
		h.t.Fatalf("HandleEvent(%s, %d): %v", kind, id, err)
	}
}

func (h *harness) consent(id int64, kinds ...account.ConsentKind) {
	h.t.Helper()
	for _, k := range kinds {
		if err := h.c.consents.Record(ctx, id, account.Consent{Kind: k, By: "alice", ByID: 1, At: h.clock.Now()}); err != nil {
			h.t.Fatalf("Record(%s): %v", k, err)
		}
	}
}

func TestUninstallLeavesNoTenantKeys(t *testing.T) {
	h := newHarness(t)
	for _, id := range []int64{7, 8} {
		h.gh.install(id, ghr(id*10, "org/app", true))
		h.event(webhook.InstallationAdded, id)
		h.consent(id, account.ConsentSourceProcessing)
	}
	other, _ := h.entitlement(8)

	h.event(webhook.InstallationRemoved, 7)
	requests, writes := h.gh.requestCount(), h.store.writes
	h.event(webhook.InstallationRemoved, 7)

	if got := h.tenants(); got[7] || !got[8] {
		t.Errorf("tenants = %v, want only 8", got)
	}
	if h.c.consents.Has(ctx, 7, account.ConsentSourceProcessing) {
		t.Error("consent of the removed tenant survived")
	}
	if after, _ := h.entitlement(8); after != other || !h.c.consents.Has(ctx, 8, account.ConsentSourceProcessing) {
		t.Error("the other tenant changed")
	}
	if h.gh.requestCount() != requests || h.store.writes != writes {
		t.Error("the second delivery called GitHub or wrote a key")
	}
}

func TestOrphanCleanupFailsSafe(t *testing.T) {
	tests := []struct {
		name        string
		repos       *fakeRepos
		fail        func(*flakyStore)
		wantErr     error
		wantTenants []store.Tenant
		wantRefresh bool
	}{
		{name: "ListInstallations is rate limited", repos: &fakeRepos{installErr: api.ErrRateLimited},
			wantErr: api.ErrRateLimited, wantTenants: []store.Tenant{1, 2, 3}},
		{name: "ListInstallations fails", repos: &fakeRepos{installErr: errBoom},
			wantErr: errBoom, wantTenants: []store.Tenant{1, 2, 3}},
		{name: "empty installation list", repos: &fakeRepos{}, wantTenants: []store.Tenant{1, 2, 3}},
		{name: "Tenants fails", repos: &fakeRepos{installations: installations(1)},
			fail: func(s *flakyStore) { s.failList = true }, wantTenants: []store.Tenant{1, 2, 3}, wantRefresh: true},
		{name: "Forget fails for one orphan", repos: &fakeRepos{installations: installations(1)},
			fail:        func(s *flakyStore) { s.failDelete = map[store.Tenant]bool{2: true} },
			wantTenants: []store.Tenant{1, 2}, wantRefresh: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &flakyStore{Store: store.NewMemory()}
			acct, _ := account.New(st)
			for _, id := range []int64{1, 2, 3} {
				if err := acct.Observe(ctx, id, true, t0); err != nil {
					t.Fatalf("Observe: %v", err)
				}
			}
			if tt.fail != nil {
				tt.fail(st)
			}
			writes := st.writes
			c := newControl(acct, tt.repos, st, admitAll{}, okHandler, newClock().Now)

			err := c.Sweep(ctx)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Sweep = %v, want %v", err, tt.wantErr)
			}
			for _, id := range tt.wantTenants {
				if _, err := st.Store.Get(ctx, id, gate.EntitlementKey); err != nil {
					t.Errorf("tenant %d lost its keys: %v", id, err)
				}
			}
			if got, _ := st.Store.Tenants(ctx); len(got) != len(tt.wantTenants) {
				t.Errorf("tenants = %v, want %v", got, tt.wantTenants)
			}
			if refreshed := st.writes > writes; refreshed != tt.wantRefresh {
				t.Errorf("refresh ran = %t, want %t", refreshed, tt.wantRefresh)
			}
		})
	}
}

func TestRevokedAccessSchedulesNothing(t *testing.T) {
	h := newHarness(t)
	h.gh.install(7, ghr(70, "org7/app", true))
	h.gh.install(8, ghr(80, "org8/app", true))
	h.gh.allow("alice", "org7/app", "admin")
	h.event(webhook.InstallationAdded, 7)
	h.event(webhook.InstallationAdded, 8)
	h.consent(7, account.ConsentSourceProcessing)
	b := newBrowser(t, h.c.UIHandler())
	b.signIn("alice")
	page := b.get("/r/org7/app")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /r/org7/app before the uninstall = %d", page.Code)
	}
	form := hiddenFields(page.Body.String())

	h.event(webhook.InstallationRemoved, 7)
	h.gh.uninstall(7) // GitHub answers 404 for the installation from now on
	h.gh.set(func(g *fakeGitHub) { g.installTokens = nil })
	writes := h.store.writes

	if err := h.c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if h.gh.usedTokenOf(7) {
		t.Error("the sweep called GitHub for the removed installation")
	}
	// The session child caches an authorization briefly; past that, GitHub's 404 decides.
	h.clock.Add(time.Hour)
	if rec := b.get("/api/repos/org7/app/overview"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/repos/org7/app/overview = %d, want 404", rec.Code)
	}
	form.Set("mode", "managed")
	form.Set("owners", "org7/owners")

	if h.tenants()[7] {
		t.Error("a key was written for the removed tenant")
	}
	if n := h.gh.writeCount(); n != 0 {
		t.Errorf("%d GitHub writes, want none", n)
	}
	// Tenant 8 is refreshed by the sweep; nothing else may write.
	if got := h.store.writes - writes; got > 2 {
		t.Errorf("%d Store writes, want only the refresh of installation 8", got)
	}
}

func TestReplayedAndForgedInstallationIDs(t *testing.T) {
	h := newHarness(t)
	h.gh.install(7, ghr(70, "org7/app", true))
	h.gh.install(8, ghr(80, "org8/app", true))
	h.gh.allow("alice", "org7/app", "admin")

	t.Run("replays keep one trial start", func(t *testing.T) {
		for range 10 {
			h.event(webhook.InstallationAdded, 7)
			h.clock.Add(time.Hour)
		}
		s, err := h.status.Status(ctx, 7, h.clock.Now())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !s.TrialEnds.Equal(t0.Add(30 * day)) {
			t.Errorf("TrialEnds = %v, want %v", s.TrialEnds, t0.Add(30*day))
		}
	})
	t.Run("UI write with a foreign installation ID", func(t *testing.T) {
		b := newBrowser(t, h.c.UIHandler())
		b.signIn("alice")
		allow := map[string]string{"kind": string(account.ConsentSourceProcessing), "action": "allow"}
		forged := map[string]string{"kind": string(account.ConsentSourceProcessing), "action": "allow", "installation_id": "8"}

		if rec := b.postJSON("/api/repos/org7/app/data-sharing", forged); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("a body naming installation 8 = %d, want 422", rec.Code)
		}
		if rec := b.postJSON("/api/repos/org7/app/data-sharing?installation_id=8&installation=8", allow); rec.Code != http.StatusOK {
			t.Fatalf("POST /api/repos/org7/app/data-sharing = %d, want 200", rec.Code)
		}
		if rec := b.postJSON("/api/repos/org8/app/data-sharing", allow); rec.Code != http.StatusNotFound {
			t.Errorf("POST /api/repos/org8/app/data-sharing = %d, want 404", rec.Code)
		}
		if !h.c.consents.Has(ctx, 7, account.ConsentSourceProcessing) {
			t.Error("consent was not recorded for the session's tenant")
		}
		if h.c.consents.Has(ctx, 8, account.ConsentSourceProcessing) || h.tenants()[8] {
			t.Error("the foreign installation ID was used")
		}
	})
}

type panickingOAuth struct{ auth.OAuth }

func (panickingOAuth) AuthorizeURL(string, string) string { panic("oauth exploded") }

func TestUIPanicIsContained(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t, func(d *Deps) { d.OAuth = panickingOAuth{} })
	h.gh.install(7, ghr(70, "org7/app", true))

	rec := httptest.NewRecorder()
	h.c.UIHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login?return=/r/secret-org/secret-repo", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if out := logs.String(); strings.Contains(out, "secret-") {
		t.Errorf("log must name the route pattern only:\n%s", out)
	}
	rec = httptest.NewRecorder()
	h.c.UIHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET / after the panic = %d, want 200", rec.Code)
	}
	h.event(webhook.InstallationAdded, 7)
	if err := h.c.Sweep(ctx); err != nil {
		t.Errorf("Sweep after the panic: %v", err)
	}

	t.Run("the façade recovers what the UI does not", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /r/{o}/{n}", func(http.ResponseWriter, *http.Request) { panic("page exploded") })
		c := newControl(&fakeAccount{}, &fakeRepos{}, &fakeTenants{}, admitAll{}, mux, newClock().Now)
		rec := httptest.NewRecorder()

		c.UIHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/r/hidden-org/hidden-repo", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		out := logs.String()
		if !strings.Contains(out, "GET /r/{o}/{n}") || strings.Contains(out, "hidden-") || strings.Contains(out, "page exploded") {
			t.Errorf("log must name the route pattern only:\n%s", out)
		}
	})
	t.Run("an aborted response is not swallowed", func(t *testing.T) {
		c := newControl(&fakeAccount{}, &fakeRepos{}, &fakeTenants{}, admitAll{},
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }), newClock().Now)
		defer func() {
			if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // the sentinel is panicked by identity
				t.Errorf("recovered %v, want http.ErrAbortHandler", v)
			}
		}()
		c.UIHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestControlImportsOnlyAllowedPackages(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	const module = "github.com/alexei-led/archfit-app/internal/"
	list := func(args ...string) []string {
		var stderr bytes.Buffer
		cmd := exec.Command(goTool, append([]string{"list", "-f", `{{.ImportPath}} {{join .Imports " "}}`}, args...)...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, stderr.String())
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}
	// Direct imports of control and its children: the gate façade only, never app.
	for _, line := range list("./...") {
		pkg, imports, _ := strings.Cut(line, " ")
		for _, imp := range strings.Fields(imports) {
			if strings.HasPrefix(imp, module+"gate/") || imp == module+"app" || strings.HasPrefix(imp, module+"app/") {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
	// The façade reaches no grandchild and no platform implementation.
	for _, imp := range strings.Fields(list(".")[0])[1:] {
		if rest, ok := strings.CutPrefix(imp, module+"control/"); ok && strings.Contains(rest, "/") {
			t.Errorf("the façade imports %s", imp)
		}
	}
	// The YAML library is reached only through wire/*.
	for _, line := range list("-deps", "./...") {
		pkg, imports, _ := strings.Cut(line, " ")
		if strings.Contains(imports, "go.yaml.in/") && !strings.HasPrefix(pkg, module+"wire/") && !strings.HasPrefix(pkg, "go.yaml.in/") {
			t.Errorf("%s imports the YAML library directly", pkg)
		}
	}
}
