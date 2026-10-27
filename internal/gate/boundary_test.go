package gate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func fixtureNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "report", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no report fixtures: %v", err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = strings.TrimSuffix(filepath.Base(f), ".json")
	}
	return names
}

func TestNoNeutralNoSkippedEver(t *testing.T) {
	var writes []api.CheckRun
	// run executes one case in a fresh required-mode world with a pending check, then keeps its gate writes.
	run := func(name string, do func(w *world, p *pull)) {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, p))
			do(w, p)
			for _, c := range w.gh.checkWrites {
				if c.Name == gateCheckName {
					writes = append(writes, c)
				}
			}
		})
	}

	for _, name := range fixtureNames(t) {
		run("fixture "+name, func(w *world, p *pull) {
			if a := w.upload(p, name); a.status != 200 {
				t.Errorf("answer = %d %s", a.status, a.raw)
			}
		})
	}
	for _, row := range negativeRows() {
		run("negative "+row.name, func(w *world, p *pull) {
			s := w.spec(p, w.report(w.shop, fixtureHealthy))
			row.damage(w, p, &s)
			w.send(s)
		})
	}
	for _, row := range problemRows() {
		run("problem "+row.name, func(w *world, p *pull) { w.send(row.spec(w, p)) })
	}
	run("transient failure", func(w *world, p *pull) {
		w.gh.fail["UpdateCheckRun"] = api.ErrRateLimited
		w.upload(p, fixtureHealthy)
	})
	run("fork refusal by webhook", func(w *world, p *pull) {
		w.push(p)
		w.editPull(p, func(pl *api.Pull) { pl.HeadRepo = "mallory/shop" })
		w.handle(w.event(webhook.PullSynchronized, p))
	})
	run("suspended", func(w *world, p *pull) {
		vault := w.addRepo("acme/vault", 12, 201, true)
		w.entitle(12, Entitlement{SuspendAfter: w.now.Add(-time.Hour)})
		w.upload(w.openPull(vault, 3), fixtureHealthy)
	})
	run("sweep: missing upload, stale inputs", func(w *world, p *pull) {
		fresh := w.openPull(w.shop, 8)
		w.upload(fresh, fixtureHealthy)
		w.setFile(w.shop, testBranch, policy.PolicyPath, []byte(basePolicy+"# changed\n"))
		w.now = w.now.Add(2 * time.Hour)
		if _, err := w.gate.Sweep(context.Background()); err != nil {
			t.Error(err)
		}
	})

	if len(writes) < 20 {
		t.Fatalf("only %d %s writes were collected", len(writes), gateCheckName)
	}
	for _, c := range writes {
		switch {
		case c.Status == runInProgress && c.Conclusion == "":
		case c.Status == runCompleted && (c.Conclusion == "success" || c.Conclusion == "failure" || c.Conclusion == "action_required"):
		default:
			t.Errorf("%s written as status %q conclusion %q", gateCheckName, c.Status, c.Conclusion)
		}
	}
}

func TestTransientFailuresLeavePending(t *testing.T) {
	plain := errors.New("github: 502 bad gateway")
	tests := []struct {
		name       string
		method     string
		err        error
		code       string
		retryAfter string
	}{
		{"5xx during Resolve", "File", plain, "unavailable", ""},
		{"rate limit during Resolve", "File", api.ErrRateLimited, "rate_limited", "60"},
		{"5xx while reading reviews", "ChangedFiles", plain, "unavailable", ""},
		{"5xx during Publish", "UpdateCheckRun", plain, "unavailable", ""},
		{"rate limit during Publish", "UpdateCheckRun", api.ErrRateLimited, "rate_limited", "60"},
		{"rate limit while reading the check", "CheckRuns", api.ErrRateLimited, "rate_limited", "60"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, p))
			pending := w.wantGate(p, runInProgress, "")
			s := w.spec(p, w.report(w.shop, fixtureHealthy))

			w.gh.fail[tc.method] = tc.err
			a := w.send(s)
			if a.status != 503 || a.Error != tc.code || a.retryAfter != tc.retryAfter || a.Conclusion != "" {
				t.Errorf("answer = %d %s Retry-After=%q, want 503 %s Retry-After=%q", a.status, a.raw, a.retryAfter, tc.code, tc.retryAfter)
			}
			delete(w.gh.fail, tc.method)
			if got := w.wantGate(p, runInProgress, ""); !reflect.DeepEqual(got, pending) {
				t.Errorf("the pending check changed: %+v", got)
			}
			if len(w.comments(p)) != 0 {
				t.Error("a failed upload left a comment")
			}
			w.noSuccess()

			// The Action retries after Retry-After (60 s), when a lease kept by a failed write has expired.
			w.now = w.now.Add(time.Minute)
			w.wantAnswer(w.send(s), 200, "success", "healthy")
			if done := w.wantGate(p, runCompleted, "success"); done.ID != pending.ID {
				t.Errorf("the retry created check %d beside the pending %d", done.ID, pending.ID)
			}
			if n := len(w.comments(p)); n != 1 {
				t.Errorf("%d comments after the retry, want 1", n)
			}
		})
	}
}

func TestClientSuppliedTenantIsIgnored(t *testing.T) {
	const otherTenant = 99
	w := newWorld(t)
	vault := w.addRepo("acme/vault", 12, 201, true)
	w.addRepo("mallory/other", otherTenant, 901, true)
	w.entitle(otherTenant, Entitlement{SuspendAfter: w.now.Add(-time.Hour), Reason: "other tenant is suspended"})
	p := w.openPull(vault, 7)

	s := w.spec(p, w.report(vault, fixtureHealthy))
	s.url = "/v1/reports?installation_id=99&tenant=99&repository=mallory/other"
	s.headers = map[string]string{
		"X-Installation-Id": "99", "X-GitHub-Installation-Id": "99", "X-Archfit-Tenant": "99",
		"X-Archfit-Repository": "mallory/other",
	}
	w.wantAnswer(w.send(s), 200, "success", "healthy")

	if len(w.store.tenants) == 0 {
		t.Fatal("the Store was not read")
	}
	for i, tenant := range w.store.tenants {
		if tenant != store.Tenant(12) || w.store.keys[i] != EntitlementKey {
			t.Errorf("Store read under tenant %d key %q", tenant, w.store.keys[i])
		}
	}
	if len(w.store.leaseTenants) == 0 {
		t.Fatal("the publication was not serialized")
	}
	for _, tenant := range w.store.leaseTenants {
		if tenant != store.Tenant(12) {
			t.Errorf("publication lease under tenant %d", tenant)
		}
	}
	for _, c := range w.gh.calls {
		if c.installationID == otherTenant || strings.HasPrefix(c.fullName, "mallory/") {
			t.Errorf("API call addresses the other tenant: %+v", c)
		}
	}
}

const (
	leakFinding = "SEEDED_LEAK_FINDING_c0ffee"
	leakSource  = "SEEDED_LEAK_SOURCE_func_main"
	leakPrompt  = "SEEDED_LEAK_PROMPT_ignore_previous"
)

func TestNoSourceLeaves(t *testing.T) {
	tests := []struct {
		name string
		body func(w *world) []byte
	}{
		{"source excerpt at the top level", func(w *world) []byte {
			return w.report(w.shop, fixtureHealthy, func(doc map[string]any) { doc["source_excerpt"] = leakSource })
		}},
		{"snippet inside a finding", func(w *world) []byte {
			return w.report(w.shop, fixtureBlocked, func(doc map[string]any) {
				doc["findings"].([]any)[0].(map[string]any)["snippet"] = leakFinding
			})
		}},
		{"raw model response on a task", func(w *world) []byte {
			return w.report(w.shop, fixtureBlocked, func(doc map[string]any) {
				doc["agent_tasks"].([]any)[0].(map[string]any)["raw_response"] = leakPrompt
			})
		}},
		{"not JSON at all", func(*world) []byte { return []byte("package main // " + leakSource) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, p))

			a := w.send(w.spec(p, tc.body(w)))
			w.wantAnswer(a, 200, "action_required", "report_invalid")

			surfaces := map[string]string{"log": logs.String(), "HTTP body": a.raw, "feedback": w.feedbackText(p)}
			for _, c := range w.gh.checkWrites {
				surfaces["check "+c.Name] += c.Title + c.Summary + c.ExternalID
			}
			for where, text := range surfaces {
				for _, seed := range []string{leakFinding, leakSource, leakPrompt} {
					if strings.Contains(text, seed) {
						t.Errorf("%s carries the seeded string %s", where, seed)
					}
				}
			}
		})
	}
}

func TestLogsCarryOnlyIdentifiers(t *testing.T) {
	const (
		markerWhy  = "MARKER_MESSAGE_why_text"
		markerPath = "src/MARKER_PATH/secret.go"
		markerGoal = "MARKER_MESSAGE_goal_text"
	)
	logs := captureLogs(t)
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.changes(p, policy.PolicyPath, markerPath)
	w.setFile(w.shop, p.head, policy.PolicyPath, []byte(basePolicy+"# edited\n"))

	// Webhook, upload with markers in messages and paths, review, protected push, sweep.
	w.handle(w.event(webhook.PullOpened, p))
	s := w.spec(p, w.report(w.shop, fixtureBlocked, func(doc map[string]any) {
		doc["comparison"].(map[string]any)["config_hash"] = w.digestOf(w.shop, p.head, policy.PolicyPath)
		finding := doc["findings"].([]any)[0].(map[string]any)
		finding["why"] = markerWhy
		finding["locations"].([]any)[0].(map[string]any)["file"] = markerPath
		doc["agent_tasks"].([]any)[0].(map[string]any)["goal"] = markerGoal
	}))
	tokens := []string{s.token}
	w.wantAnswer(w.send(s), 200, "action_required", "owner_approval_missing")
	for _, marker := range []string{markerWhy, markerPath, markerGoal} {
		if strings.Contains(w.feedbackText(p), marker) {
			t.Fatalf("unapproved report leaked %q into recovery-only feedback", marker)
		}
	}
	w.review(p, ownerLogin, "APPROVED", p.head)
	w.handle(w.event(webhook.ReviewChanged, p))
	s = w.spec(p, s.body)
	tokens = append(tokens, s.token)
	if a := w.send(s); a.status != 200 {
		t.Fatalf("approved upload = %d %s", a.status, a.raw)
	}
	if !strings.Contains(w.feedbackText(p), markerWhy) {
		t.Fatal("approved malicious-content report never reached feedback: log privacy control proves nothing")
	}
	w.handle(w.pushEvent(w.shop, policy.PolicyPath, markerPath))
	w.now = w.now.Add(2 * time.Hour)
	if _, err := w.gate.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.gh.fail["File"] = errors.New("GET /repos/" + shopName + "/contents/" + markerPath + ": 502")
	s = w.spec(p, s.body) // the flows above re-ran the workflow: a new attempt
	if a := w.send(s); a.status != 503 {
		t.Fatalf("answer = %d %s", a.status, a.raw)
	}

	text := logs.String()
	for _, banned := range append([]string{markerWhy, markerPath, "MARKER", markerGoal, s.token, shopName, testWorkflow, policy.PolicyPath, "pkg/a"}, tokens...) {
		if strings.Contains(text, banned) {
			t.Errorf("a log line carries %q", banned)
		}
	}

	allowed := map[string]bool{
		"time": true, "level": true, "msg": true,
		"installation_id": true, "repository_id": true, "pr": true, "head_sha": true, "run_id": true, "run_attempt": true,
		"digest": true, "conclusion": true, "reasons": true, "status": true, "duration_ms": true,
		"kind": true, "delivery_id": true, "reaction": true, "failed": true,
	}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(logs.Bytes()))
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("log line is not JSON: %s", scanner.Text())
		}
		msg, _ := line["msg"].(string)
		if msg != "gate upload" && msg != "gate event" {
			continue // the children's lines are theirs; the banned strings above cover them
		}
		seen[msg] = true
		for key := range line {
			if !allowed[key] {
				t.Errorf("%q logs the key %q", msg, key)
			}
		}
	}
	if !seen["gate upload"] || !seen["gate event"] {
		t.Errorf("façade log lines seen: %v", seen)
	}
}

func TestStoreWritesOnlyLeasesAndInputsRecords(t *testing.T) {
	// guardStore panics on every write but a lease compare-and-swap and a compare-and-swap of a pusher or approval
	// record, and on Tenants; this drives every façade entry point over it.
	w := newWorld(t)
	vault := w.addRepo("acme/vault", 12, 201, true)
	w.entitle(12, Entitlement{SuspendAfter: w.now.Add(time.Hour)})
	p := w.openPull(vault, 7)
	w.handle(w.event(webhook.PullOpened, p))
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
	w.handle(w.event(webhook.ReviewChanged, p))
	w.handle(w.pushEvent(vault))
	w.handle(w.branchPush(p, ownerLogin))
	if _, err := w.gate.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := w.openPull(vault, 8)
	w.editsBaseline(q)
	w.handle(w.event(webhook.PullOpened, q))
	w.approve(q, ownerLogin)
	if len(w.store.tenants) == 0 {
		t.Fatal("the Store was never read")
	}
	written := map[string]bool{}
	for i, tenant := range w.store.recordTenants {
		if tenant != store.Tenant(12) {
			t.Errorf("record %s under tenant %d", w.store.recordKeys[i], tenant)
		}
		written[strings.SplitN(w.store.recordKeys[i], "/", 2)[0]+"/"] = true
	}
	if !written[pusherKeyPrefix] || !written[approvalKeyPrefix] {
		t.Fatalf("records touched: %v, want a pusher and an approval record", written)
	}
	for i, key := range w.store.keys {
		if key != EntitlementKey || w.store.tenants[i] == store.SystemTenant {
			t.Errorf("Store read of key %q under tenant %d", key, w.store.tenants[i])
		}
	}
	for _, tenant := range w.store.leaseTenants {
		if tenant != store.Tenant(12) {
			t.Errorf("publication lease under tenant %d", tenant)
		}
	}
	// The pull's lease is released once its writes are done.
	if _, err := w.mem.Get(context.Background(), 12, leaseKeyPrefix+p.head); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("publication lease left behind: %v", err)
	}
}
