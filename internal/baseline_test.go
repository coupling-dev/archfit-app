package internal_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestBaselineReachesMainOnlyThroughAPullRequest runs the real gate receiver, OIDC verification and proposal code
// over the GitHub fake: a baseline captured on main becomes one pull request and never a commit to main.
func TestBaselineReachesMainOnlyThroughAPullRequest(t *testing.T) {
	protected := loadFixture(t, "real-baselined-comparable") // two accepted findings on main
	capture := readFixture(t, "report", "real-attention-baseline.baseline.json")

	t.Run("one pull request, retried, main untouched", func(t *testing.T) {
		w := newWorld(t)
		w.seedDemo(protected, "advisory", true)
		s := newSystem(t, w, options{})
		head := discoveryRun(w, demoRepo, 555)

		for range 2 { // the producer retries
			got := readAnswer(t, s.do(baselineUpload(demoRepo, demoRepoID, 555, head, capture, protected.labels).request(t, "/v1/baselines")))
			if got.Status != http.StatusOK || got.PullRequest == 0 {
				t.Fatalf("baseline upload answered %+v; want 200 with a pull request", got)
			}
		}
		var opened []pull
		for _, p := range w.pullsOf(demoRepo) {
			if p.HeadRef == "archfit/baseline" {
				opened = append(opened, p)
			}
		}
		if len(opened) != 1 || opened[0].BaseRef != "main" || opened[0].State != "open" {
			t.Fatalf("baseline pull requests = %+v; want one open PR from archfit/baseline into main", opened)
		}
		if got, _ := w.file(demoRepo, "archfit/baseline", ".archfit-baseline.json"); got != string(capture) {
			t.Error("the pull request does not carry the uploaded baseline byte for byte")
		}
		var mainHead string
		w.edit(demoRepo, func(rp *repo) { mainHead = rp.branches["main"] })
		if mainHead != head {
			t.Errorf("main moved from %s to %s; the App must never commit to it", head, mainHead)
		}
		if got, _ := w.file(demoRepo, "main", ".archfit-baseline.json"); got != string(protected.baseline) {
			t.Error("the baseline on main changed")
		}
		body := opened[0].Body
		for _, want := range []string{"| Removed | 2 |", "Approving accepts no finding", "architecture owner"} {
			if !strings.Contains(body, want) {
				t.Errorf("PR body does not mention %q:\n%s", want, body)
			}
		}
		if checks := w.checkRuns(demoRepo); len(checks) != 0 {
			t.Errorf("a baseline upload produced checks: %+v", checks)
		}
	})

	refused := []struct {
		name   string
		mutate func(w *world, u *upload)
		status int
		code   string
	}{
		{"main moved after the capture", func(w *world, _ *upload) {
			w.edit(demoRepo, func(rp *repo) {
				rp.push("main", strings.Repeat("9", 40), map[string]string{"pkg/a/b.go": "package a\n"})
			})
		}, http.StatusConflict, "stale_head"},
		{"run on another branch", func(w *world, u *upload) {
			w.edit(demoRepo, func(rp *repo) { rp.branches["topic"] = rp.branches["main"] })
			u.claims["ref"] = "refs/heads/topic"
			u.claims["job_workflow_ref"] = demoRepo + "/" + workflowPath + "@refs/heads/topic"
		}, http.StatusForbidden, "not_default_branch"},
		{"captured under another policy", func(_ *world, u *upload) {
			other := readFixture(t, "report", "real-healthy.baseline.json")
			u.body, u.env.PayloadDigest = other, "sha256:"+digest(other)
		}, http.StatusForbidden, "policy_mismatch"},
		{"not a baseline", func(_ *world, u *upload) {
			u.body, u.env.PayloadDigest = protected.report, "sha256:"+digest(protected.report)
		}, http.StatusBadRequest, "baseline_invalid"},
		{"report envelope on the baseline route", func(_ *world, u *upload) { u.env.Kind = "report" }, http.StatusBadRequest, "kind_mismatch"},
		{"token for another audience", func(_ *world, u *upload) { u.claims["aud"] = baseURL + "/" }, http.StatusUnauthorized, "token_invalid"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.seedDemo(protected, "advisory", true)
			s := newSystem(t, w, options{})
			head := discoveryRun(w, demoRepo, 555)
			u := baselineUpload(demoRepo, demoRepoID, 555, head, capture, protected.labels)
			claims := map[string]any{}
			for k, v := range u.claims {
				claims[k] = v
			}
			u.claims = claims
			tc.mutate(w, &u)

			got := readAnswer(t, s.do(u.request(t, "/v1/baselines")))
			if got.Status != tc.status || got.Error != tc.code {
				t.Errorf("answer = %+v; want %d %s", got, tc.status, tc.code)
			}
			for _, p := range w.pullsOf(demoRepo) {
				if p.HeadRef == "archfit/baseline" {
					t.Errorf("a refused upload opened %+v", p)
				}
			}
			if got, _ := w.file(demoRepo, "main", ".archfit-baseline.json"); got != string(protected.baseline) {
				t.Error("the baseline on main changed")
			}
		})
	}
}
