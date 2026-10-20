package internal_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	shopInstallation = 7
	shopRepo         = "acme/shop"
	shopRepoID       = 7001
	leakMarker       = "LEAK-7f3a"
)

func (w *world) seedShop() {
	w.install(shopInstallation)
	w.addRepo(shopInstallation, shopRepoID, shopRepo, true)
	w.allow("alice", shopRepo, "admin")
}

func onboard(t *testing.T, b *browser, mode string) (*http.Response, string) {
	t.Helper()
	return b.postJSON("/api/repos/"+shopRepo+"/onboarding", map[string]string{"owners": owner, "mode": mode})
}

// recordConsent allows one managed feature on the Data sharing page; query is appended to the API path as is.
func recordConsent(t *testing.T, b *browser, repo, kind, query string) {
	t.Helper()
	body := map[string]string{"kind": kind, "action": "allow"}
	if resp, page := b.postJSON("/api/repos/"+repo+"/data-sharing"+query, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("allowing %s: %d\n%s", kind, resp.StatusCode, page)
	}
}

func TestBothOnboardingModesComplete(t *testing.T) {
	draft := loadFixture(t, "real-attention-no-baseline").policy

	t.Run("customer mode", func(t *testing.T) {
		w := newWorld(t)
		w.seedShop()
		provider := &fakeProvider{}
		s := newSystem(t, w, options{provider: provider})
		b := newBrowser(t, s.do)
		b.signIn("alice")

		for range 2 { // the second submit is a retry
			if resp, page := onboard(t, b, "customer"); resp.StatusCode != http.StatusOK {
				t.Fatalf("start onboarding: %d\n%s", resp.StatusCode, page)
			}
		}
		pulls := w.pullsOf(shopRepo)
		if len(pulls) != 1 || pulls[0].HeadRef != "archfit/onboarding" || !pulls[0].Draft {
			t.Fatalf("after PR 1 and a retry: %+v; want one draft from archfit/onboarding", pulls)
		}
		for _, path := range []string{workflowPath, ".archfit-app.yaml", ".github/CODEOWNERS"} {
			if _, ok := w.file(shopRepo, "archfit/onboarding", path); !ok {
				t.Errorf("PR 1 lacks %s", path)
			}
		}
		w.edit(shopRepo, func(rp *repo) { rp.merge(t, "archfit/onboarding") })

		if resp, page := b.postJSON("/api/repos/"+shopRepo+"/discover", struct{}{}); resp.StatusCode != http.StatusOK {
			t.Fatalf("dispatch discovery: %d\n%s", resp.StatusCode, page)
		}
		if d := w.dispatchesOf(shopRepo); len(d) != 1 || d[0].Ref != "main" || d[0].Inputs["mode"] != "discovery" {
			t.Fatalf("dispatches = %+v; want the archfit workflow on main with mode discovery", d)
		}

		head := discoveryRun(w, shopRepo, 555)
		for range 2 { // the producer retries
			got := readAnswer(t, s.do(discoveryUpload(shopRepo, shopRepoID, 555, head, draft).request(t, "/v1/discoveries")))
			if got.Status != http.StatusOK {
				t.Fatalf("discovery upload answered %+v; want 200", got)
			}
		}
		pulls = w.pullsOf(shopRepo)
		if len(pulls) != 2 || pulls[1].HeadRef != "archfit/policy" || pulls[1].State != "open" {
			t.Fatalf("after the discovery upload and a retry: %+v; want PR 2 from archfit/policy", pulls)
		}
		if got, _ := w.file(shopRepo, "archfit/policy", ".archfit.yaml"); got != string(draft) {
			t.Error("PR 2 does not carry the uploaded draft policy byte for byte")
		}
		if checks := w.checkRuns(shopRepo); len(checks) != 0 {
			t.Errorf("a discovery upload produced checks: %+v; discovery output never satisfies the gate", checks)
		}
		if provider.count() != 0 {
			t.Error("customer onboarding called the LLM provider")
		}
	})

	t.Run("managed mode without consent", func(t *testing.T) {
		w := newWorld(t)
		w.seedShop()
		sandbox, provider := &fakeSandbox{draft: draft}, &fakeProvider{}
		s := newSystem(t, w, options{sandbox: sandbox, provider: provider})
		b := newBrowser(t, s.do)
		b.signIn("alice")

		resp, page := onboard(t, b, "managed")
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, `"location":"/r/`+shopRepo+`/data-sharing"`) {
			t.Fatalf("managed onboarding without consent: %d %s; want Data sharing as the next location", resp.StatusCode, page)
		}
		if sandbox.count() != 0 || provider.count() != 0 || len(w.pullsOf(shopRepo)) != 0 {
			t.Errorf("without consent: %d sandbox jobs, %d LLM calls, %d pull requests; want none",
				sandbox.count(), provider.count(), len(w.pullsOf(shopRepo)))
		}
	})

	t.Run("managed mode with consent", func(t *testing.T) {
		w := newWorld(t)
		w.seedShop()
		sandbox, provider := &fakeSandbox{draft: draft}, &fakeProvider{}
		s := newSystem(t, w, options{sandbox: sandbox, provider: provider})
		b := newBrowser(t, s.do)
		b.signIn("alice")
		recordConsent(t, b, shopRepo, "source_processing", "")

		if resp, page := onboard(t, b, "managed"); resp.StatusCode != http.StatusOK {
			t.Fatalf("managed onboarding: %d\n%s", resp.StatusCode, page)
		}
		w.awaitWrites("the managed discovery job", func() bool {
			_, committed := w.file(shopRepo, "archfit/onboarding", ".archfit.yaml")
			pulls := w.pullsOf(shopRepo)
			return committed && len(pulls) == 1 && strings.Contains(pulls[0].Body, "state=complete")
		})
		if got, _ := w.file(shopRepo, "archfit/onboarding", ".archfit.yaml"); got != string(draft) {
			t.Error("the onboarding PR does not carry the sandbox's draft policy")
		}
		if n := len(w.pullsOf(shopRepo)); n != 1 {
			t.Errorf("%d pull requests; managed onboarding uses exactly one", n)
		}
		if sandbox.count() != 1 || sandbox.jobs[0].Token != fmt.Sprintf("read-%d", shopInstallation) {
			t.Errorf("sandbox jobs = %+v; want one with a contents:read token", sandbox.jobs)
		}
		if provider.count() != 0 {
			t.Error("managed discovery called the LLM provider; no LLM runs in the sandbox flow")
		}
	})

	t.Run("managed mode with no sandbox configured", func(t *testing.T) {
		w := newWorld(t)
		w.seedShop()
		s := newSystem(t, w, options{})
		b := newBrowser(t, s.do)
		b.signIn("alice")
		recordConsent(t, b, shopRepo, "source_processing", "")

		resp, page := onboard(t, b, "managed")
		if resp.StatusCode != http.StatusConflict || !strings.Contains(page, "not available") {
			t.Errorf("managed onboarding without a sandbox: %d; want 409 saying it is not available\n%s", resp.StatusCode, page)
		}
		if n := len(w.pullsOf(shopRepo)); n != 0 {
			t.Errorf("%d pull requests opened without a sandbox", n)
		}
	})
}

func leakageFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("wire", "testdata", "leakage", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, p := range paths {
		if strings.HasSuffix(p, ".expect.json") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), leakMarker) {
			t.Fatalf("%s carries no %s", p, leakMarker)
		}
		out[strings.TrimSuffix(filepath.Base(p), ".json")] = raw
	}
	if len(out) < 5 {
		t.Fatalf("found %d leakage fixtures", len(out))
	}
	return out
}

// discoveryRun registers a workflow_dispatch run on main and returns the head it ran on.
func discoveryRun(w *world, fullName string, runID int64) string {
	var head string
	w.edit(fullName, func(rp *repo) {
		head = rp.branches["main"]
		rp.runs[runID] = &run{ID: runID, Attempt: 1, Event: "workflow_dispatch", HeadSHA: head, HeadRepo: fullName, Path: workflowPath, Branch: "main", Status: "in_progress"}
	})
	return head
}

func TestNoSourceLeavesThePayloadBoundary(t *testing.T) {
	base := loadFixture(t, "real-blocked-no-baseline") // the leakage fixtures were edited from it
	for name, raw := range leakageFixtures(t) {
		t.Run(name, func(t *testing.T) {
			logs, _ := captureLogs(t)
			w := newWorld(t)
			w.seedDemo(base, "required", true)
			s := newSystem(t, w, options{})

			u := demoUpload(base)
			u.body, u.env.PayloadDigest = raw, "sha256:"+digest(raw)
			got := s.upload(u)
			if name == "declarations-dropped" {
				// The one allowed shape: the decoder drops the field and the report is still usable.
				if got.Conclusion != "failure" {
					t.Errorf("answer = %+v; want the blocked report decided normally", got)
				}
			} else if got.Status != http.StatusOK || got.Conclusion != "action_required" || got.Reason != "report_invalid" {
				t.Errorf("as a report: answer = %+v; want action_required report_invalid", got)
			}
			if c := w.check(demoRepo, demoHead, checkGate); c == nil || c.Conclusion == "success" {
				t.Errorf("archfit/gate = %+v", c)
			}

			// The same bytes as a discovery result: whatever the answer, nothing the App writes about
			// the upload (checks, comments, pull request text, logs) may carry the seeded string.
			head := discoveryRun(w, demoRepo, 555)
			drain(s.do(discoveryUpload(demoRepo, demoRepoID, 555, head, raw).request(t, "/v1/discoveries")))

			if strings.Contains(w.everythingPublished(), leakMarker) {
				t.Errorf("%s reached a check, a comment or pull request text", leakMarker)
			}
			if strings.Contains(logs.String(), leakMarker) {
				t.Errorf("%s reached a log line", leakMarker)
			}
			if !strings.Contains(logs.String(), "gate upload") {
				t.Error("the upload was not logged; the log assertion checked nothing")
			}
		})
	}

	t.Run("as discovery results", func(t *testing.T) {
		for name, raw := range leakageFixtures(t) {
			w := newWorld(t)
			w.seedDemo(base, "required", true)
			s := newSystem(t, w, options{})
			head := discoveryRun(w, demoRepo, 555)
			before := len(w.pullsOf(demoRepo))
			got := readAnswer(t, s.do(discoveryUpload(demoRepo, demoRepoID, 555, head, raw).request(t, "/v1/discoveries")))
			if got.Status != http.StatusBadRequest {
				t.Errorf("%s: answer = %+v; want 400: a source-bearing payload must not decode", name, got)
			}
			if content, ok := w.file(demoRepo, "archfit/policy", ".archfit.yaml"); len(w.pullsOf(demoRepo)) != before || ok && content == string(raw) {
				t.Errorf("%s: the report bytes, %s included, were committed as the proposed policy", name, leakMarker)
			}
		}
	})
}

func TestLogsCarryOnlyIdentifiers(t *testing.T) {
	const titleMarker = "MARKER-pull-request-title"
	f := loadFixture(t, "real-blocked-no-baseline")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.allow("alice", demoRepo, "admin")
	a := startApp(t, w)

	u := demoUpload(f)
	request := u.request(t, "/v1/reports")
	if got := readAnswer(t, a.do(request)); got.Conclusion != "failure" {
		t.Fatalf("upload answered %+v", got)
	}
	opened := pullPayload("opened", demoInstallation, demoRepo, demoRepo, true, demoPull, demoHead)
	opened["pull_request"].(map[string]any)["title"] = titleMarker
	if status := drain(a.webhook("pull_request", opened)); status != http.StatusAccepted {
		t.Fatalf("webhook: %d", status)
	}
	if status := drain(a.webhook("installation", installationPayload("created", demoInstallation))); status != http.StatusAccepted {
		t.Fatalf("webhook: %d", status)
	}
	if status := drain(a.operator("/internal/sweep", "")); status != http.StatusOK {
		t.Fatalf("sweep: %d", status)
	}
	b := newBrowser(t, a.do)
	b.signIn("alice")
	pages := []string{"/", "/api/repos", "/r/" + demoRepo, "/api/repos/" + demoRepo + "/overview", "/r/" + demoRepo + "/policy", "/r/" + demoRepo + "/data-sharing", "/api/repos/" + demoRepo + "/data-sharing"}
	for _, page := range pages {
		if resp, _ := b.get(page); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d", page, resp.StatusCode)
		}
	}

	logs := a.logs.String()
	for _, want := range []string{`"msg":"request"`, digest(f.report), `"conclusion":"failure"`, fmt.Sprint(demoInstallation), `"duration_ms"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %s", want)
		}
	}
	facts := factsOf(t, f)
	markers := []string{
		titleMarker, webhookSecret, operatorToken, oauthSecret, cookieKey,
		strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "), request.Header.Get(envelopeHeader),
		"user-alice", "tok-42", b.cookies["archfit_session"],
		"explicitly forbidden", "Remove the dependency", "archfit check", "forbidden_imports", "refs/pull",
	}
	for _, fd := range facts.Findings {
		for _, loc := range fd.Locations {
			markers = append(markers, loc.File)
		}
	}
	for _, m := range markers {
		if m != "" && strings.Contains(logs, m) {
			t.Errorf("logs contain %q", m)
		}
	}
}

func TestClientSuppliedInstallationIDIsIgnored(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.seedShop() // the victim: installation 7, alice has no business there in this test
	w.allow("alice", shopRepo, "")
	w.allow("alice", demoRepo, "admin")
	st := newRecordingStore()
	s := newSystem(t, w, options{store: st})
	victim := fmt.Sprint(shopInstallation)

	// Upload: header and query name the victim; an envelope field cannot even be expressed.
	r := demoUpload(f).request(t, "/v1/reports?installation_id="+victim+"&installation="+victim)
	r.Header.Set("X-Installation-Id", victim)
	r.Header.Set("X-GitHub-Installation-Id", victim)
	if got := readAnswer(t, s.do(r)); got.Conclusion != "success" {
		t.Fatalf("upload answered %+v", got)
	}
	u := demoUpload(f)
	u.rawEnvelope = `{"installation_id":` + victim + `,` + strings.TrimPrefix(string(mustJSON(t, u.env)), "{")
	if got := s.upload(u); got.Status != http.StatusBadRequest {
		t.Errorf("an envelope carrying installation_id answered %+v; want 400", got)
	}

	// UI: the body and the query name the victim.
	b := newBrowser(t, s.do)
	b.signIn("alice")
	forged := map[string]string{"kind": "source_processing", "action": "allow", "installation_id": victim}
	if resp, _ := b.postJSON("/api/repos/"+demoRepo+"/data-sharing", forged); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a body naming an installation answered %d; want 422", resp.StatusCode)
	}
	recordConsent(t, b, demoRepo, "source_processing", "?installation_id="+victim+"&installation="+victim)
	if resp, _ := b.get("/r/" + demoRepo + "?installation_id=" + victim); resp.StatusCode != http.StatusOK {
		t.Fatalf("repository page: %d", resp.StatusCode)
	}
	if resp, _ := b.get("/r/" + shopRepo + "?installation_id=" + fmt.Sprint(demoInstallation)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a repository of another tenant answered %d; want 404", resp.StatusCode)
	}

	// Webhook: the verified payload names installation 42; nothing else in it can redirect the event.
	opened := pullPayload("opened", demoInstallation, demoRepo, demoRepo, true, demoPull, demoHead)
	opened["installation_id"] = shopInstallation
	if gateErr, ctlErr := s.deliver("pull_request", opened); gateErr != nil || ctlErr != nil {
		t.Fatalf("webhook: gate %v, control %v", gateErr, ctlErr)
	}

	if keys := st.written(store.Tenant(shopInstallation)); len(keys) != 0 {
		t.Errorf("keys written under the victim tenant: %v", keys)
	}
	if keys := st.written(store.Tenant(demoInstallation)); !slices.Contains(keys, "consent/source_processing") {
		t.Errorf("the consent went to %v; want it under the session's tenant", keys)
	}
	if cross := w.crossTenantRequests(); len(cross) != 0 {
		t.Errorf("requests crossed tenants: %v", cross)
	}
	w.locked(func() {
		for _, line := range w.requests {
			if strings.Contains(line, "/app/installations/"+victim+"/") { // no token was minted for the victim
				t.Errorf("the victim tenant was touched: %s", line)
			}
		}
	})
	if checks := w.checkRuns(shopRepo); len(checks) != 0 {
		t.Errorf("checks on the victim repository: %+v", checks)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestUninstallLeavesNoKeys(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.seedShop()
	st := newRecordingStore()
	provider := &fakeProvider{text: string(f.policy) + "# proposed by the assistant\n"}
	s := newSystem(t, w, options{store: st, provider: provider})
	ctx := context.Background()

	for _, id := range []int64{demoInstallation, shopInstallation} {
		if _, ctlErr := s.deliver("installation", installationPayload("created", id)); ctlErr != nil {
			t.Fatal(ctlErr)
		}
	}
	if err := s.ctl.Grant(ctx, demoInstallation, s.clock.Now().Add(90*day)); err != nil {
		t.Fatal(err)
	}
	b := enableManagedAssist(t, s) // records managed_llm consent
	recordConsent(t, b, demoRepo, "source_processing", "")
	if resp := requestAssist(t, b, f.policy); resp.StatusCode != http.StatusSeeOther || provider.count() != 1 {
		t.Fatalf("assist: status %d after %d provider calls; want 303 after 1", resp.StatusCode, provider.count())
	}

	keys := st.written(store.Tenant(demoInstallation))
	for _, want := range []string{"account", "entitlement", "consent/source_processing", "consent/managed_llm"} {
		if !slices.Contains(keys, want) {
			t.Fatalf("the tenant wrote %v; the scenario lacks %s", keys, want)
		}
	}
	budgetKeys := st.written(store.SystemTenant)
	if len(budgetKeys) != 1 {
		t.Fatalf("system tenant keys = %v; want the budget counter", budgetKeys)
	}
	spent, err := st.Add(ctx, store.SystemTenant, budgetKeys[0], 0)
	if err != nil || spent <= 0 {
		t.Fatalf("budget counter = %d, %v; want the cost of one call", spent, err)
	}

	w.uninstall(demoInstallation)
	if _, ctlErr := s.deliver("installation", installationPayload("deleted", demoInstallation)); ctlErr != nil {
		t.Fatal(ctlErr)
	}

	for _, key := range keys {
		if _, err := st.Get(ctx, store.Tenant(demoInstallation), key); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("key %q survived the uninstall (%v)", key, err)
		}
	}
	tenants, err := st.Tenants(ctx)
	if err != nil || slices.Contains(tenants, store.Tenant(demoInstallation)) || !slices.Contains(tenants, store.Tenant(shopInstallation)) {
		t.Errorf("tenants = %v, %v; want the uninstalled one gone and the other kept", tenants, err)
	}
	if after, err := st.Add(ctx, store.SystemTenant, budgetKeys[0], 0); err != nil || after != spent {
		t.Errorf("system budget counter = %d, %v; want %d unchanged", after, err, spent)
	}

	// The session outlives the installation; the tenant is gone for it too.
	s.clock.add(2 * time.Minute)
	if resp, _ := b.get("/r/" + demoRepo); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the repository page answered %d after the uninstall; want 404", resp.StatusCode)
	}
}
