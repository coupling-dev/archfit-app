package internal_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// pullApproval is the approval block of the pull request page's JSON.
type pullApproval struct {
	HeadSHA    string            `json:"head_sha"`
	Paths      []string          `json:"paths"`
	Digests    map[string]string `json:"digests"`
	State      string            `json:"state"`
	By         string            `json:"by"`
	CanApprove bool              `json:"can_approve"`
	Reason     string            `json:"reason"`
}

func readApproval(t *testing.T, b *browser) pullApproval {
	t.Helper()
	resp, raw := b.get("/api/repos/" + demoRepo + "/pulls/42")
	var page struct{ Approval *pullApproval }
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(raw), &page) != nil || page.Approval == nil {
		t.Fatalf("pull request page = %d %.300s; want an approval block", resp.StatusCode, raw)
	}
	return *page.Approval
}

// policyChange makes the demo pull request change the policy: main holds a stricter one, and the report was computed
// with the head's.
func policyChange(t *testing.T, w *world, settings string) {
	t.Helper()
	w.edit(demoRepo, func(rp *repo) {
		rp.push("main", demoBase, map[string]string{
			".archfit.yaml":     string(loadFixture(t, "real-blocked-no-baseline").policy),
			".archfit-app.yaml": settings,
		})
	})
}

// A sole owner cannot approve their own pull request on GitHub, and a push from the owner's account is no approval: a
// local coding agent pushes with the same credentials. The owner approves the exact commit on the App's page instead.
func TestSoleOwnerApprovesTheExactCommitInTheApp(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	policyChange(t, w, settingsYAML("required", "customer"))
	w.allow(owner, demoRepo, "write")
	s := newSystem(t, w, options{})
	if got := s.upload(demoUpload(f)); got.Reason != "owner_approval_missing" {
		t.Fatalf("the unapproved policy change answered %+v; want owner_approval_missing", got)
	}

	b := newBrowser(t, s.do)
	b.signIn(owner)
	shown := readApproval(t, b)
	if !shown.CanApprove || shown.State != "missing" || shown.HeadSHA != demoHead || !slices.Equal(shown.Paths, []string{".archfit.yaml"}) ||
		shown.Digests[".archfit.yaml"] == "" {
		t.Fatalf("approval = %+v; want the owner offered the head's policy", shown)
	}

	resp, raw := b.postJSON("/api/repos/"+demoRepo+"/pulls/42/approve", map[string]any{"head_sha": shown.HeadSHA, "digests": shown.Digests})
	if resp.StatusCode != http.StatusOK || !strings.Contains(raw, `"state":"granted"`) || !strings.Contains(raw, `"by":"`+owner+`"`) {
		t.Fatalf("approve = %d %.300s; want granted by %s", resp.StatusCode, raw, owner)
	}
	if reruns := w.rerunsOf(demoRepo); !slices.Equal(reruns, []int64{demoRun}) {
		t.Fatalf("re-runs = %v; want the customer's workflow run", reruns)
	}
	if c := w.check(demoRepo, demoHead, checkGate); c.Conclusion != "action_required" {
		t.Errorf("archfit/gate = %s; the approval alone must turn nothing green", c.Conclusion)
	}
	completeRun(w)
	if got := s.upload(demoUpload(f).attempt(2)); got.Conclusion != "success" {
		t.Fatalf("the re-run after the approval answered %+v; want success", got)
	}
}

func TestAnAgentIdentityCannotApproveInTheApp(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	policyChange(t, w, "version: 1\nowners: ["+owner+"]\nagents: ["+owner+"]\ngate: required\nworkflow: "+workflowPath+"\n")
	w.allow(owner, demoRepo, "write")
	s := newSystem(t, w, options{})
	if got := s.upload(demoUpload(f)); got.Reason != "owner_approval_missing" {
		t.Fatalf("answer = %+v; want owner_approval_missing", got)
	}

	b := newBrowser(t, s.do)
	b.signIn(owner)
	shown := readApproval(t, b)
	if shown.CanApprove || !strings.Contains(shown.Reason, "agent identity") {
		t.Fatalf("approval = %+v; want no Approve and the agent reason", shown)
	}
	resp, raw := b.postJSON("/api/repos/"+demoRepo+"/pulls/42/approve", map[string]any{"head_sha": shown.HeadSHA, "digests": shown.Digests})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("approve = %d %.300s; want 403", resp.StatusCode, raw)
	}
	if reruns := w.rerunsOf(demoRepo); len(reruns) != 0 {
		t.Errorf("re-runs = %v; want none", reruns)
	}
	if got := s.upload(demoUpload(f)); got.Reason != "owner_approval_missing" {
		t.Errorf("the report sent again answered %+v; want owner_approval_missing", got)
	}
}
