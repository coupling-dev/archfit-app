package gate

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// policyPush is a push to the default branch from before to after that changes the policy among other files.
func (w *world4) policyPush(before, after string) webhook.Event {
	e := w.pushEvent(w.shop, "pkg/a.go", policyPath)
	e.Before, e.HeadSHA = before, after
	return e
}

func (w *world4) comment(p *pull) string {
	w.t.Helper()
	list := w.gh.comments[pullKey(shopName, p.number)]
	if len(list) == 0 {
		w.t.Fatalf("no comment on #%d", p.number)
	}
	return list[0].Body
}

// TestACodePullRequestThatEditsThePolicyIsUnknown: the engine reports the policy of the change differs from the
// default branch's (design 9.2: a code change that also edits the policy); the App shows that unknown as it is.
func TestACodePullRequestThatEditsThePolicyIsUnknown(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.setFile(w.shop, p.head, policyPath, []byte(policy4+"  extra:\n    paths: [x/**]\n"))
	w.wantStatus(w.send4(w.spec4(p, "policy_differs")), "unknown", "")
	w.wantGate(p, "completed", "action_required")
	if c := w.comment(p); !strings.Contains(c, "## Archfit: unknown") || !strings.Contains(c, "differs from origin/main") ||
		!strings.Contains(c, "git checkout origin/main -- .archfit.yaml") {
		t.Errorf("comment:\n%s", c)
	}
}

// TestAPolicyNoOwnerApprovedGivesNoVerdict: a schema 3 policy with no approval recorded is not change control yet, so
// no upload passes until an administrator approves it (task 23).
func TestAPolicyNoOwnerApprovedGivesNoVerdict(t *testing.T) {
	w := newWorld4(t)
	if err := w.mem.Delete(context.Background(), store.Tenant(shopInstallation), records.RepoKey(shopName)); err != nil {
		t.Fatal(err)
	}
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "unknown", reasonFirstApproval)
	w.wantGate(p, "completed", "action_required")
	if c := w.comment(p); !strings.Contains(c, "has no recorded approval on main") ||
		!strings.Contains(c, "an administrator approves the policy in the App") {
		t.Errorf("comment:\n%s", c)
	}
}

// TestADirectPushPutsEveryUploadOutsideTheFlow: a push that changes the policy with no recorded approval opens one
// issue to the owners, and every upload is then unknown with a record that names it.
func TestADirectPushPutsEveryUploadOutsideTheFlow(t *testing.T) {
	w := newWorld4(t)
	after := w.sha()
	w.setFile(w.shop, after, policyPath, []byte(policy4+"# edited by hand\n"))
	w.handle(w.policyPush(w.tip, after))
	w.handle(w.policyPush(w.tip, after)) // delivered twice

	issues := w.gh.issues[shopName]
	if len(issues) != 1 || issues[0].Title != "archfit: the policy changed outside the flow" || !issues[0].Open ||
		!slices.Equal(issues[0].Assignees, []string{"olivia"}) || !strings.Contains(issues[0].Body, after) {
		t.Fatalf("issues = %+v, want one open issue to the owners naming the commit", issues)
	}
	clean(t, issues[0].Body)

	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "unknown", reasonOutsideFlow)
	w.wantGate(p, "completed", "action_required")
	if c := w.comment(p); !strings.Contains(c, "changed on main with no recorded approval") ||
		!strings.Contains(c, "an owner confirms or reverts it in the App: #"+strconv.Itoa(issues[0].Number)) {
		t.Errorf("comment:\n%s", c)
	}
}

// TestAnAnsweredRequestCoversItsKeyUntilItsProposalIsConsumed is the release-blocking item of task 19's review: during
// "merge later" a pull request failing on the answered key waits and asking again opens no second issue; the merge
// push consumes the proposal and frees the key.
func TestAnAnsweredRequestCoversItsKeyUntilItsProposalIsConsumed(t *testing.T) {
	ctx := context.Background()
	w := newAskWorld(t)
	asked, later := w.pullBy(7), w.pullBy(8)
	w.uploadAsk(asked, "graph", "fail")
	w.ask(asked, workerLogin, "/archfit request tc needs the retry helper")

	// An owner approves and chooses "merge later": gate/proposals records it so (its branch writes are tested there).
	answered := policyAsk + "# request #1000\n"
	const policyPull = 30
	a := records.Approval{Kind: records.KindApprove, Request: firstRequest, Change: json.RawMessage(`{}`), Owner: 3, Login: "olivia"}
	pin := &records.Pin{Proposal: "p1", PullRequest: policyPull, HeadSHA: w.sha(), Digest: policy3.Digest([]byte(answered))}
	if err := records.Answer(ctx, w.mem, store.Tenant(shopInstallation), shopName, "a1", a, pin); err != nil {
		t.Fatal(err)
	}

	w.uploadAsk(later, "graph", "waiting")
	w.wantWaiting(later, "waiting, request #1000")
	w.ask(later, workerLogin, "/archfit request the same need")
	if n := len(w.gh.issues[shopName]); n != 1 {
		t.Fatalf("%d issues, want the answered request only", n)
	}
	if r := w.storedRequest(firstRequest); r.State != records.StateAnswered || !slices.Equal(r.Waiting, []int{7, 8}) {
		t.Errorf("request = %+v, want answered with #7 and #8 waiting", r)
	}

	merge := w.sha()
	w.gh.pulls[pullKey(shopName, policyPull)] = api.Pull{Number: policyPull, State: "closed", Merged: true, MergeCommitSHA: merge}
	w.setFile(w.shop, merge, policyPath, []byte(answered))
	// The merge moved main in the policy only, so the waiting pull requests can be re-run (design 10.5).
	w.gh.heads[branchKey(shopName, testBranch)] = merge
	for _, n := range []int{7, 8} {
		base := w.gh.pulls[pullKey(shopName, n)].BaseSHA
		w.gh.compares[shopName+"|"+base+"|"+merge] = api.Comparison{Status: "ahead", Paths: []string{policyPath}}
	}
	w.handle(w.policyPush(w.tip, merge))
	w.tip = merge

	var p records.Proposal
	if err := records.Load(ctx, w.mem, store.Tenant(shopInstallation), records.ProposalKey(shopName, "p1"), &p); err != nil ||
		p.State != records.StateMerged || p.Commit != merge {
		t.Fatalf("proposal = %+v, %v; want merged by the push", p, err)
	}
	if len(w.gh.issues[shopName]) != 1 {
		t.Errorf("issues = %+v, want no issue for a consumed proposal", w.gh.issues[shopName])
	}
	// The consume hands both waiting pull requests to the re-check after an answer (task 21): each latest run runs again.
	for _, n := range []int{7, 8} {
		var rec records.Pull
		if err := records.Load(ctx, w.mem, store.Tenant(shopInstallation), records.PullKey(shopName, n), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Recheck == nil || rec.Recheck.Proposal != "p1" || rec.Recheck.Path != "rerun" {
			t.Errorf("#%d recheck = %+v, want a re-run for p1", n, rec.Recheck)
		}
	}
	if len(w.gh.reruns) != 2 {
		t.Errorf("re-runs = %v, want the latest run of #7 and #8", w.gh.reruns)
	}
	notes := w.gh.comments[pullKey(shopName, firstRequest)]
	if len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "#7: its check runs again") ||
		!strings.Contains(notes[len(notes)-1].Body, "#8: its check runs again") {
		t.Errorf("request comments = %+v, want #7 and #8 told", notes)
	}
	// The key is free: no answered request covers it now. The run reads the merged policy from the new tip.
	third := w.spec4(w.pullBy(9), "graph")
	third.env.PolicyDigest = policy3.Digest([]byte(answered))
	third.result["policy"].(map[string]any)["digest"] = third.env.PolicyDigest
	w.wantStatus(w.send4(third), "fail", "")
}

// TestAnUploadBeforeItsPushIsJudgedGivesNoVerdict: the policy on the default branch changed and no judgement reached
// it yet (the push is late or lost): the upload is unknown, not a verdict under a policy no owner approved; once the
// push is judged outside the flow, the record names the issue.
func TestAnUploadBeforeItsPushIsJudgedGivesNoVerdict(t *testing.T) {
	w := newWorld4(t)
	byHand := policy4 + "# edited by hand\n"
	w.setFile(w.shop, testBranch, policyPath, []byte(byHand))
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	s.env.PolicyDigest = policy3.Digest([]byte(byHand))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	w.wantStatus(w.send4(s), "unknown", reasonPolicyUnapproved)

	after := w.sha()
	w.setFile(w.shop, after, policyPath, []byte(byHand))
	w.setFile(w.shop, w.tip+"-before", policyPath, []byte(policy4))
	w.handle(w.policyPush(w.tip+"-before", after))
	w.push(p)
	s = w.spec4(p, "pass")
	s.env.PolicyDigest = policy3.Digest([]byte(byHand))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	w.wantStatus(w.send4(s), "unknown", reasonOutsideFlow)
}
