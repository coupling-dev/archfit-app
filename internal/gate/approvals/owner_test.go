package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const passkeyID = "pk-olivia-0001"

var (
	admin   = Owner{ID: 1, Login: "olivia", Admin: true}
	member  = Owner{ID: 2, Login: "oscar"}
	outside = Owner{ID: 3, Login: "mallory"}
)

// mallorysPolicy is a policy a direct push wrote: it names its author as an owner.
const mallorysPolicy = "version: 3\nowners: [mallory]\nmodules:\n  app:\n    paths: [cmd/**]\n"

// bare appends commit, which has no policy, to the default branch.
func (h *hub) bare(commit string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, commit)
}

// approvedAt records digest at commit as the repository's approved policy.
func (w *world) approvedAt(digest, commit string) {
	w.t.Helper()
	raw, err := records.Encode(&records.Repo{ApprovedDigest: digest, ApprovedCommit: commit, GuardedSince: commit})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), w.tenant(), records.RepoKey(testRepo.FullName), raw); err != nil {
		w.t.Fatal(err)
	}
}

// openPull opens pull request pr of this repository into the default branch at head, with policy and workflow there.
func (w *world) openPull(pr int, head, policy, workflow string) {
	w.gh.files[head], w.gh.workflows[head] = policy, workflow
	w.gh.pulls[pr] = api.Pull{Number: pr, State: "open", HeadSHA: head, HeadRepo: testRepo.FullName, BaseRef: "main"}
}

// mergeWith merges pull request pr as commit with policy and workflow: the default branch's tip now.
func (w *world) mergeWith(pr int, commit, policy, workflow string) {
	w.gh.commit(commit, policy)
	w.gh.workflows[commit] = workflow
	w.gh.pulls[pr] = api.Pull{Number: pr, State: "closed", Merged: true, MergeCommitSHA: commit}
}

// act binds action a, then makes it as owner o with that digest. redeemed counts the step-ups it used.
func (w *world) act(o Owner, a Action) (Done, error) {
	w.t.Helper()
	ctx := context.Background()
	c, err := w.svc.Bind(ctx, testRepo, a)
	if err != nil {
		return Done{}, err
	}
	return w.svc.Act(ctx, testRepo, Input{Action: a, Owner: o, Digest: c.Digest(), Redeem: redeem(c.Digest())})
}

func redeem(want string) func(context.Context, string) (string, error) {
	return func(_ context.Context, digest string) (string, error) {
		if digest != want {
			return "", errors.New("the token is for another digest")
		}
		return passkeyID, nil
	}
}

func (w *world) workflowPush(before, after string) Outcome {
	w.t.Helper()
	out, err := w.svc.WorkflowPushed(context.Background(), Push{Repo: testRepo, Before: before, After: after,
		ChangedPaths: []string{WorkflowPath}})
	if err != nil {
		w.t.Fatalf("WorkflowPushed(%s): %v", after, err)
	}
	return out
}

func (w *world) lastComment(number int) string {
	w.t.Helper()
	notes := w.gh.comments[number]
	if len(notes) == 0 {
		w.t.Fatalf("no comment on #%d", number)
	}
	return notes[len(notes)-1]
}

func TestTheFirstApprovalOfAnOnboardingPullRequestIsConsumedByItsMerge(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")

	if _, err := w.act(member, Action{Kind: records.KindFirst, Pull: 12}); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("a first approval by a user who is not an administrator = %v, want ErrNotAdmin", err)
	}
	done, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12})
	if err != nil {
		t.Fatal(err)
	}
	want := Change{Kind: records.KindFirst, Pull: 12, HeadSHA: "h1", Policy: digestV(1), Workflow: records.FileDigest([]byte("wf-1"))}
	if done.Change != want {
		t.Errorf("change = %+v, want %+v", done.Change, want)
	}
	if note := w.lastComment(12); !strings.Contains(note, "`olivia`") || !strings.Contains(note, "`pk-olivi`") {
		t.Errorf("the pull request's record does not name the owner and the passkey:\n%s", note)
	}

	w.mergeWith(12, "m1", policyV(1), "wf-1")
	if out := w.push("c0", "m1"); out.Judged != JudgedConsumed {
		t.Fatalf("the merge push = %+v, want consumed", out)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(1) || r.ApprovedCommit != "m1" || r.GuardedSince != "m1" {
		t.Errorf("repository %+v, want the first policy approved at m1, change control from m1", r)
	}
}

func TestOneFirstApprovalNeverAdmitsAnotherWorkflow(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")
	if _, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12}); err != nil {
		t.Fatal(err)
	}
	// The pull request merged at a later head that kept the approved policy and changed the workflow.
	w.mergeWith(12, "m1", policyV(1), "wf-forged")

	out := w.push("c0", "m1")
	if out.Judged != JudgedUnguarded || out.Voided == "" {
		t.Errorf("outcome %+v, want the first proposal void and the push unguarded", out)
	}
	if r := w.repo(); r.ApprovedDigest != "" {
		t.Errorf("repository %+v, want no approved policy", r)
	}
}

func TestAFirstApprovalIsBoundToWhatTheAdministratorSaw(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")
	ctx := context.Background()
	c, err := w.svc.Bind(ctx, testRepo, Action{Kind: records.KindFirst, Pull: 12})
	if err != nil {
		t.Fatal(err)
	}
	w.openPull(12, "h2", policyV(1), "wf-2") // a push after the administrator looked
	redeemed := false
	_, err = w.svc.Act(ctx, testRepo, Input{Action: Action{Kind: records.KindFirst, Pull: 12}, Owner: admin,
		Digest: c.Digest(), Redeem: func(context.Context, string) (string, error) { redeemed = true; return passkeyID, nil }})
	if !errors.Is(err, ErrMoved) || redeemed {
		t.Errorf("Act = %v, redeemed %v; want ErrMoved before the step-up is used", err, redeemed)
	}
}

func TestAFirstApprovalRefuses(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *world)
		pull  int
		want  error
	}{
		{"an approved repository", func(w *world) { w.approvedAt(digestV(0), "c0") }, 12, ErrNotDue},
		{"a pull request from a fork", func(w *world) {
			w.gh.pulls[12] = api.Pull{Number: 12, State: "open", HeadSHA: "h1", HeadRepo: "fork/shop", BaseRef: "main"}
		}, 12, ErrNotApprovable},
		{"a head without a policy of version 3", func(w *world) { w.openPull(12, "h1", "version: 2\n", "wf-1") }, 12, ErrNotApprovable},
		{"a head without the workflow", func(w *world) {
			w.openPull(12, "h1", policyV(1), "wf-1")
			delete(w.gh.workflows, "h1")
		}, 12, ErrNotApprovable},
		{"a pull request into a branch that has a policy", func(w *world) { w.gh.commit("c1", policyV(0)) }, 12, ErrNotApprovable},
		{"the current policy of a branch without one", func(*world) {}, 0, ErrNotApprovable},
		{"no pull request", func(w *world) { delete(w.gh.pulls, 12) }, 12, ErrNotApprovable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.gh.bare("c0")
			w.openPull(12, "h1", policyV(1), "wf-1")
			tc.setup(w)
			if _, err := w.act(admin, Action{Kind: records.KindFirst, Pull: tc.pull}); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTheFirstApprovalOfAnAlreadyConvertedRepository(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.openPull(12, "h1", policyV(1), "wf-1")

	if _, err := w.act(Owner{ID: 4, Login: "adam", Admin: true}, Action{Kind: records.KindFirst}); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("an administrator the policy does not name = %v, want ErrNotAdmin", err)
	}
	if _, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12}); !errors.Is(err, ErrNotApprovable) {
		t.Errorf("a pull request into a converted branch = %v, want ErrNotApprovable: approve the policy as it is", err)
	}
	done, err := w.act(admin, Action{Kind: records.KindFirst})
	if err != nil {
		t.Fatal(err)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(0) || r.ApprovedCommit != "c0" || r.GuardedSince != "c0" {
		t.Errorf("repository %+v, want the current policy approved at c0, change control from c0", r)
	}
	if done.Issue == 0 || !slices.Equal(w.gh.assignees[done.Issue], []string{"olivia", "oscar"}) {
		t.Errorf("issue %d assigned to %v, want an issue assigned to the owners", done.Issue, w.gh.assignees[done.Issue])
	}
	if body := w.lastComment(done.Issue); !strings.HasPrefix(body, firstTitle) || !strings.Contains(body, "`pk-olivi`") {
		t.Errorf("the issue does not record the approval:\n%s", body)
	}

	// Change control started: a direct push of the policy is outside the flow.
	w.gh.commit("c1", policyV(2))
	w.wantOutside(w.push("c0", "c1"), digestV(2))
}

func TestAWorkflowApprovalIsConsumedByItsMergeOnce(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.gh.workflows["c0"] = "wf-0"
	w.approvedAt(digestV(0), "c0")
	w.openPull(30, "h3", policyV(0), "wf-1")

	if _, err := w.act(outside, Action{Kind: records.KindWorkflow, Pull: 30}); !errors.Is(err, ErrNotOwner) {
		t.Errorf("a workflow approval by a user the policy does not name = %v, want ErrNotOwner", err)
	}
	done, err := w.act(member, Action{Kind: records.KindWorkflow, Pull: 30})
	if err != nil {
		t.Fatal(err)
	}
	if note := w.lastComment(30); !strings.Contains(note, WorkflowPath) || !strings.Contains(note, "`oscar`") {
		t.Errorf("the pull request's record:\n%s", note)
	}

	w.mergeWith(30, "m2", policyV(0), "wf-1")
	if out := w.push("c0", "m2", WorkflowPath); out.Judged != JudgedNone {
		t.Errorf("the policy judgement of a workflow push = %+v, want none", out)
	}
	out := w.workflowPush("c0", "m2")
	if out.Judged != JudgedConsumed {
		t.Fatalf("WorkflowPushed = %+v, want consumed", out)
	}
	if p := w.proposal(out.Proposal); p.State != records.StateMerged || p.Commit != "m2" || p.Kind != records.ProposalWorkflow {
		t.Errorf("proposal %+v, want the workflow proposal merged by m2", p)
	}
	if again := w.workflowPush("c0", "m2"); again.Judged != JudgedNone {
		t.Errorf("the same push again = %+v, want none", again)
	}
	if a := w.approval(done.Approval); a.Proposal != out.Proposal {
		t.Errorf("approval joined %q, want %q", a.Proposal, out.Proposal)
	}
}

func (w *world) approval(id string) records.Approval {
	w.t.Helper()
	var a records.Approval
	if err := records.Load(context.Background(), w.mem, w.tenant(), records.ApprovalKey(testRepo.FullName, id), &a); err != nil {
		w.t.Fatal(err)
	}
	return a
}

func TestAWorkflowMergedWithAnotherFileVoidsItsApproval(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.gh.workflows["c0"] = "wf-0"
	w.approvedAt(digestV(0), "c0")
	w.openPull(30, "h3", policyV(0), "wf-1")
	if _, err := w.act(member, Action{Kind: records.KindWorkflow, Pull: 30}); err != nil {
		t.Fatal(err)
	}
	w.mergeWith(30, "m2", policyV(0), "wf-forged")
	out := w.workflowPush("c0", "m2")
	if out.Judged != JudgedNone || out.Voided == "" {
		t.Fatalf("WorkflowPushed = %+v, want the proposal void", out)
	}
	if p := w.proposal(out.Voided); p.State != records.StateVoid {
		t.Errorf("proposal state %q, want void", p.State)
	}
}

func TestAWorkflowApprovalRefuses(t *testing.T) {
	cases := []struct {
		name     string
		approved bool
		setup    func(w *world)
		want     error
	}{
		{"before the first approval", false, func(*world) {}, ErrNotDue},
		{"a pull request that leaves the workflow as it is", true, func(w *world) { w.gh.workflows["h3"] = "wf-0" }, ErrNotApprovable},
		{"outside the flow", true, func(w *world) {
			w.gh.commit("c1", mallorysPolicy)
			w.push("c0", "c1")
		}, ErrNotDue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.gh.commit("c0", policyV(0))
			w.gh.workflows["c0"] = "wf-0"
			w.openPull(30, "h3", policyV(0), "wf-1")
			if tc.approved {
				w.approvedAt(digestV(0), "c0")
			}
			tc.setup(w)
			if _, err := w.act(member, Action{Kind: records.KindWorkflow, Pull: 30}); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestOnlyAnOwnerOfTheApprovedPolicyConfirms: a direct push wrote a policy that names its author as an owner. That
// author cannot confirm it: the owners are read from the approved policy.
func TestOnlyAnOwnerOfTheApprovedPolicyConfirms(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.approvedAt(digestV(0), "c0")
	w.gh.commit("c1", mallorysPolicy)
	pushed := w.push("c0", "c1")
	w.wantOutside(pushed, policy3.Digest([]byte(mallorysPolicy)))

	if _, err := w.act(outside, Action{Kind: records.KindConfirm}); !errors.Is(err, ErrNotOwner) {
		t.Errorf("a confirm by the owner the direct push added = %v, want ErrNotOwner", err)
	}
	done, err := w.act(member, Action{Kind: records.KindConfirm})
	if err != nil {
		t.Fatal(err)
	}
	if done.Issue != pushed.Issue {
		t.Errorf("confirm ended issue %d, want %d", done.Issue, pushed.Issue)
	}
	r := w.repo()
	if r.OutsideFlow != nil || r.ApprovedDigest != policy3.Digest([]byte(mallorysPolicy)) || r.ApprovedCommit != "c1" {
		t.Errorf("repository %+v, want the confirmed policy approved at c1", r)
	}
	if issues := w.gh.openIssues(); len(issues) != 0 {
		t.Errorf("open issues %v, want the issue of the state closed", issues)
	}
	if note := w.lastComment(pushed.Issue); !strings.Contains(note, "`oscar`") || !strings.Contains(note, "confirmed") {
		t.Errorf("the issue's record:\n%s", note)
	}
	if _, err := w.act(member, Action{Kind: records.KindConfirm}); !errors.Is(err, ErrNotDue) {
		t.Errorf("a confirm inside the flow = %v, want ErrNotDue", err)
	}
}

func TestAConfirmWaitsForTheJudgementOfALaterChange(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.approvedAt(digestV(0), "c0")
	w.gh.commit("c1", policyV(1))
	w.push("c0", "c1")
	w.gh.commit("c2", policyV(2)) // pushed, not judged yet
	if _, err := w.svc.Bind(context.Background(), testRepo, Action{Kind: records.KindConfirm}); !errors.Is(err, ErrMoved) {
		t.Errorf("Bind = %v, want ErrMoved", err)
	}
}

func TestARevertConsumeEndsTheStateAndClosesItsIssue(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.approvedAt(digestV(0), "c0")
	w.gh.commit("c1", policyV(1))
	pushed := w.push("c0", "c1")

	change, err := json.Marshal(records.Revert{Commit: "c0", Digest: digestV(0), Outside: digestV(1), Issue: pushed.Issue})
	if err != nil {
		t.Fatal(err)
	}
	a := records.Approval{Kind: records.KindRevert, Change: change, Owner: 2, Login: "oscar", Credential: passkeyID}
	err = records.RevertTo(context.Background(), w.mem, w.tenant(), testRepo.FullName, "r1", a,
		records.Revert{Commit: "c0", Digest: digestV(0), Outside: digestV(1), Issue: pushed.Issue},
		records.Pin{Proposal: "p9", PullRequest: 40, HeadSHA: "head-revert", Digest: digestV(0)})
	if err != nil {
		t.Fatal(err)
	}
	w.merge(40, "m3", 0)

	if out := w.push("c1", "m3"); out.Judged != JudgedConsumed || out.Proposal != "p9" {
		t.Fatalf("the revert's merge push = %+v, want consumed", out)
	}
	if r := w.repo(); r.OutsideFlow != nil || r.ApprovedDigest != digestV(0) || r.ApprovedCommit != "m3" {
		t.Errorf("repository %+v, want the approved policy back at m3 and the state ended", r)
	}
	if issues := w.gh.openIssues(); len(issues) != 0 {
		t.Errorf("open issues %v, want the issue of the state closed", issues)
	}
	if note := w.lastComment(pushed.Issue); !strings.Contains(note, "#40 restored the approved policy") {
		t.Errorf("the issue's last note:\n%s", note)
	}
}

// TestAFirstApprovalMergedAfterChangeControlStartedIsOutsideTheFlow: two onboarding pull requests were approved; the
// first merge started change control, so the second admits nothing.
func TestAFirstApprovalMergedAfterChangeControlStartedIsOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")
	w.openPull(13, "h2", policyV(2), "wf-2")
	if _, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 13}); err != nil {
		t.Fatal(err)
	}
	w.mergeWith(12, "m1", policyV(1), "wf-1")
	if out := w.push("c0", "m1"); out.Judged != JudgedConsumed {
		t.Fatalf("the first merge = %+v, want consumed", out)
	}

	w.mergeWith(13, "m2", policyV(2), "wf-2")
	out := w.push("m1", "m2")
	w.wantOutside(out, digestV(2))
	if out.Voided == "" || w.proposal(out.Voided).State != records.StateVoid {
		t.Errorf("outcome %+v, want the second first proposal void", out)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(1) {
		t.Errorf("approved digest %q, want the first merge's", r.ApprovedDigest)
	}
}

// TestARevertMergedAfterAConfirmIsOutsideTheFlow: an owner reverted to merge later, then another confirmed; the
// revert's merge would undo the confirm, so it admits nothing.
func TestARevertMergedAfterAConfirmIsOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.approvedAt(digestV(0), "c0")
	w.gh.commit("c1", policyV(1))
	pushed := w.push("c0", "c1")
	err := records.RevertTo(context.Background(), w.mem, w.tenant(), testRepo.FullName, "r1",
		records.Approval{Owner: 2, Login: "oscar", Credential: passkeyID},
		records.Revert{Commit: "c0", Digest: digestV(0), Outside: digestV(1), Issue: pushed.Issue},
		records.Pin{Proposal: "p9", PullRequest: 40, HeadSHA: "head-revert", Digest: digestV(0)})
	if err != nil {
		t.Fatal(err)
	}
	w.gh.pulls[40] = api.Pull{Number: 40, State: "open"}
	if _, err := w.act(member, Action{Kind: records.KindConfirm}); err != nil {
		t.Fatal(err)
	}

	w.merge(40, "m3", 0)
	out := w.push("c1", "m3")
	w.wantOutside(out, digestV(0))
	if out.Voided != "p9" {
		t.Errorf("outcome %+v, want the revert's proposal void", out)
	}
}
