package gate

import (
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestWaitingRefreshDoesNotOverwriteANewerUploadInsideTheLease(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.ask(p, workerLogin, "/archfit request allow this dependency")
	old := w.wantSweepGate(p, "in_progress", "")
	for i, c := range w.gh.checks[shopName] {
		if c.ID == old.ID {
			w.gh.checks[shopName][i].StartedAt = w.now.Add(-11 * 24 * time.Hour)
		}
	}
	// The upload lands after the sweep's reads but before its lease is acquired.
	w.store.beforeLease = func() {
		w.editRun(p, func(run *api.WorkflowRun) { run.Attempt++ })
		s := w.spec4(p, "pass")
		s.env.RunAttempt = 2
		s.env.PolicyDigest = w.repo4().ApprovedDigest
		s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
		w.wantStatus(w.send4(s), "pass", "")
	}
	w.sweep4()
	c := w.wantSweepGate(p, "completed", "success")
	if c.ID < old.ID {
		t.Fatalf("newer publication replaced with %+v", c)
	}
}
