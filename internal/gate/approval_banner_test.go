package gate

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestPullOpenedPublishesOwnerDecisionBannerBeforeAnyReport(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.editsBaseline(p)
	w.changes(p, policy.BaselinePath)
	w.handle(w.event(webhook.PullOpened, p))

	comments := w.comments(p)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Architecture decision needed") ||
		!strings.Contains(comments[0].Body, ownerLogin) || !strings.Contains(comments[0].Body, p.repo.FullName) ||
		!strings.Contains(comments[0].Body, p.head) || !strings.Contains(comments[0].Body, "See this pull request in Archfit") ||
		!strings.Contains(comments[0].Body, "baseline") {
		t.Fatalf("owner decision comment = %+v", comments)
	}
	check, ok := w.check(p, p.head, gateCheckName)
	if !ok || check.Status != runInProgress || !strings.Contains(check.Summary, "Architecture decision needed") ||
		!strings.Contains(check.Summary, ownerLogin) || !strings.Contains(check.Summary, "See this pull request in Archfit") {
		t.Fatalf("pending gate check = %+v, exists=%v; want owner banner before report", check, ok)
	}
}
