package internal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

const day = 24 * time.Hour

// withPolicy rebinds a fixture to another policy: the report's config_hash is the policy digest.
func withPolicy(t *testing.T, f fixture, policy []byte) fixture {
	t.Helper()
	if !bytes.Contains(f.report, []byte(digest(f.policy))) {
		t.Fatalf("fixture %s does not carry its policy digest", f.name)
	}
	f.report = bytes.ReplaceAll(f.report, []byte(digest(f.policy)), []byte(digest(policy)))
	f.policy = policy
	return f
}

// assertNoNewGreen fails if a check is green after a sweep without having been exactly so before it.
func assertNoNewGreen(t *testing.T, before, after []checkRun) {
	t.Helper()
	was := map[int64]checkRun{}
	for _, c := range before {
		was[c.ID] = c
	}
	for _, c := range after {
		if c.Conclusion != "success" {
			continue
		}
		if old, ok := was[c.ID]; !ok || old.Conclusion != "success" || old.ExternalID != c.ExternalID {
			t.Errorf("check %s (%d) turned green without a fresh report", c.Name, c.ID)
		}
	}
}

func completeRun(w *world) {
	w.edit(demoRepo, func(rp *repo) { rp.runs[demoRun].Status, rp.runs[demoRun].Conclusion = "completed", "success" })
}

func TestDuplicateAndOutOfOrderDelivery(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	s := newSystem(t, w, options{})

	for i := range 2 {
		if got := s.upload(demoUpload(f)); got.Status != http.StatusOK || got.Conclusion != "success" {
			t.Fatalf("upload %d answered %+v; want 200 success", i+1, got)
		}
	}
	if n := len(w.commentsOf(demoRepo, demoPull)); n != 1 {
		t.Fatalf("the same upload twice left %d comments; want 1", n)
	}
	var gateChecks int
	for _, c := range w.checkRuns(demoRepo) {
		if c.Name == checkGate {
			gateChecks++
		}
	}
	if gateChecks != 1 {
		t.Errorf("the same upload twice left %d archfit/gate checks; want 1", gateChecks)
	}

	// The run is re-run: attempt 2 reports, then the delayed attempt 1 arrives.
	w.edit(demoRepo, func(rp *repo) { rp.runs[demoRun].Attempt = 2 })
	if got := s.upload(demoUpload(f).attempt(2)); got.Conclusion != "success" {
		t.Fatalf("attempt 2 answered %+v", got)
	}
	writes := len(w.publishingWrites())
	if got := s.upload(demoUpload(f)); got.Status != http.StatusConflict {
		t.Errorf("the older attempt answered %+v; want 409", got)
	}
	if rc := w.check(demoRepo, demoHead, checkGate).receipt(t); rc.RunAttempt != 2 {
		t.Errorf("receipt attempt = %d; the newer result must stand", rc.RunAttempt)
	}
	comments := w.commentsOf(demoRepo, demoPull)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "17654321098.2") {
		t.Errorf("comments = %d; want one, updated in place to attempt 2", len(comments))
	}

	// A new head supersedes the pull request's head: the upload for the old head is stale.
	w.edit(demoRepo, func(rp *repo) {
		rp.push("feature", strings.Repeat("9a", 20), map[string]string{"pkg/a/a.go": "package a // again\n"})
	})
	if got := s.upload(demoUpload(f).attempt(2)); got.Status != http.StatusConflict {
		t.Errorf("the upload for a superseded head answered %+v; want 409", got)
	}
	if after := len(w.publishingWrites()); after != writes {
		t.Errorf("stale uploads wrote to GitHub: %v", w.publishingWrites()[writes:])
	}
}

// A queue delivers at least once, and two worker instances can run one task at the same time.
func TestConcurrentDuplicateDelivery(t *testing.T) {
	w := newWorld(t)
	w.seedDemo(loadFixture(t, "real-healthy"), "required", true)
	s := newSystem(t, w, options{})
	events := map[string]any{
		"pull_request": pullPayload("opened", demoInstallation, demoRepo, demoRepo, true, demoPull, demoHead),
		"installation": installationPayload("created", demoInstallation),
	}
	const copies = 4
	errs := make(chan error, 2*copies*len(events))
	var wg sync.WaitGroup
	for name, payload := range events {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		e, err := webhook.Parse(name, "delivery-dup-"+name, raw)
		if err != nil {
			t.Fatal(err)
		}
		for range copies {
			wg.Go(func() { errs <- s.gate.HandleEvent(context.Background(), e) })
			wg.Go(func() { errs <- s.ctl.HandleEvent(context.Background(), e) })
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a duplicate delivery failed: %v", err)
		}
	}
	var gateChecks int
	for _, c := range w.checkRuns(demoRepo) {
		if c.Name == checkGate && c.HeadSHA == demoHead {
			gateChecks++
		}
	}
	if gateChecks != 1 {
		t.Errorf("%d concurrent copies of one delivery left %d pending archfit/gate checks; want 1", copies, gateChecks)
	}
	if _, err := s.store.Get(context.Background(), demoInstallation, gate.EntitlementKey); err != nil {
		t.Errorf("entitlement after duplicate installation events: %v", err)
	}
}

func TestSuspensionIsNeitherArchitectureFailureNorSuccess(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	s := newSystem(t, w, options{})

	if gateErr, ctlErr := s.deliver("installation", installationPayload("created", demoInstallation)); gateErr != nil || ctlErr != nil {
		t.Fatalf("installation created: gate %v, control %v", gateErr, ctlErr)
	}
	if got := s.upload(demoUpload(f)); got.Conclusion != "success" {
		t.Fatalf("during the trial the upload answered %+v; want success", got)
	}

	s.clock.add(38 * day) // trial (30 days) and grace (7 days) are over; nobody granted anything
	got := s.upload(demoUpload(f))
	if got.Status != http.StatusOK || got.Conclusion != "action_required" || got.Reason != "subscription_suspended" {
		t.Fatalf("after trial and grace the upload answered %+v; want action_required subscription_suspended", got)
	}
	c := w.check(demoRepo, demoHead, checkGate)
	if c.Conclusion != "action_required" || !strings.Contains(c.Summary, "`subscription_suspended` (subscription)") {
		t.Errorf("archfit/gate = %s; want action_required in category subscription\n%s", c.Conclusion, c.Summary)
	}
	if !strings.Contains(c.Summary, "- **Next:**") || strings.Contains(c.Summary, "- **Next:** (none)") {
		t.Errorf("the suspended check names no recovery action:\n%s", c.Summary)
	}

	if err := s.ctl.Grant(context.Background(), demoInstallation, s.clock.Now().Add(30*day)); err != nil {
		t.Fatal(err)
	}
	before := w.checkRuns(demoRepo)
	if sum := s.sweep(); sum.Rerun != 1 {
		t.Errorf("sweep after the grant: %+v; want one re-run", sum)
	}
	if reruns := w.rerunsOf(demoRepo); len(reruns) != 1 || reruns[0] != demoRun {
		t.Errorf("re-runs = %v; want the customer's workflow run", reruns)
	}
	assertNoNewGreen(t, before, w.checkRuns(demoRepo))
	if c := w.check(demoRepo, demoHead, checkGate); c.Conclusion == "success" {
		t.Error("the grant alone turned the gate green")
	}

	completeRun(w)
	if got := s.upload(demoUpload(f).attempt(2)); got.Conclusion != "success" {
		t.Fatalf("the fresh report after the grant answered %+v; want success", got)
	}
	if c := w.check(demoRepo, demoHead, checkGate); c.Conclusion != "success" {
		t.Errorf("archfit/gate = %s after the fresh report; want success", c.Conclusion)
	}
}

func TestSweepRepairsMissedEvents(t *testing.T) {
	opened := pullPayload("opened", demoInstallation, demoRepo, demoRepo, true, demoPull, demoHead)

	t.Run("dropped PR-opened webhook", func(t *testing.T) {
		w := newWorld(t)
		w.seedDemo(loadFixture(t, "real-healthy"), "required", true)
		s := newSystem(t, w, options{})
		if sum := s.sweep(); sum.Pended != 1 || sum.Errors != 0 {
			t.Errorf("summary = %+v; want one pending check", sum)
		}
		if c := w.check(demoRepo, demoHead, checkGate); c == nil || c.Status != "in_progress" || c.Conclusion != "" {
			t.Errorf("archfit/gate = %+v; want in_progress", c)
		}
	})

	t.Run("dropped upload", func(t *testing.T) {
		w := newWorld(t)
		w.seedDemo(loadFixture(t, "real-healthy"), "required", true)
		s := newSystem(t, w, options{})
		if gateErr, _ := s.deliver("pull_request", opened); gateErr != nil {
			t.Fatal(gateErr)
		}
		if sum := s.sweep(); sum.TimedOut != 0 {
			t.Errorf("summary = %+v; nothing timed out yet", sum)
		}
		s.clock.add(61 * time.Minute)
		if sum := s.sweep(); sum.TimedOut != 1 {
			t.Errorf("summary = %+v; want one timed-out upload", sum)
		}
		c := w.check(demoRepo, demoHead, checkGate)
		if c.Conclusion != "action_required" || c.receipt(t).Reason != "missing_upload" {
			t.Errorf("archfit/gate = %+v; want action_required missing_upload", c)
		}
	})

	t.Run("base policy changed", func(t *testing.T) {
		f := loadFixture(t, "real-healthy")
		w := newWorld(t)
		w.seedDemo(f, "required", true)
		s := newSystem(t, w, options{})
		if got := s.upload(demoUpload(f)); got.Conclusion != "success" {
			t.Fatalf("answer = %+v", got)
		}
		w.edit(demoRepo, func(rp *repo) {
			rp.push("main", strings.Repeat("5b", 20), map[string]string{".archfit.yaml": string(f.policy) + "# tightened\n"})
		})
		before := w.checkRuns(demoRepo)
		if sum := s.sweep(); sum.Rerun != 1 {
			t.Errorf("summary = %+v; want one re-run", sum)
		}
		assertNoNewGreen(t, before, w.checkRuns(demoRepo))
		c := w.check(demoRepo, demoHead, checkGate)
		if c.Conclusion != "action_required" || c.receipt(t).Reason != "stale_inputs" {
			t.Errorf("archfit/gate = %s %+v; want action_required stale_inputs", c.Conclusion, c.receipt(t))
		}
		if reruns := w.rerunsOf(demoRepo); len(reruns) != 1 || reruns[0] != demoRun {
			t.Errorf("re-runs = %v", reruns)
		}
	})

	t.Run("waiver expires", func(t *testing.T) {
		f := loadFixture(t, "real-waiver-active")
		f = withPolicy(t, f, bytes.ReplaceAll(f.policy, []byte("2099-12-31"), []byte("2026-09-25")))
		w := newWorld(t)
		w.seedDemo(f, "required", true)
		s := newSystem(t, w, options{})
		if got := s.upload(demoUpload(f)); got.Conclusion != "success" {
			t.Fatalf("answer = %+v", got)
		}
		if sum := s.sweep(); sum.Warned != 1 || sum.Rerun != 0 {
			t.Errorf("summary = %+v; want one waiver warning", sum)
		}
		if body := w.commentsOf(demoRepo, demoPull)[0].Body; !strings.Contains(body, "2026-09-25") {
			t.Errorf("the comment does not warn about the waiver expiry:\n%s", body)
		}
		if c := w.check(demoRepo, demoHead, checkGate); c.Conclusion != "success" {
			t.Errorf("a warning changed the gate to %s", c.Conclusion)
		}

		s.clock.add(5 * day)
		before := w.checkRuns(demoRepo)
		if sum := s.sweep(); sum.Rerun != 1 {
			t.Errorf("summary = %+v; want one re-run after the expiry", sum)
		}
		assertNoNewGreen(t, before, w.checkRuns(demoRepo))
		c := w.check(demoRepo, demoHead, checkGate)
		if c.Conclusion != "action_required" || c.receipt(t).Reason != "waiver_expired" {
			t.Errorf("archfit/gate = %s %+v; want action_required waiver_expired", c.Conclusion, c.receipt(t))
		}
		if len(w.rerunsOf(demoRepo)) != 1 {
			t.Errorf("re-runs = %v; want one", w.rerunsOf(demoRepo))
		}
	})

	t.Run("suspended then restored", func(t *testing.T) {
		f := loadFixture(t, "real-healthy")
		w := newWorld(t)
		w.seedDemo(f, "required", true)
		s := newSystem(t, w, options{})
		if _, ctlErr := s.deliver("installation", installationPayload("created", demoInstallation)); ctlErr != nil {
			t.Fatal(ctlErr)
		}
		if got := s.upload(demoUpload(f)); got.Conclusion != "success" {
			t.Fatalf("answer = %+v", got)
		}
		s.clock.add(38 * day)
		if sum := s.sweep(); sum.Suspended != 1 {
			t.Errorf("summary = %+v; want one suspended pull request", sum)
		}
		if c := w.check(demoRepo, demoHead, checkGate); c.Conclusion != "action_required" || c.receipt(t).Reason != "subscription_suspended" {
			t.Errorf("archfit/gate = %s; want action_required subscription_suspended", c.Conclusion)
		}

		if err := s.ctl.Grant(context.Background(), demoInstallation, s.clock.Now().Add(30*day)); err != nil {
			t.Fatal(err)
		}
		before := w.checkRuns(demoRepo)
		if sum := s.sweep(); sum.Rerun != 1 {
			t.Errorf("summary = %+v; want one re-run after the grant", sum)
		}
		assertNoNewGreen(t, before, w.checkRuns(demoRepo))
		if c := w.check(demoRepo, demoHead, checkGate); c.Conclusion == "success" {
			t.Error("restoration turned the gate green without a fresh report")
		}
	})
}

// enableManagedAssist makes the demo repository managed and records alice's consent to managed AI.
func enableManagedAssist(t *testing.T, s *system) *browser {
	t.Helper()
	s.w.allow("alice", demoRepo, "admin")
	s.w.edit(demoRepo, func(rp *repo) {
		for branch, id := range map[string]string{"main": demoBase, "feature": demoHead} {
			rp.push(branch, id, map[string]string{".archfit-app.yaml": settingsYAML("required", "managed")})
		}
	})
	b := newBrowser(t, s.do)
	b.signIn("alice")
	form := url.Values{"csrf": {b.csrf("/r/" + demoRepo + "/consent")}, "kind": {"managed_llm"}, "action": {"record"}}
	if resp, page := b.post("/r/"+demoRepo+"/consent", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("recording consent: %d\n%s", resp.StatusCode, page)
	}
	return b
}

// requestAssist saves an edited policy and asks the managed assistant to improve it.
func requestAssist(t *testing.T, b *browser, policy []byte) *http.Response {
	t.Helper()
	resp, _ := b.post("/r/"+demoRepo+"/assist", url.Values{
		"csrf": {b.csrf("/r/" + demoRepo + "/policy")}, "branch": {"archfit/edit-alice"}, "head_sha": {""},
		"content": {string(policy) + "# edited by alice\n"}, "mode": {"managed"}, "instruction": {"tighten the rules"},
	})
	return resp
}

func TestGateSurvivesControlPlaneFailure(t *testing.T) {
	second := pullPayload("opened", demoInstallation, demoRepo, demoRepo, true, 43, strings.Repeat("7c", 20))
	openSecondPull := func(w *world) {
		w.edit(demoRepo, func(rp *repo) {
			rp.branches["other"] = demoBase
			rp.push("other", strings.Repeat("7c", 20), map[string]string{"pkg/b/b.go": "package b\n"})
			rp.openPull(43, "other", "main")
		})
	}

	t.Run("LLM provider failing and UI failing", func(t *testing.T) {
		f := loadFixture(t, "real-healthy")
		w := newWorld(t)
		w.seedDemo(f, "required", true)
		provider := &fakeProvider{err: errDown}
		s := newSystem(t, w, options{provider: provider})
		b := enableManagedAssist(t, s)
		if resp := requestAssist(t, b, f.policy); resp.StatusCode != http.StatusServiceUnavailable || provider.count() != 1 {
			t.Fatalf("assist with a failing provider: status %d after %d provider calls; want 503 after 1", resp.StatusCode, provider.count())
		}
		// The UI cannot be made to panic from outside; it is made to fail instead: GitHub refuses every user token.
		w.fail(http.StatusInternalServerError, -1, func(r *http.Request) bool { return callerOf(r).user != "" })
		s.clock.add(2 * time.Minute) // past the session's permission cache
		if resp, _ := b.get("/r/" + demoRepo); resp.StatusCode < 500 {
			t.Fatalf("the UI answered %d while GitHub refuses user tokens; want 5xx", resp.StatusCode)
		}

		if got := s.upload(demoUpload(f)); got.Conclusion != "success" {
			t.Errorf("upload answered %+v; want success", got)
		}
		openSecondPull(w)
		if gateErr, _ := s.deliver("pull_request", second); gateErr != nil {
			t.Errorf("gate.HandleEvent: %v", gateErr)
		}
		if c := w.check(demoRepo, strings.Repeat("7c", 20), checkGate); c == nil || c.Status != "in_progress" {
			t.Errorf("pending check = %+v; want in_progress", c)
		}
	})

	t.Run("Store failing and control sweep failing", func(t *testing.T) {
		f := loadFixture(t, "real-healthy")
		w := newWorld(t)
		w.seedDemo(f, "required", true) // private: a suspended tenant would show
		s := newSystem(t, w, options{store: failingStore{errDown}})

		if _, ctlErr := s.deliver("installation", installationPayload("created", demoInstallation)); ctlErr == nil {
			t.Fatal("control handled an installation event without a Store; the failure is not being exercised")
		}
		// An unreadable entitlement alone means "not suspended", but without the Store no publication lease can be
		// taken: the gate fails closed and writes nothing rather than publish unserialized.
		if got := s.upload(demoUpload(f)); got.Status != http.StatusServiceUnavailable || got.Conclusion != "" {
			t.Errorf("upload answered %+v; without a publication lease want 503 and no conclusion", got)
		}
		openSecondPull(w)
		if gateErr, _ := s.deliver("pull_request", second); gateErr == nil {
			t.Error("gate.HandleEvent created the pending check without a publication lease")
		}

		// The control sweep fails on its first GitHub call; the gate sweep runs right after it, as in app.
		w.fail(http.StatusInternalServerError, 1, func(r *http.Request) bool { return r.URL.Path == "/app/installations" })
		if err := s.ctl.Sweep(context.Background()); err == nil {
			t.Fatal("the control sweep succeeded; the failure is not being exercised")
		}
		if sum := s.sweep(); sum.Errors != 2 || sum.Pulls != 2 {
			t.Errorf("gate sweep summary = %+v; want both pull requests visited, each an error", sum)
		}
		for _, head := range []string{demoHead, strings.Repeat("7c", 20)} {
			if c := w.check(demoRepo, head, checkGate); c != nil {
				t.Errorf("archfit/gate on %s = %s/%s; want no check written without the Store", head, c.Status, c.Conclusion)
			}
		}
	})
}
