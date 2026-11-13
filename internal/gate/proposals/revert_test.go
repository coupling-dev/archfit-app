package proposals

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// mallorysPolicy is what a direct push wrote: it names its author as the owner.
const mallorysPolicy = "version: 3\nowners: [mallory]\nmodules:\n  pkg_tc:\n    paths: [pkg/tc/**]\n"

var mallory = Owner{ID: 666, Login: "mallory"}

// outsideTheFlow records the start policy as approved, then pushes mallorysPolicy straight to the default branch and
// records the state as the push judgement does, with issue 70.
func (w *world) outsideTheFlow() { w.outsideWith(mallorysPolicy) }

// outsideWith is outsideTheFlow with policy pushed; "" deletes the policy.
func (w *world) outsideWith(policy string) {
	w.t.Helper()
	approved := w.hub.branches[fakeBase]
	digest := ""
	if policy == "" {
		w.hub.remove(fakeBase, policyPath)
	} else {
		w.hub.push(fakeBase, policyPath, policy)
		digest = policy3.Digest([]byte(policy))
	}
	w.putRepo(records.Repo{ApprovedDigest: policy3.Digest([]byte(testPolicy)), ApprovedCommit: approved, GuardedSince: approved,
		OutsideFlow: &records.OutsideFlow{Digest: digest, Issue: 70}})
	w.hub.issues[70] = true
}

// remove moves branch to a new commit without path, as a person's push that deletes it does.
func (h *fakeHub) remove(branch, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	head := h.branches[branch]
	files := maps.Clone(h.commits[head].files)
	delete(files, path)
	h.branches[branch] = h.put(head, "push", files)
	h.follow(branch)
}

func (w *world) putRepo(r records.Repo) {
	w.t.Helper()
	raw, err := records.Encode(&r)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.store.Put(context.Background(), tenant(testRepo), records.RepoKey(testRepo.FullName), raw); err != nil {
		w.t.Fatal(err)
	}
}

// revert makes the step-up of a revert and reverts as owner.
func (w *world) revert(owner Owner) (Outcome, error) {
	w.t.Helper()
	digest, err := w.svc.RevertDigest(context.Background(), testRepo)
	if err != nil {
		return Outcome{}, err
	}
	w.mu.Lock()
	w.tokens["revert-token"] = digest
	w.mu.Unlock()
	return w.svc.Revert(context.Background(), testRepo, RevertInput{Owner: owner, Digest: digest, Redeem: w.redeem("revert-token")})
}

// TestAnAnswerOutsideTheFlowIsRefused: an owner a direct push added, and an owner of the approved policy, both wait
// for a confirm or a revert; nothing is written and no step-up is used.
func TestAnAnswerOutsideTheFlowIsRefused(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.outsideTheFlow()
	before := w.snapshot()
	approve := requests.Answer{Kind: policy3.Approve}

	if _, err := w.svc.Digest(context.Background(), testRepo, 431, approve); !errors.Is(err, ErrOutsideFlow) {
		t.Errorf("Digest = %v, want ErrOutsideFlow", err)
	}
	for _, owner := range []Owner{mallory, olivia} {
		in := Input{Request: 431, Answer: approve, Owner: owner, Redeem: w.redeem("none")}
		if _, err := w.svc.Answer(context.Background(), testRepo, in); !errors.Is(err, ErrOutsideFlow) {
			t.Errorf("answer by %s = %v, want ErrOutsideFlow", owner.Login, err)
		}
	}
	if !maps.Equal(before, w.snapshot()) || len(w.redeemed) != 0 || len(w.hub.writes()) != 0 {
		t.Errorf("a refused answer wrote: redeemed %v, GitHub %v", w.redeemed, w.hub.writes())
	}
}

func TestARevertRestoresTheApprovedPolicyWithTheOpenAnswers(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	later := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.outsideTheFlow()

	out, err := w.revert(oscar)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != requests.PullMerged || out.PullRequest != later.PullRequest {
		t.Fatalf("outcome = %+v, want the policy pull request #%d merged", out, later.PullRequest)
	}
	got := w.hub.file(fakeBase, policyPath)
	if !strings.Contains(got, "owners: [olivia, oscar]") || !strings.Contains(got, "- pkg_util  # request #431") {
		t.Errorf("the policy on main is not the approved one with the open answer:\n%s", got)
	}
	a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
	if a.Kind != records.KindRevert || a.Login != "Oscar" || a.Credential != "cred-revert-token" || a.Request != 0 {
		t.Errorf("approval = %+v", a)
	}
	if p := w.proposalOf(out); len(p.Approvals) != 2 || p.ExpectedDigest != policy3.Digest([]byte(got)) {
		t.Errorf("proposal = %+v, want the answer and the revert, expecting the merged policy", p)
	}
	note := w.hub.lastComment(70)
	if !strings.Contains(note, "`Oscar`") || !strings.Contains(note, "`cred-rev`") || !strings.Contains(note, "it merged") {
		t.Errorf("the issue of the state does not record the revert:\n%s", note)
	}
	if body := w.hub.pulls[out.PullRequest].Body; !strings.Contains(body, "| none | revert to the policy approved at ") {
		t.Errorf("the pull request does not list the revert:\n%s", body)
	}
}

// TestOnlyAnOwnerOfTheApprovedPolicyReverts: the owner a direct push named is not an owner of the approved policy.
func TestOnlyAnOwnerOfTheApprovedPolicyReverts(t *testing.T) {
	w := newWorld(t)
	w.outsideTheFlow()
	if _, err := w.revert(mallory); !errors.Is(err, ErrNotOwner) {
		t.Errorf("a revert by the owner the push added = %v, want ErrNotOwner", err)
	}
	if len(w.redeemed) != 0 || len(w.hub.writes()) != 0 {
		t.Errorf("a refused revert wrote: redeemed %v, GitHub %v", w.redeemed, w.hub.writes())
	}
}

func TestARevertRefuses(t *testing.T) {
	t.Run("inside the flow", func(t *testing.T) {
		w := newWorld(t)
		if _, err := w.revert(olivia); !errors.Is(err, ErrNotOutside) {
			t.Errorf("err = %v, want ErrNotOutside", err)
		}
	})
	t.Run("the policy changed again since the owner looked", func(t *testing.T) {
		w := newWorld(t)
		w.outsideTheFlow()
		digest, err := w.svc.RevertDigest(context.Background(), testRepo)
		if err != nil {
			t.Fatal(err)
		}
		r := load[records.Repo](t, w.store, records.RepoKey(testRepo.FullName))
		r.OutsideFlow.Digest = "sha256:later"
		w.putRepo(r)
		_, err = w.svc.Revert(context.Background(), testRepo, RevertInput{Owner: olivia, Digest: digest, Redeem: w.redeem("none")})
		if !errors.Is(err, ErrMoved) {
			t.Errorf("err = %v, want ErrMoved", err)
		}
	})
	t.Run("the default branch holds the approved policy", func(t *testing.T) {
		w := newWorld(t)
		w.outsideTheFlow()
		w.hub.push(fakeBase, policyPath, testPolicy) // put back by hand, and judged outside the flow again
		r := load[records.Repo](t, w.store, records.RepoKey(testRepo.FullName))
		r.OutsideFlow.Digest = policy3.Digest([]byte(testPolicy))
		w.putRepo(r)
		if _, err := w.revert(olivia); !errors.Is(err, ErrNothingToRevert) {
			t.Errorf("err = %v, want ErrNothingToRevert", err)
		}
	})
}

// TestAProposalOfAPullRequestSomeoneElseWroteIsNotThePolicyPullRequest: a first approval's proposal does not stop an
// answer, and its pull request gets its verdict from its uploads.
func TestAProposalOfAPullRequestSomeoneElseWroteIsNotThePolicyPullRequest(t *testing.T) {
	w := newWorld(t)
	a := records.Approval{Kind: records.KindFirst, Owner: 1, Login: "admin", Credential: "cred-1", At: testNow}
	b := records.Bound{Proposal: "pf", PullRequest: 12, HeadSHA: "h1", Digest: "sha256:p", WorkflowDigest: "sha256:w"}
	if err := records.First(context.Background(), w.store, tenant(testRepo), testRepo.FullName, "af", a, b); err != nil {
		t.Fatal(err)
	}
	w.request(431, "graph", "pkg_tc", "pkg_util")
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)

	ctx := context.Background()
	if own, err := IsProposalPull(ctx, w.store, testRepo, 12); err != nil || own {
		t.Errorf("IsProposalPull(onboarding) = %v, %v; want false", own, err)
	}
	if own, err := IsProposalPull(ctx, w.store, testRepo, out.PullRequest); err != nil || !own {
		t.Errorf("IsProposalPull(policy) = %v, %v; want true", own, err)
	}
}

func TestARevertRestoresADeletedPolicy(t *testing.T) {
	w := newWorld(t)
	w.outsideWith("")
	out, err := w.revert(oscar)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != requests.PullMerged || w.hub.file(fakeBase, policyPath) != testPolicy {
		t.Errorf("outcome %+v, policy on main:\n%s\nwant the approved policy back", out, w.hub.file(fakeBase, policyPath))
	}
}

// TestARevertIsBoundToTheChangeTheOwnerSaw: a later push, not judged yet, changed the policy again.
func TestARevertIsBoundToTheChangeTheOwnerSaw(t *testing.T) {
	w := newWorld(t)
	w.outsideTheFlow()
	digest, err := w.svc.RevertDigest(context.Background(), testRepo)
	if err != nil {
		t.Fatal(err)
	}
	w.hub.push(fakeBase, policyPath, mallorysPolicy+"# again\n")
	w.mu.Lock()
	w.tokens["revert-token"] = digest
	w.mu.Unlock()
	_, err = w.svc.Revert(context.Background(), testRepo, RevertInput{Owner: oscar, Digest: digest, Redeem: w.redeem("revert-token")})
	if !errors.Is(err, ErrMoved) || len(w.redeemed) != 0 {
		t.Errorf("Revert = %v, redeemed %v; want ErrMoved with the step-up unused", err, w.redeemed)
	}
}

// TestAConfirmEndsAnOpenRevert: an owner reverted to merge later; another confirmed the change. The next answer is
// written on the confirmed policy, and the revert is void.
func TestAConfirmEndsAnOpenRevert(t *testing.T) {
	w := newWorld(t)
	edited := testPolicy + "# edited by hand\n"
	w.outsideWith(edited)
	digest, err := w.svc.RevertDigest(context.Background(), testRepo)
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.tokens["revert-token"] = digest
	w.mu.Unlock()
	reverted, err := w.svc.Revert(context.Background(), testRepo, RevertInput{Owner: oscar, Digest: digest, Later: true,
		Redeem: w.redeem("revert-token")})
	if err != nil {
		t.Fatal(err)
	}
	w.putRepo(records.Repo{ApprovedDigest: policy3.Digest([]byte(edited)), ApprovedCommit: w.hub.branches[fakeBase]}) // confirmed

	w.request(431, "graph", "pkg_tc", "pkg_util")
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	if got := w.hub.file(fakeBase, policyPath); !strings.Contains(got, "# edited by hand") || !strings.Contains(got, "# request #431") {
		t.Errorf("the answer was not written on the confirmed policy:\n%s", got)
	}
	if a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, reverted.Approval)); a.State != records.StateVoid {
		t.Errorf("the revert is %q, want void", a.State)
	}
	if p := w.proposalOf(out); slices.Contains(p.Approvals, reverted.Approval) {
		t.Errorf("proposal %+v still lists the revert", p)
	}
}
