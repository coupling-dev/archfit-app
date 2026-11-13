package gate

import (
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

// editsBaseline gives the default branch a baseline and makes the pull replace it; it returns the head digest.
func (w *world) editsBaseline(p *pull) string {
	w.setFile(p.repo, testBranch, policy.BaselinePath, []byte(`{"violations":[]}`))
	w.changes(p, "pkg/a/a.go", policy.BaselinePath)
	w.setFile(p.repo, p.head, policy.BaselinePath, []byte(`{"violations":["accepted by an agent"]}`))
	return w.digestOf(p.repo, p.head, policy.BaselinePath)
}

func TestSourceOnlyPushToTheDefaultBranchStillWritesNothing(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
	before := w.gh.writes()

	push := w.pushEvent(w.shop, "pkg/a/a.go")
	push.Branch = testBranch
	w.handle(push)
	if w.gh.writes() != before || !slices.Equal(w.gh.reruns, nil) {
		t.Errorf("writes %d → %d, reruns %v", before, w.gh.writes(), w.gh.reruns)
	}
}
