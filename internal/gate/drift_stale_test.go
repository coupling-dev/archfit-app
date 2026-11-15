package gate

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestAPushOvertakenWhilePublishingDoesNotTouchDrift(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	s := w.pushSpec(p, "repository_drift")
	w.gh.beforeCheckWrite = func() {
		w.gh.mu.Lock()
		defer w.gh.mu.Unlock()
		w.gh.heads[branchKey(shopName, testBranch)] = "newer-tip"
	}
	w.wantStatus(w.send4(s), "pass", "")
	if len(w.driftIssues()) != 0 {
		t.Errorf("overtaken push opened drift: %+v", w.driftIssues())
	}
}

func TestDriftReturnsAFailedFinalTipRead(t *testing.T) {
	w := newWorld4(t)
	w.gh.fail = map[string]error{"BranchHead": errors.New("unavailable")}
	u := ingest.Upload4{Binding: ingest.Binding4{Repo: w.shop, Event: envelope.EventPush, HeadSHA: w.tip},
		Result: &result.Result{Repository: &result.Repository{State: result.RepositoryClean}}}
	if err := w.gate.driftPushed(context.Background(), u, decision4{Tip: w.tip}); err == nil {
		t.Error("failed tip read was not returned")
	}
}
