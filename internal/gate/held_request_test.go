package gate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	asks "github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

const (
	heldReply    = "request received before the check result"
	newHeadReply = "the pull request has a new head"
	passReply    = "the check passes, so there is nothing to ask"
)

// held reads the request line held for pull request p.
func (w *world4) held(p *pull) (records.HeldRequest, bool) {
	w.t.Helper()
	var h records.HeldRequest
	err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.HeldRequestKey(shopName, p.number), &h)
	if errors.Is(err, records.ErrNoRecord) {
		return h, false
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return h, true
}

// uploadAskGolden sends a result golden without errors to amend, under policyAsk.
func (w *world4) uploadAskGolden(p *pull, golden, status string) {
	w.t.Helper()
	s := w.spec4(p, golden)
	s.env.PolicyDigest = policy3.Digest([]byte(policyAsk))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	w.wantStatus(w.send4(s), status, "")
}

// replies are the App's comments on p after the check comment, oldest first.
func (w *world4) replies(p *pull) []string {
	var out []string
	for _, c := range w.gh.comments[pullKey(shopName, p.number)] {
		if !strings.Contains(c.Body, "## ") {
			out = append(out, c.Body)
		}
	}
	return out
}

func TestAnEarlyRequestLineIsHeldAndFiledWhenTheResultLands(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.ask(p, workerLogin, "/archfit request tc needs the retry helper")

	if got := w.replies(p); len(got) != 1 || !strings.Contains(got[0], heldReply) {
		t.Fatalf("replies = %q, want one that holds the line", got)
	}
	clean(t, w.lastReply(7))
	h, ok := w.held(p)
	if !ok || h.HeadSHA != p.head || h.Author != workerLogin || h.Reason != "tc needs the retry helper" || h.Today == "" {
		t.Fatalf("held = %+v, %v", h, ok)
	}
	if n := len(w.gh.issues[shopName]); n != 0 {
		t.Fatalf("%d issues before the result", n)
	}

	w.uploadAsk(p, "graph", "fail")

	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Fatalf("%d issues, want 1", n)
	}
	if got := w.storedRequest(firstRequest); got.Reason != "tc needs the retry helper" || got.AskedBy != workerLogin {
		t.Errorf("request = %+v", got)
	}
	if reply := w.lastReply(7); !strings.Contains(reply, "request received") || strings.Contains(reply, heldReply) {
		t.Errorf("reply:\n%s", reply)
	}
	w.wantWaiting(p, "waiting, request #1000")
	if _, ok := w.held(p); ok {
		t.Error("the filed line is still held")
	}
	if reaction, err := w.gate.asks.Release(context.Background(), p.repo, p.number); err != nil || reaction != asks.ReactionNone {
		t.Errorf("a second release = %s, %v", reaction, err)
	}
}

func TestAnEarlyRequestLineIsDiscardedOnANewHeadOrAPass(t *testing.T) {
	tests := map[string]struct {
		land func(w *world4, p *pull)
		want string
	}{
		"a new head": {func(w *world4, p *pull) { w.push(p); w.uploadAsk(p, "graph", "fail") }, newHeadReply},
		"a pass":     {func(w *world4, p *pull) { w.uploadAskGolden(p, "pass", "pass") }, passReply},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.ask(p, workerLogin, "/archfit request tc needs it")
			tt.land(w, p)
			if reply := w.lastReply(7); !strings.Contains(reply, tt.want) {
				t.Errorf("reply:\n%s\nwant %q", reply, tt.want)
			}
			clean(t, w.lastReply(7))
			if n := len(w.gh.issues[shopName]); n != 0 {
				t.Errorf("%d issues opened", n)
			}
			if _, ok := w.held(p); ok {
				t.Error("the discarded line is still held")
			}
		})
	}
}

func TestAnEarlyRequestLineWaitsForAVerdictOfItsHead(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.push(p) // the record is of an older head now
	w.ask(p, workerLogin, "/archfit request tc needs it")
	if _, ok := w.held(p); !ok {
		t.Fatal("a line on a head without a result is not held")
	}

	w.uploadAskGolden(p, "policy_differs", "unknown")
	if _, ok := w.held(p); !ok || len(w.gh.issues[shopName]) != 0 {
		t.Fatalf("an unknown released the line: held %v, %d issues", ok, len(w.gh.issues[shopName]))
	}

	w.uploadAsk(p, "graph", "fail")
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Fatalf("%d issues after the fail, want 1", n)
	}
	if _, ok := w.held(p); ok {
		t.Error("the filed line is still held")
	}
}

func TestOnlyOneReleaseFilesAHeldLine(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.put4(records.HeldRequestKey(shopName, p.number), &records.HeldRequest{HeadSHA: p.head, Author: workerLogin,
		Reason: "tc needs it", Today: "2026-11-24"})

	var wg sync.WaitGroup
	reactions := make([]string, 2)
	for i := range reactions {
		wg.Go(func() {
			var err error
			if reactions[i], err = w.gate.asks.Release(context.Background(), p.repo, p.number); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Errorf("%d issues, want 1 (reactions %v)", n, reactions)
	}
}

func TestAFailedFilingIsHeldAgainAndFiledOnce(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.ask(p, workerLogin, "/archfit request tc needs it")
	w.gh.fail["CreateIssue"] = errors.New("github is down")
	w.uploadAsk(p, "graph", "fail") // the upload succeeds; its filing fails
	if _, ok := w.held(p); !ok || len(w.gh.issues[shopName]) != 0 {
		t.Fatalf("after a failed filing: held %v, %d issues", ok, len(w.gh.issues[shopName]))
	}

	delete(w.gh.fail, "CreateIssue")
	w.ask(p, workerLogin, "/archfit request tc needs it") // a retried delivery, judged against the result
	if _, ok := w.held(p); ok {
		t.Error("a line judged against the result left the held copy")
	}
	if reaction, err := w.gate.asks.Release(context.Background(), p.repo, p.number); err != nil || reaction != asks.ReactionNone {
		t.Errorf("release after the retry = %s, %v", reaction, err)
	}
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Errorf("%d issues, want 1", n)
	}
}

func TestAHeldLineStaysHeldWhenTheHeadLosesItsVerdict(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.put4(records.HeldRequestKey(shopName, p.number), &records.HeldRequest{HeadSHA: p.head, Author: workerLogin,
		Reason: "tc needs it", Today: "2026-11-24"})
	rec, _ := w.pullRecord(p)
	replies := len(w.replies(p))
	reads := 0
	w.gh.onPull = func() {
		// Release read a verdict; a newer attempt of the head is decided unknown before the line is judged.
		if reads++; reads == 2 {
			rec.Status, rec.RunAttempt = "unknown", rec.RunAttempt+1
			w.put4(records.PullKey(shopName, p.number), &rec)
		}
	}
	reaction, err := w.gate.asks.Release(context.Background(), p.repo, p.number)
	if err != nil || reaction != asks.ReactionHeld {
		t.Fatalf("release = %s, %v; want %s", reaction, err, asks.ReactionHeld)
	}
	if _, ok := w.held(p); !ok {
		t.Error("the line is no longer held")
	}
	if got := len(w.replies(p)); got != replies || len(w.gh.issues[shopName]) != 0 {
		t.Errorf("%d new replies, %d issues", got-replies, len(w.gh.issues[shopName]))
	}
}

func TestAHeldLineIsFiledOnlyWithThePermissionItsAuthorHasThen(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.gh.permissions[shopName+"|writer-2"] = "write"
	w.ask(p, "writer-2", "/archfit request tc needs it")
	if _, ok := w.held(p); !ok {
		t.Fatal("a writer's early line is not held")
	}
	w.gh.permissions[shopName+"|writer-2"] = "read"
	w.uploadAsk(p, "graph", "fail")
	if reply := w.lastReply(7); !strings.Contains(reply, "a request counts only from the author") {
		t.Errorf("reply:\n%s", reply)
	}
	if n := len(w.gh.issues[shopName]); n != 0 {
		t.Errorf("%d issues opened", n)
	}
}

func TestAClosedPullRequestDropsItsHeldLine(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.ask(p, workerLogin, "/archfit request tc needs it")
	w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
	w.handle(w.event(webhook.PullClosed, p))
	if _, ok := w.held(p); ok {
		t.Error("the closed pull request still holds a line")
	}
}

func TestAnAdvisoryCheckNamesTheFiledRequest(t *testing.T) {
	w := newWorld4(t) // policy4 has no gate: advisory
	p := w.pullBy(7)
	w.wantStatus(w.send4(w.spec4(p, "graph")), "fail", "")
	if comment := w.comment(p); !strings.Contains(comment, "the author asks an owner") {
		t.Fatalf("before the request the comment says:\n%s", comment)
	}

	w.ask(p, workerLogin, "/archfit request tc needs it")
	wantNamed := func(when string) {
		t.Helper()
		comment := w.comment(p)
		if strings.Contains(comment, "the author asks an owner") || !strings.Contains(comment, "ask #1000") ||
			!strings.Contains(comment, "request #1000") || !strings.Contains(comment, "Archfit: fail") {
			t.Errorf("%s the comment says:\n%s", when, comment)
		}
		clean(t, comment)
		c, _ := w.latestGate(p.head)
		if c.Status != runCompleted || c.Conclusion != "neutral" || strings.Contains(c.Title, "waiting") {
			t.Errorf("%s the gate is %+v, want a neutral fail", when, c)
		}
		if rec, _ := w.pullRecord(p); rec.Status != "fail" {
			t.Errorf("%s the pull record status is %s", when, rec.Status)
		}
	}
	wantNamed("after the request")

	w.push(p)
	w.wantStatus(w.send4(w.spec4(p, "graph")), "fail", "")
	wantNamed("after a new push")
	if got := w.storedRequest(firstRequest).Waiting; len(got) != 1 || got[0] != 7 {
		t.Errorf("request waiting = %v", got)
	}
}
