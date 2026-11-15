package gate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestSweepWithdrawsExpiredOrStaleSuccessBeforeRerunErrors(t *testing.T) {
	for _, policyChanged := range []bool{false, true} {
		for _, method := range []string{"Run", "HeadRuns", "Compare", "Rerun"} {
			t.Run(map[bool]string{false: "expired", true: "stale"}[policyChanged]+"/"+method, func(t *testing.T) {
				w := newWorld4(t)
				w.now = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
				policy := datedPolicy4
				if policyChanged {
					policy = policy4
				}
				w.guarded4(policy)
				p := w.openPull(w.shop, 7)
				s := w.spec4(p, "pass")
				s.env.PolicyDigest = w.repo4().ApprovedDigest
				s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
				s.result["date"] = "2026-11-18"
				w.wantStatus(w.send4(s), "pass", "")
				base := w.sha()
				w.editPull(p, func(pl *api.Pull) { pl.BaseSHA = base })
				if policyChanged {
					w.guarded4(policy4 + "# new approval\n")
				}
				w.gh.compares[shopName+"|"+base+"|"+w.tip] = api.Comparison{Status: "ahead", Paths: []string{policyPath}}
				w.gh.fail = map[string]error{method: errors.New("service unavailable")}
				sum, err := w.gate.sweep4(t.Context(), Summary{})
				if err != nil || sum.Errors != 1 {
					t.Fatalf("sweep = %+v,%v", sum, err)
				}
				w.wantSweepGate(p, "completed", "action_required")
			})
		}
	}
}

func TestSweepFindsAMissingNewerAttemptOrRunAfterAnOlderUpload(t *testing.T) {
	for _, newRun := range []bool{false, true} {
		for _, age := range []time.Duration{59 * time.Minute, 61 * time.Minute} {
			t.Run(map[bool]string{false: "attempt", true: "run"}[newRun]+"/"+age.String(), func(t *testing.T) {
				w := newWorld4(t)
				p := w.openPull(w.shop, 7)
				w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
				w.now = w.now.Add(3 * time.Hour)
				run := w.gh.runs[p.runID]
				run.UpdatedAt = w.now.Add(-age)
				if newRun {
					run.ID++
					run.CreatedAt = w.now.Add(-2 * time.Hour)
				} else {
					run.Attempt++
				}
				w.gh.runs[run.ID] = run
				w.sweep4()
				conclusion := "success"
				if age > time.Hour {
					conclusion = "action_required"
				}
				w.wantSweepGate(p, "completed", conclusion)
				if age > time.Hour && !strings.Contains(w.comment(p), "without an uploaded result") {
					t.Error(w.comment(p))
				}
			})
		}
	}
}

func TestSweepProvenPolicyDeletionCreatesOutsideFlow(t *testing.T) {
	w := newWorld4(t)
	w.guarded4(policy4)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	before := w.tip
	tip := w.sha()
	w.tip4("", api.Commit{SHA: tip, Parents: []string{before}})
	w.removeFile(w.shop, tip, policyPath)
	w.removeFile(w.shop, testBranch, policyPath)
	w.sweep4()
	if r := w.repo4(); r.OutsideFlow == nil || r.OutsideFlow.Digest != "" {
		t.Fatalf("deletion state = %+v", r)
	}
	if len(w.gh.issues[shopName]) != 1 {
		t.Errorf("issues = %+v", w.gh.issues[shopName])
	}
	w.wantSweepGate(p, "completed", "action_required")
}
