package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/request"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

const (
	workerLogin = "worker-1"
	// policyAsk is a required policy with two owners and a deny rule an owner wrote when rejecting a request. Its
	// instead holds a mention and a link, which must never take effect where the App shows them.
	policyAsk = "version: 3\ngate: required\nowners: [olivia, oscar]\nmodules:\n  app:\n    paths: [cmd/**, pkg/**]\n" +
		"rules:\n  - id: no-tc-to-net\n    deny:\n      from: [pkg_tc]\n      to: [pkg_net]\n" +
		"    why: tc stays offline\n    instead: \"ask @team, see [docs](https://example.com)\"\n"
	firstRequest = firstIssue // the number of the first issue the fake opens
)

// newAskWorld is world4 with policyAsk on the default branch.
func newAskWorld(t *testing.T) *world4 {
	t.Helper()
	w := newWorld4(t)
	w.setFile(w.shop, testBranch, policyPath, []byte(policyAsk))
	w.approved(w.shop, policyAsk)
	return w
}

// pullBy opens a pull request written by workerLogin.
func (w *world4) pullBy(number int) *pull {
	p := w.openPull(w.shop, number)
	w.editPull(p, func(pl *api.Pull) { pl.Author = workerLogin })
	return p
}

// upload sends the result golden for p's head under policyAsk; deps are more graph errors, "from>to" each.
func (w *world4) uploadAsk(p *pull, golden, status string, deps ...string) {
	w.t.Helper()
	s := w.spec4(p, golden)
	s.env.PolicyDigest = policy3.Digest([]byte(policyAsk))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	errs := s.result["errors"].([]any)
	for _, dep := range deps {
		from, to, _ := strings.Cut(dep, ">")
		e := map[string]any{}
		for k, v := range errs[0].(map[string]any) {
			e[k] = v
		}
		e["from"], e["to"] = from, to
		e["what"] = from + " -> " + to + " is not an allowed dependency"
		e["key"] = "graph " + from + " " + to + " example.com/" + to
		errs = append(errs, e)
	}
	s.result["errors"] = errs
	if len(errs) > 1 {
		s.result["summary"] = fmt.Sprintf("archfit: fail, %d errors", len(errs))
	}
	w.wantStatus(w.send4(s), status, "")
}

// ask posts a comment on p as login.
func (w *world4) ask(p *pull, login, body string) {
	w.t.Helper()
	e := w.event(webhook.PullCommented, p)
	e.CommentID, e.Author, e.AuthorType, e.Body = int64(9000+p.number), login, "User", body
	w.handle(e)
}

func (w *world4) issue(number int) fakeIssue {
	w.t.Helper()
	for _, i := range w.gh.issues[shopName] {
		if i.Number == number {
			return i
		}
	}
	w.t.Fatalf("no issue #%d", number)
	return fakeIssue{}
}

func (w *world4) storedRequest(number int) records.Request {
	w.t.Helper()
	var r records.Request
	if err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.RequestKey(shopName, number), &r); err != nil {
		w.t.Fatalf("request #%d: %v", number, err)
	}
	return r
}

// lastReply is the newest comment on a pull request or an issue.
func (w *world4) lastReply(number int) string {
	w.t.Helper()
	list := w.gh.comments[pullKey(shopName, number)]
	if len(list) == 0 {
		w.t.Fatalf("no comment on #%d", number)
	}
	return list[len(list)-1].Body
}

// clean fails when App text uses a "Not used" word or has a line the App would read as a request.
func clean(t *testing.T, text string) {
	t.Helper()
	if words := wording.NotUsedIn(text); len(words) > 0 {
		t.Errorf("text uses %v:\n%s", words, text)
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, request.Command) {
			t.Errorf("a line reads as a request: %q", line)
		}
	}
}

func (w *world4) wantWaiting(p *pull, title string) {
	w.t.Helper()
	c, ok := w.latestGate(p.head)
	if !ok || c.Status != runInProgress || c.Title != title {
		w.t.Fatalf("latest gate on #%d = %+v, want in_progress %q", p.number, c, title)
	}
}

func (w *world4) wantFailure(p *pull) {
	w.t.Helper()
	c, ok := w.latestGate(p.head)
	if !ok || c.Status != runCompleted || c.Conclusion != "failure" {
		w.t.Fatalf("latest gate on #%d = %+v, want a failure", p.number, c)
	}
}

func TestARequestLineOpensAnIssueAndSetsWaiting(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.ask(p, workerLogin, "Thanks.\n/archfit request tc needs the retry helper @team [x](https://evil.example) --expires 2026-12-01\n/archfit request a second line is ignored")

	i := w.issue(firstRequest)
	if i.Title != "Request: graph pkg_tc -> pkg_util" || !i.Open {
		t.Errorf("issue = %q open=%v", i.Title, i.Open)
	}
	if !slices.Equal(i.Labels, []string{request.Label}) || !slices.Equal(i.Assignees, []string{"olivia", "oscar"}) {
		t.Errorf("labels %v, assignees %v", i.Labels, i.Assignees)
	}
	if !strings.Contains(i.Body, "```text\ntc needs the retry helper @team [x](https://evil.example)\n```\n") ||
		!strings.Contains(i.Body, "- Pull requests: #7\n") || !strings.Contains(i.Body, "- Expires: 2026-12-01\n") ||
		strings.Contains(i.Body, "as reported") || strings.Contains(i.Body, "second line") {
		t.Errorf("issue body:\n%s", i.Body)
	}
	clean(t, i.Body)

	r := w.storedRequest(firstRequest)
	if r.Key != "graph pkg_tc pkg_util" || r.State != records.StateOpen || !slices.Equal(r.Waiting, []int{7}) ||
		!r.Numbers.Verified || r.Numbers.Balance == nil || r.Numbers.Balance.Score != 9 || r.Expires != "2026-12-01" {
		t.Errorf("stored request = %+v", r)
	}

	w.wantWaiting(p, "waiting, request #1000")
	reply := w.lastReply(7)
	if !strings.Contains(reply, "- #1000: a new request") || strings.Contains(reply, "retry helper") {
		t.Errorf("reply:\n%s", reply)
	}
	clean(t, reply)
	comment := w.gh.comments[pullKey(shopName, 7)][0].Body
	if !strings.Contains(comment, "## Archfit: waiting, request #1000") || !strings.Contains(comment, "**Owner:** answer #1000.") {
		t.Errorf("check comment:\n%s", comment)
	}
	if rec, _ := w.pullRecord(p); rec.Status != "waiting" {
		t.Errorf("pull record status = %s, want waiting", rec.Status)
	}
}

func TestASecondPullRequestJoinsTheOpenRequest(t *testing.T) {
	w := newAskWorld(t)
	first, second := w.pullBy(7), w.pullBy(8)
	w.uploadAsk(first, "graph", "fail")
	w.ask(first, workerLogin, "/archfit request tc needs the retry helper")
	w.uploadAsk(second, "graph", "waiting") // covered by #1000 before it asks

	w.ask(second, workerLogin, "/archfit request the same need")
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Fatalf("%d issues, want 1", n)
	}
	if r := w.storedRequest(firstRequest); !slices.Equal(r.Waiting, []int{7, 8}) || r.Reason != "tc needs the retry helper" {
		t.Errorf("request = %+v", r)
	}
	if !strings.Contains(w.lastReply(8), "- #1000: the same request was asked; this pull request joins it.") {
		t.Errorf("reply:\n%s", w.lastReply(8))
	}
	// The upload joined #8 in the store alone; its own request line puts it on the issue.
	if body := w.issue(firstRequest).Body; !strings.Contains(body, "- Pull requests: #7, #8\n") {
		t.Errorf("issue body:\n%s", body)
	}
	if note := w.lastReply(firstRequest); note != "archfit: #8 waits on this request.\n" {
		t.Errorf("note on the issue = %q", note)
	}
	w.wantWaiting(second, "waiting, request #1000")

	edits := w.gh.issueEdits
	w.ask(second, workerLogin, "/archfit request again") // a redelivery or a second ask changes nothing more
	if w.gh.issueEdits != edits {
		t.Error("a pull request already waiting edited the issue again")
	}
}

func TestADependencyARuleAlreadyAnswersOpensNoRequest(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail", "pkg_tc>pkg_net")
	w.ask(p, workerLogin, "/archfit request tc needs both")

	reply := w.lastReply(7)
	if !strings.Contains(reply, "- `pkg_tc -> pkg_net`: the rule `no-tc-to-net` already answers it. Instead:\n\n  ```text\n  ask @team, see [docs](https://example.com)\n  ```\n") {
		t.Errorf("reply:\n%s", reply)
	}
	clean(t, reply)
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Errorf("%d issues, want 1: only pkg_tc -> pkg_util is asked", n)
	}
	w.wantFailure(p) // pkg_tc -> pkg_net is not covered
}

func TestARequestLineIsRefusedWithOneReply(t *testing.T) {
	tests := []struct {
		name   string
		golden string
		upload bool
		author string
		perm   string
		body   string
		want   string
	}{
		{"an author without permission", "graph", true, "stranger", "read", "/archfit request why", "a request counts only from the author"},
		{"a deny-mode error", "deny", true, workerLogin, "", "/archfit request why", "pkg/chaos/netem/netem.go:8: no-chaos-to-runtime"},
		{"no result yet", "graph", false, workerLogin, "", "/archfit request why", "no checked result for its head"},
		{"no reason", "graph", true, workerLogin, "", "/archfit request", "a request needs a reason"},
		{"a past date", "graph", true, workerLogin, "", "/archfit request why --expires 2020-01-01", "`--expires` needs a date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			if tt.upload {
				w.uploadAsk(p, tt.golden, "fail")
			}
			if tt.perm != "" {
				w.gh.permissions[shopName+"|"+tt.author] = tt.perm
			}
			before := len(w.gh.comments[pullKey(shopName, 7)])
			w.ask(p, tt.author, tt.body)
			if got := len(w.gh.comments[pullKey(shopName, 7)]) - before; got != 1 {
				t.Fatalf("%d replies, want 1", got)
			}
			reply := w.lastReply(7)
			if !strings.Contains(reply, tt.want) {
				t.Errorf("reply:\n%s\nwant %q", reply, tt.want)
			}
			clean(t, reply)
			if n := len(w.gh.issues[shopName]); n != 0 {
				t.Errorf("%d issues opened", n)
			}
		})
	}
}

func TestARequestLineNeedsTheVerifiedResultOfTheHead(t *testing.T) {
	tests := map[string]func(w *world4, p *pull){
		"an artifact that is not the uploaded bytes": func(w *world4, p *pull) {
			raw := w.gh.artifacts[p.runID]
			w.gh.artifacts[p.runID] = append(slices.Clone(raw), ' ')
		},
		"an artifact that is gone":  func(w *world4, p *pull) { delete(w.gh.artifacts, p.runID) },
		"a result of an older head": func(w *world4, p *pull) { w.push(p) },
	}
	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			breakIt(w, p)
			w.ask(p, workerLogin, "/archfit request tc needs it")
			if reply := w.lastReply(7); !strings.Contains(reply, "no checked result for its head") {
				t.Errorf("reply:\n%s", reply)
			}
			if n := len(w.gh.issues[shopName]); n != 0 {
				t.Errorf("%d issues opened", n)
			}
		})
	}
}

func TestWaitingIsNotPublishedOverARecordDecidedAgain(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	failure, _ := w.latestGate(p.head)
	// The Action's retry of the same run is decided again, unknown, after the request line read the record and
	// before it publishes.
	w.store.beforeLease = func() {
		ctx, tenant := context.Background(), store.Tenant(shopInstallation)
		var rec records.Pull
		if err := records.Load(ctx, w.mem, tenant, records.PullKey(shopName, 7), &rec); err != nil {
			t.Error(err)
			return
		}
		old, _ := records.Encode(&rec)
		rec.Status = "unknown"
		raw, _ := records.Encode(&rec)
		if ok, err := w.mem.CompareAndSwap(ctx, tenant, records.PullKey(shopName, 7), old, raw); !ok || err != nil {
			t.Errorf("swap: %v %v", ok, err)
		}
	}
	w.ask(p, workerLogin, "/archfit request tc needs it")
	if c, _ := w.latestGate(p.head); c.ID != failure.ID || c.Status != runCompleted {
		t.Errorf("latest gate = %+v, want the earlier check untouched", c)
	}
	if rec, _ := w.pullRecord(p); rec.Status != "unknown" {
		t.Errorf("pull record status = %s, want unknown", rec.Status)
	}
}

func TestAFailedTriageIsLoggedAndTheRequestStillCounts(t *testing.T) {
	logs := captureLogs(t)
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.gh.fail["SetAssigneesAndLabels"] = errors.New("github: 503")
	w.ask(p, workerLogin, "/archfit request tc needs it")
	w.wantWaiting(p, "waiting, request #1000")
	if out := logs.String(); !strings.Contains(out, `"msg":"requests triage failed"`) || !strings.Contains(out, `"issue":1000`) ||
		strings.Contains(out, "tc needs it") {
		t.Errorf("logs:\n%s", out)
	}
}

func TestAWriterWhoDidNotWriteThePullRequestMayAsk(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.gh.permissions[shopName+"|maria"] = "write"
	w.ask(p, "maria", "/archfit request tc needs it")
	w.wantWaiting(p, "waiting, request #1000")
}

func TestACommentWithoutARequestLineWritesNothing(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	writes := w.gh.writes()
	w.ask(p, workerLogin, "please /archfit request it for me")
	if w.gh.writes() != writes || len(w.gh.issues[shopName]) != 0 {
		t.Error("a comment without a request line wrote something")
	}
}

func TestOneLineOpensAtMostTenRequests(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	var deps []string
	for i := range 11 {
		deps = append(deps, fmt.Sprintf("pkg_tc>pkg_m%02d", i))
	}
	w.uploadAsk(p, "graph", "fail", deps...)
	w.ask(p, workerLogin, "/archfit request tc needs them")
	if n := len(w.gh.issues[shopName]); n != 10 {
		t.Fatalf("%d issues, want 10", n)
	}
	if reply := w.lastReply(7); !strings.Contains(reply, "- 2 more errors ask for an owner") {
		t.Errorf("reply:\n%s", reply)
	}
	w.wantFailure(p)

	w.ask(p, workerLogin, "/archfit request tc needs them")
	if n := len(w.gh.issues[shopName]); n != 12 {
		t.Fatalf("%d issues after asking again, want 12", n)
	}
	w.wantWaiting(p, "waiting, requests #1000, #1001, #1002, #1003, #1004, #1005, #1006, #1007, #1008, #1009 and 2 more")
}

func TestACoveredPullRequestStaysWaitingAndANewErrorFails(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.ask(p, workerLogin, "/archfit request tc needs the retry helper")

	w.push(p)
	w.uploadAsk(p, "graph", "waiting")
	w.wantWaiting(p, "waiting, request #1000")
	if rec, _ := w.pullRecord(p); rec.Status != "waiting" {
		t.Errorf("pull record status = %s", rec.Status)
	}

	w.push(p)
	w.uploadAsk(p, "graph", "fail", "pkg_tc>pkg_cli")
	w.wantFailure(p)
}

func TestAnAdvisoryPolicyNeverWaits(t *testing.T) {
	w := newWorld4(t) // policy4 has no gate: advisory
	p := w.pullBy(7)
	s := w.spec4(p, "graph")
	w.wantStatus(w.send4(s), "fail", "")
	w.ask(p, workerLogin, "/archfit request tc needs it")
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Fatalf("%d issues, want 1", n)
	}
	if reply := w.lastReply(7); strings.Contains(reply, "waiting") {
		t.Errorf("an advisory reply says the check waits:\n%s", reply)
	}
	w.push(p)
	s = w.spec4(p, "graph")
	w.wantStatus(w.send4(s), "fail", "")
	if c, _ := w.latestGate(p.head); c.Conclusion != "neutral" {
		t.Errorf("gate = %+v, want neutral", c)
	}
}

func TestAnUploadDecidedBeforeARequestIsStale(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	s := w.spec4(p, "graph")
	s.env.PolicyDigest = policy3.Digest([]byte(policyAsk))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	d := w.decide(s)
	u, err := w.gate.v4.receiver.ReceiveV2(context.Background(), w.request(s.uploadSpec))
	if err != nil {
		t.Fatal(err)
	}
	covered, err := w.gate.v4.cover(context.Background(), u, d.decision4)
	if err != nil || covered.Status != "fail" || !covered.Coverable {
		t.Fatalf("cover = %+v, %v", covered, err)
	}
	// A request line opens a request for the error before the upload publishes.
	err = records.Indexed(context.Background(), w.mem, store.Tenant(shopInstallation), records.RequestKey(shopName, 1000),
		records.RequestIndexKey(shopName), records.SchemaRequestIndex, func(_ []byte, x *records.Index) ([]byte, error) {
			if err := x.Add("graph pkg_tc pkg_util", "1000"); err != nil {
				return nil, err
			}
			return records.Encode(&records.Request{Key: "graph pkg_tc pkg_util", State: records.StateOpen})
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.gate.v4.publish(context.Background(), u, covered); !errors.Is(err, feedback.ErrStaleInputs) {
		t.Errorf("publish = %v, want ErrStaleInputs", err)
	}
}

func TestACLIIssueIsARequestAsReported(t *testing.T) {
	const cli = "/archfit request tc needs the retry helper\n\n```text\npkg_tc -> pkg_util: graph: not an allowed dependency\n" +
		"  why: a new dependency between modules needs an owner\n  balance: 8/10 (min 7)\n  next: archfit request -m \"<why>\"\n```\n"
	w := newAskWorld(t)
	w.gh.permissions[shopName+"|"+workerLogin] = "write"
	number := w.cliIssue(cli)
	r := w.storedRequest(number)
	if r.Key != "graph pkg_tc pkg_util" || r.Numbers.Verified || r.Numbers.Balance.Score != 8 || len(r.Waiting) != 0 {
		t.Errorf("request = %+v", r)
	}
	if i := w.issue(number); !slices.Equal(i.Labels, []string{request.Label}) || !i.Open {
		t.Errorf("issue = %+v", i)
	}

	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "waiting") // the pull request is covered by the CLI's request
	w.wantWaiting(p, fmt.Sprintf("waiting, request #%d", number))

	w.ask(p, workerLogin, "/archfit request tc needs the retry helper")
	r = w.storedRequest(number)
	body := w.issue(number).Body
	if !r.Numbers.Verified || r.Numbers.Balance.Score != 9 || !strings.Contains(body, "- Pull requests: #7") || strings.Contains(body, "as reported") {
		t.Errorf("request %+v, body:\n%s", r, body)
	}
	clean(t, body)
}

func TestACLIIssueIsClosedOrRefused(t *testing.T) {
	answered := "/archfit request tc needs the network\n\n```text\npkg_tc -> pkg_net: graph: not an allowed dependency\n```\n"
	asked := "/archfit request tc needs util\n\n```text\npkg_tc -> pkg_util: graph: not an allowed dependency\n```\n"
	t.Run("a deny rule already answers it", func(t *testing.T) {
		w := newAskWorld(t)
		w.gh.permissions[shopName+"|"+workerLogin] = "write"
		n := w.cliIssue(answered)
		if w.issue(n).Open || !strings.Contains(w.lastReply(n), "the rule `no-tc-to-net` already answers this request") {
			t.Errorf("issue open=%v, reply:\n%s", w.issue(n).Open, w.lastReply(n))
		}
		clean(t, w.lastReply(n))
	})
	t.Run("the same request was asked", func(t *testing.T) {
		w := newAskWorld(t)
		w.gh.permissions[shopName+"|"+workerLogin] = "write"
		first := w.cliIssue(asked)
		second := w.cliIssue(asked)
		if w.issue(second).Open || !w.issue(first).Open || w.lastReply(second) != fmt.Sprintf("archfit: the same request was asked in #%d. This issue is closed; follow #%d.\n", first, first) {
			t.Errorf("reply:\n%s", w.lastReply(second))
		}
	})
	t.Run("an author without write permission", func(t *testing.T) {
		w := newAskWorld(t)
		n := w.cliIssue(asked)
		if !w.issue(n).Open || !strings.Contains(w.lastReply(n), "a request counts only") {
			t.Errorf("reply:\n%s", w.lastReply(n))
		}
		var r records.Request
		if err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.RequestKey(shopName, n), &r); !errors.Is(err, records.ErrNoRecord) {
			t.Errorf("a refused issue was stored: %v", err)
		}
	})
}

// cliIssue opens an issue as the CLI does, with the worker's credentials, and delivers its event.
func (w *world4) cliIssue(body string) int {
	w.t.Helper()
	number := w.gh.nextIssue(shopName)
	w.gh.issues[shopName] = append(w.gh.issues[shopName], fakeIssue{Issue: api.Issue{Number: number, Body: body}, Open: true, ByWorker: true})
	w.handle(webhook.Event{Kind: webhook.IssueOpened, DeliveryID: "delivery-issue", InstallationID: shopInstallation,
		Repo: shopName, DefaultBranch: testBranch, Issue: number, Author: workerLogin, AuthorType: "User", Body: body})
	return number
}

func TestAClosedPullRequestLeavesThePullIndex(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
	w.handle(w.event(webhook.PullClosed, p))
	raw, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), records.PullIndexKey(shopName))
	if err != nil {
		t.Fatal(err)
	}
	x := records.Index{Schema: records.SchemaPullIndex}
	if err := records.Decode(raw, &x); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.Lookup(records.PullEntry(7)); ok {
		t.Error("the closed pull request is still in the pull index")
	}
}

func TestRequestCannotReplaceOutsideFlowWithdrawal(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	repo := w.repo4()
	repo.OutsideFlow = &records.OutsideFlow{Digest: "sha256:changed", Issue: 433}
	w.put4(records.RepoKey(shopName), &repo)
	w.sweep4()
	check := w.wantSweepGate(p, "completed", "action_required")
	if !strings.Contains(w.comment(p), "changed on main with no recorded approval") {
		t.Fatalf("comment %s", w.comment(p))
	}
	w.ask(p, workerLogin, "/archfit request tc needs it")
	after := w.wantSweepGate(p, "completed", "action_required")
	if after.ID != check.ID || !strings.Contains(w.comment(p), "changed on main with no recorded approval") {
		t.Fatalf("withdrawal changed: check %+v, comment %s", after, w.comment(p))
	}
	got, err := NewPullViews(pullSummaryGitHub{w.gh}, w.mem).PullPage(t.Context(), w.shop, p.number)
	if err != nil {
		t.Fatal(err)
	}
	if got.View == nil || got.View.Status != "unknown" {
		t.Fatalf("page %+v", got)
	}
}
