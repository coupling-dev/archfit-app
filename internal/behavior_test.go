package internal_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// reportFacts are the strings of a report that failure feedback must carry.
type reportFacts struct {
	Findings []struct {
		RuleID string `json:"rule_id"`
		Kind   string `json:"kind"`
		Status string `json:"status"`
		Edge   struct {
			From struct{ Module string } `json:"from"`
			To   struct{ Module string } `json:"to"`
		} `json:"edge"`
		Locations []struct {
			File string `json:"file"`
			Line int    `json:"line"`
		} `json:"locations"`
	} `json:"findings"`
	AgentTasks []struct {
		Validation []string `json:"validation"`
	} `json:"agent_tasks"`
}

func factsOf(t *testing.T, f fixture) reportFacts {
	t.Helper()
	var facts reportFacts
	if err := json.Unmarshal(f.report, &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestLegitimateChangePassesAndViolationFails(t *testing.T) {
	pairs := []struct{ scenario, healthy, blocked string }{
		{"forbidden dependency, no baseline", "real-attention-no-baseline", "real-blocked-no-baseline"},
		{"forbidden dependency beside accepted debt", "real-healthy", "real-blocked-forbidden-and-allowed"},
		{"waiver active and expired", "real-waiver-active", "real-waiver-expired"},
	}
	for _, pair := range pairs {
		t.Run(pair.scenario+"/healthy", func(t *testing.T) {
			f := loadFixture(t, pair.healthy)
			w := newWorld(t)
			w.seedDemo(f, "required", true)
			got := newSystem(t, w, options{}).upload(demoUpload(f))
			if got.Status != http.StatusOK || got.Conclusion != "success" {
				t.Fatalf("answer = %+v; want success", got)
			}
			if c := w.check(demoRepo, demoHead, checkGate); c == nil || c.Conclusion != "success" {
				t.Errorf("archfit/gate = %+v; want success", c)
			}
			facts := factsOf(t, f)
			if len(facts.AgentTasks) != 0 {
				t.Fatalf("fixture %s has agent tasks; it is not a healthy twin", pair.healthy)
			}
			if body := w.commentsOf(demoRepo, demoPull)[0].Body; strings.Contains(body, "### Validation commands") {
				t.Error("a healthy report has no task and must show no validation command")
			}
		})
		t.Run(pair.scenario+"/blocked", func(t *testing.T) {
			f := loadFixture(t, pair.blocked)
			w := newWorld(t)
			w.seedDemo(f, "required", true)
			got := newSystem(t, w, options{}).upload(demoUpload(f))
			if got.Status != http.StatusOK || got.Conclusion != "failure" || got.Reason != "blocking_findings" {
				t.Fatalf("answer = %+v; want failure blocking_findings", got)
			}
			if c := w.check(demoRepo, demoHead, checkGate); c == nil || c.Conclusion != "failure" {
				t.Errorf("archfit/gate = %+v; want failure", c)
			}
			comments := w.commentsOf(demoRepo, demoPull)
			if len(comments) != 1 {
				t.Fatalf("comments = %d; want 1", len(comments))
			}
			facts, blocking := factsOf(t, f), 0
			for _, fd := range facts.Findings {
				if fd.Kind != "gate" || (fd.Status != "new" && fd.Status != "expired_waiver") {
					continue
				}
				blocking++
				want := []string{fd.RuleID, "`" + fd.Edge.From.Module + "`", "`" + fd.Edge.To.Module + "`"}
				for _, loc := range fd.Locations {
					want = append(want, fmt.Sprintf("%s:%d", loc.File, loc.Line))
				}
				for _, s := range want {
					if !strings.Contains(comments[0].Body, s) {
						t.Errorf("the failure comment lacks %q", s)
					}
				}
			}
			if blocking == 0 || len(facts.AgentTasks) == 0 {
				t.Fatalf("fixture %s has %d blocking findings and %d tasks", pair.blocked, blocking, len(facts.AgentTasks))
			}
			for _, task := range facts.AgentTasks {
				for _, cmd := range task.Validation {
					if !strings.Contains(comments[0].Body, cmd) {
						t.Errorf("the failure comment lacks the validation command %q", cmd)
					}
				}
			}
		})
	}
}

// withoutProfile returns the fixture with the measurement profile removed from its report.
func withoutProfile(t *testing.T, f fixture) fixture {
	t.Helper()
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(f.report))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	comparison, _ := doc["comparison"].(map[string]any)
	if _, ok := comparison["measurement_profile"]; !ok {
		t.Fatal("the fixture has no comparison.measurement_profile to remove")
	}
	delete(comparison, "measurement_profile")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	f.report = raw
	return f
}

func TestTamperingNeverYieldsSuccess(t *testing.T) {
	const oldHead = "00000000000000000000000000000000000000aa"
	editHead := func(path, content string) func(*testing.T, *world, *upload) {
		return func(_ *testing.T, w *world, _ *upload) {
			w.edit(demoRepo, func(rp *repo) { rp.push("feature", demoHead, map[string]string{path: content}) })
		}
	}
	cases := []struct {
		name   string
		reason string
		detail string // must appear in the gate check
		tamper func(t *testing.T, w *world, u *upload)
	}{
		{"head-edited policy", "owner_approval_missing", ".archfit.yaml", func(t *testing.T, w *world, _ *upload) {
			// The producer measured with the head policy; the protected ref still holds the stricter one.
			w.edit(demoRepo, func(rp *repo) {
				rp.push("main", demoBase, map[string]string{".archfit.yaml": string(loadFixture(t, "real-blocked-no-baseline").policy)})
			})
		}},
		{"head-edited baseline", "owner_approval_missing", ".archfit-baseline.json", func(_ *testing.T, w *world, u *upload) {
			edited := `{"edited":"on the head"}`
			w.edit(demoRepo, func(rp *repo) { rp.push("feature", demoHead, map[string]string{".archfit-baseline.json": edited}) })
			u.env.BaselineDigest = digest([]byte(edited))
		}},
		{"baseline other than the protected one", "baseline_mismatch", "", func(_ *testing.T, _ *world, u *upload) {
			u.env.BaselineDigest = strings.Repeat("f", 64)
		}},
		{"head-edited settings", "owner_approval_missing", ".archfit-app.yaml",
			editHead(".archfit-app.yaml", "version: 1\nowners: [mallory]\ngate: advisory\nworkflow: "+workflowPath+"\n")},
		{"head-edited settings approved by the owner they name", "owner_approval_missing", ".archfit-app.yaml", func(t *testing.T, w *world, u *upload) {
			editHead(".archfit-app.yaml", "version: 1\nowners: [mallory]\ngate: advisory\nworkflow: "+workflowPath+"\n")(t, w, u)
			w.edit(demoRepo, func(rp *repo) { rp.pull(demoPull).reviews = []review{{"mallory", "APPROVED", demoHead}} })
		}},
		{"head-edited workflow", "owner_approval_missing", workflowPath, editHead(workflowPath, "name: archfit\n# edited\n")},
		{"workflow file other than the approved one", "workflow_not_approved", "", func(_ *testing.T, _ *world, u *upload) {
			u.claims["job_workflow_ref"] = demoRepo + "/.github/workflows/other.yaml@refs/pull/42/merge"
		}},
		{"head-edited CODEOWNERS confers no approval rights", "owner_approval_missing", ".archfit.yaml", func(t *testing.T, w *world, u *upload) {
			w.edit(demoRepo, func(rp *repo) {
				rp.push("main", demoBase, map[string]string{".archfit.yaml": string(loadFixture(t, "real-blocked-no-baseline").policy)})
				rp.push("feature", demoHead, map[string]string{".github/CODEOWNERS": "/.archfit.yaml @mallory\n"})
				rp.pull(demoPull).reviews = []review{{"mallory", "APPROVED", demoHead}}
			})
		}},
		{"stale approval after re-push", "owner_approval_missing", ".archfit.yaml", func(t *testing.T, w *world, _ *upload) {
			w.edit(demoRepo, func(rp *repo) {
				rp.push("main", demoBase, map[string]string{".archfit.yaml": string(loadFixture(t, "real-blocked-no-baseline").policy)})
				rp.pull(demoPull).reviews = []review{{owner, "APPROVED", oldHead}}
			})
		}},
		{"unknown image", "unknown_engine_identity", "", func(_ *testing.T, _ *world, u *upload) {
			u.env.ImageDigest = "sha256:" + strings.Repeat("4c", 32)
		}},
		{"missing profile", "measurement_profile_missing", "", func(t *testing.T, _ *world, u *upload) {
			f := withoutProfile(t, loadFixture(t, "real-healthy"))
			u.body, u.env.PayloadDigest = f.report, "sha256:"+digest(f.report)
		}},
		{"fork PR", "fork_unsupported", "", func(_ *testing.T, w *world, _ *upload) {
			w.edit(demoRepo, func(rp *repo) {
				rp.pull(demoPull).HeadRepo = "mallory/archfit-demo"
				rp.runs[demoRun].HeadRepo = "mallory/archfit-demo"
			})
		}},
		{"workflow_run upload", "unsupported_event", "", func(_ *testing.T, w *world, u *upload) {
			w.edit(demoRepo, func(rp *repo) { rp.runs[demoRun].Event = "workflow_run" })
			u.claims["event_name"] = "workflow_run"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := loadFixture(t, "real-healthy")
			w := newWorld(t)
			w.seedDemo(f, "required", true)
			s := newSystem(t, w, options{})
			u := demoUpload(f)
			tc.tamper(t, w, &u)

			got := s.upload(u)
			if got.Status != http.StatusOK || got.Conclusion != "action_required" || got.Reason != tc.reason {
				t.Fatalf("answer = %+v; want action_required %s", got, tc.reason)
			}
			gateCheck := w.check(demoRepo, demoHead, checkGate)
			if tc.name == "workflow_run upload" {
				// Such an upload binds to no pull request, so it cannot touch archfit/gate: the answer and
				// archfit/state carry the reason, and the required check stays absent or pending, never green.
				if gateCheck != nil {
					t.Fatalf("archfit/gate = %+v; want none for an upload without a pull request", gateCheck)
				}
				gateCheck = w.check(demoRepo, demoHead, checkState)
			}
			if gateCheck == nil || gateCheck.Status != "completed" || (gateCheck.Name == checkGate && gateCheck.Conclusion != "action_required") {
				t.Fatalf("check = %+v; want completed action_required", gateCheck)
			}
			if rc := gateCheck.receipt(t); rc.Reason != tc.reason || rc.Conclusion != "action_required" {
				t.Errorf("receipt = %+v; want reason %s", rc, tc.reason)
			}
			if !strings.Contains(gateCheck.Summary, tc.detail) {
				t.Errorf("the gate check does not name %q", tc.detail)
			}
			for _, c := range w.checkRuns(demoRepo) {
				if c.Conclusion == "success" || c.Name == checkGate && (c.Conclusion == "neutral" || c.Conclusion == "skipped") {
					t.Errorf("check %s concluded %s", c.Name, c.Conclusion)
				}
			}
		})
	}

	// The untampered control: the same upload is green, so every row above failed for its own reason.
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	if got := newSystem(t, w, options{}).upload(demoUpload(f)); got.Conclusion != "success" {
		t.Fatalf("the untampered upload answered %+v; want success", got)
	}

	// And the only exception the design allows: a base-version owner approved the exact head SHA.
	w = newWorld(t)
	w.seedDemo(f, "required", true)
	w.edit(demoRepo, func(rp *repo) {
		rp.push("main", demoBase, map[string]string{".archfit.yaml": string(loadFixture(t, "real-blocked-no-baseline").policy)})
		rp.pull(demoPull).reviews = []review{{owner, "APPROVED", demoHead}}
	})
	if got := newSystem(t, w, options{}).upload(demoUpload(f)); got.Conclusion != "success" {
		t.Fatalf("the owner-approved policy change answered %+v; want success", got)
	}
}

func TestUnapprovedAccountIsInertUntilAnOperatorApproves(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.allow("alice", demoRepo, "admin")
	s := newSystem(t, w, options{approved: []string{"acme"}})
	alice := newBrowser(t, s.do)
	alice.signIn("alice")
	olivia := newBrowser(t, s.do)
	olivia.signIn("olivia")

	opened := pullPayload("opened", demoInstallation, demoRepo, demoRepo, true, demoPull, demoHead)
	if gateErr, ctlErr := s.deliver("pull_request", opened); gateErr != nil || ctlErr != nil {
		t.Fatalf("deliver: gate %v, control %v", gateErr, ctlErr)
	}
	if got := s.upload(demoUpload(f)); got.Status != http.StatusForbidden || got.Error != "installation_not_approved" {
		t.Fatalf("upload before approval answered %+v; want 403 installation_not_approved", got)
	}
	if sum := s.sweep(); sum.NotAdmitted != 1 {
		t.Errorf("sweep before approval: %+v; want the installation skipped", sum)
	}
	if w.check(demoRepo, demoHead, checkGate) != nil || len(w.commentsOf(demoRepo, demoPull)) != 0 {
		t.Fatal("an unapproved account got a check or a comment")
	}
	resp, page := alice.get("/api/repos/" + demoRepo + "/overview")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(page, `"waiting":"alexei-led"`) {
		t.Fatalf("overview before approval: %d %s; want 403 waiting for alexei-led", resp.StatusCode, page)
	}
	if resp, _ := alice.postJSON(fmt.Sprintf("/api/admin/installations/%d/approve", demoInstallation), struct{}{}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a non-operator's approve: %d; want 404", resp.StatusCode)
	}

	if resp, page := olivia.postJSON(fmt.Sprintf("/api/admin/installations/%d/approve", demoInstallation), struct{}{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("operator approve: %d %s", resp.StatusCode, page)
	}
	if got := s.upload(demoUpload(f)); got.Status/100 != 2 || got.Conclusion != "success" {
		t.Fatalf("upload after approval answered %+v; want 2xx success", got)
	}
	if resp, page := alice.get("/api/repos/" + demoRepo + "/overview"); resp.StatusCode != http.StatusOK {
		t.Fatalf("overview after approval: %d %s", resp.StatusCode, page)
	}
}
