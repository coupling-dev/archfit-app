package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const ownerPasskey = "pk-olivia-0001"

var oliviaAdmin = OwnerUser{ID: 501, Login: "olivia", Admin: true}

// onboarding makes the shop a repository the App admitted but that has no policy of version 3 and no workflow on its
// default branch yet, and no record: its onboarding pull request brings both.
func (w *world4) onboarding() {
	w.t.Helper()
	w.removeFile(w.shop, testBranch, policyPath)
	w.removeFile(w.shop, testBranch, ingest.WorkflowPath)
	if err := w.mem.Delete(context.Background(), store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName)); err != nil {
		w.t.Fatal(err)
	}
}

// onboardingPull opens pull request number with policy4 and the workflow at its head, and the upload of its run.
func (w *world4) onboardingPull(number int) (*pull, *spec4) {
	w.t.Helper()
	p := w.openPull(w.shop, number)
	return p, w.onboardingHead(p)
}

// onboardingHead puts policy4 and the workflow at p's head and returns the upload of its run: the run executed that
// workflow from its test merge commit and judged with the head's policy.
func (w *world4) onboardingHead(p *pull) *spec4 {
	w.t.Helper()
	wf := workflow4(actionPinned, " # v4.0.0")
	w.setFile(w.shop, p.head, policyPath, []byte(policy4))
	w.setFile(w.shop, p.head, ingest.WorkflowPath, wf)
	s := w.spec4(p, "pass")
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, wf)
	return s
}

// act binds action a, then makes it as user with a step-up bound to that digest.
func (w *world4) act(user OwnerUser, a OwnerAction) (OwnerOutcome, error) {
	w.t.Helper()
	ctx := context.Background()
	digest, err := w.gate.OwnerDigest(ctx, Target{Repo: w.shop}, a)
	if err != nil {
		return OwnerOutcome{}, err
	}
	return w.gate.OwnerAct(ctx, Target{Repo: w.shop}, OwnerInput{Action: a, User: user, Digest: digest,
		Redeem: func(_ context.Context, got string) (string, error) {
			if got != digest {
				return "", errors.New("the token is for another digest")
			}
			return ownerPasskey, nil
		}})
}

// rerunUpload is the upload of the re-run of s's run: its next attempt, completed.
func (w *world4) rerunUpload(p *pull, s *spec4) answer {
	w.t.Helper()
	w.editRun(p, func(r *api.WorkflowRun) { r.Status = runCompleted })
	s.uploadSpec = w.spec(p, nil)
	s.claims.SHA = w.gh.pulls[pullKey(p.repo.FullName, p.number)].MergeCommitSHA
	s.env.RunAttempt = w.gh.runs[p.runID].Attempt
	return w.send4(s)
}

func TestAFirstApprovalGivesTheOnboardingPullRequestItsVerdict(t *testing.T) {
	w := newWorld4(t)
	w.onboarding()
	p, s := w.onboardingPull(7)
	if a := w.send4(s); a.Reason != reasonFirstPolicy {
		t.Fatalf("before the first approval = %s, want %s", a.raw, reasonFirstPolicy)
	}

	if _, err := w.act(OwnerUser{ID: 502, Login: "oscar"}, OwnerAction{Kind: OwnerFirst, Pull: 7}); !errors.Is(err, ErrOwnerNotAdmin) {
		t.Errorf("a first approval by a user who is not an administrator = %v, want ErrOwnerNotAdmin", err)
	}
	out, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst, Pull: 7})
	if err != nil {
		t.Fatal(err)
	}
	if out.Check != CheckRerun || !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("check %q, reruns %v; want the stored run re-run", out.Check, w.gh.reruns)
	}
	w.wantStatus(w.rerunUpload(p, s), "pass", "")
	if run, ok := w.latestGate(p.head); !ok || run.Conclusion != "success" {
		t.Errorf("archfit/gate on the approved head = %+v, want success", run)
	}

	// A push after the approval needs a new one.
	w.push(p)
	if a := w.send4(w.onboardingHead(p)); a.Reason != reasonFirstPolicy {
		t.Errorf("a later head = %s, want %s", a.raw, reasonFirstPolicy)
	}
}

func TestAFirstApprovalAdmitsOnlyTheWorkflowItWasMadeFor(t *testing.T) {
	w := newWorld4(t)
	w.onboarding()
	_, s := w.onboardingPull(7)
	if _, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst, Pull: 7}); err != nil {
		t.Fatal(err)
	}
	// The run executed another workflow than the head's: the default branch changed it under the test merge.
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, workflow4(actionPinned, " # v4.0.0", "      - run: echo forged\n"))
	if got := w.decide(s).codes(); !slices.Equal(got, []string{reasonWorkflowChanged}) {
		t.Errorf("reasons = %v, want only the workflow", got)
	}
}

func TestAFirstApprovalOnAHeadWhoseRunIsGoneSaysSo(t *testing.T) {
	w := newWorld4(t)
	w.onboarding()
	p, s := w.onboardingPull(7)
	w.send4(s)
	delete(w.gh.runs, p.runID)

	out, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst, Pull: 7})
	if err != nil {
		t.Fatal(err)
	}
	if out.Check != CheckGone {
		t.Fatalf("check = %q, want %q", out.Check, CheckGone)
	}
	run, ok := w.latestGate(p.head)
	if !ok || run.Conclusion != "action_required" {
		t.Fatalf("archfit/gate = %+v, want action_required", run)
	}
	if text := run.Summary; !strings.Contains(text, "this head is approved, and its run cannot run again") ||
		!strings.Contains(text, "run the Archfit check again") {
		t.Errorf("the check does not say what happened and what to do:\n%s", text)
	}
}

// TestAnOnboardingPullRequestClosedWithoutAMerge: its first proposal stays in the index, the repository stays without
// an approved policy, and every reader of the index goes on: a policy that lands another way is first_approval.
func TestAnOnboardingPullRequestClosedWithoutAMerge(t *testing.T) {
	w := newWorld4(t)
	w.onboarding()
	p, _ := w.onboardingPull(7)
	if _, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst, Pull: 7}); err != nil {
		t.Fatal(err)
	}
	w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
	w.setFile(w.shop, testBranch, policyPath, []byte(policy4))
	w.setFile(w.shop, testBranch, ingest.WorkflowPath, workflow4(actionPinned, " # v4.0.0"))

	var repo records.Repo
	err := records.Load(context.Background(), w.mem, store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName), &repo)
	if err != nil && !errors.Is(err, records.ErrNoRecord) || repo.ApprovedDigest != "" {
		t.Fatalf("repository %+v, %v; want no approved policy", repo, err)
	}
	other := w.openPull(w.shop, 8)
	w.wantStatus(w.send4(w.spec4(other, "pass")), "unknown", reasonFirstApproval)

	x := records.Index{Schema: records.SchemaRepos}
	if err := records.Load(context.Background(), w.mem, store.Tenant(w.shop.InstallationID), records.ReposKey, &x); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.Lookup(strings.ToLower(w.shop.FullName)); !ok {
		t.Errorf("repositories = %v, want the shop listed by its first approval", x.Entries)
	}
	if _, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst}); err != nil {
		t.Errorf("the first approval of the policy that landed = %v, want it made", err)
	}
}

func TestTheFirstApprovalOfAnAlreadyConvertedRepositoryGivesItsPullRequestsAVerdict(t *testing.T) {
	w := newWorld4(t)
	if err := w.mem.Delete(context.Background(), store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName)); err != nil {
		t.Fatal(err)
	}
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	w.wantStatus(w.send4(s), "unknown", reasonFirstApproval)

	out, err := w.act(oliviaAdmin, OwnerAction{Kind: OwnerFirst})
	if err != nil {
		t.Fatal(err)
	}
	if out.Issue == 0 {
		t.Error("no issue records the first approval")
	}
	if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("reruns %v, want the run of the pull request without a verdict", w.gh.reruns)
	}
	w.wantStatus(w.rerunUpload(p, s), "pass", "")
}

func TestAWorkflowApprovalGivesItsHeadAVerdict(t *testing.T) {
	w := newWorld4(t)
	w.approvedAt(policy4, w.tip)
	p := w.openPull(w.shop, 7)
	edited := workflow4(actionPinned, " # v4.0.0", "      - run: echo extra\n")
	w.setFile(w.shop, p.head, policyPath, []byte(policy4))
	w.setFile(w.shop, p.head, ingest.WorkflowPath, edited)
	s := w.spec4(p, "pass")
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, edited)
	w.wantStatus(w.send4(s), "unknown", reasonWorkflowChanged)

	out, err := w.act(OwnerUser{ID: 501, Login: "olivia"}, OwnerAction{Kind: OwnerWorkflow, Pull: 7})
	if err != nil {
		t.Fatal(err)
	}
	if out.Check != CheckRerun {
		t.Fatalf("check = %q, want a re-run", out.Check)
	}
	w.wantStatus(w.rerunUpload(p, s), "pass", "")
}

// approvedAt records policy, at commit, as the approved policy of the shop.
func (w *world4) approvedAt(policy, commit string) {
	w.t.Helper()
	raw, err := records.Encode(&records.Repo{ApprovedDigest: policy3.Digest([]byte(policy)), ApprovedCommit: commit, GuardedSince: commit})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName), raw); err != nil {
		w.t.Fatal(err)
	}
}

// TestAWorkflowMergePushConsumesItsApproval: the default-branch push that merges an approved workflow change uses the
// approval, through the push dispatch.
func TestAWorkflowMergePushConsumesItsApproval(t *testing.T) {
	w := newWorld4(t)
	w.approvedAt(policy4, w.tip)
	p := w.openPull(w.shop, 7)
	edited := workflow4(actionPinned, " # v4.0.0", "      - run: echo extra\n")
	w.setFile(w.shop, p.head, policyPath, []byte(policy4))
	w.setFile(w.shop, p.head, ingest.WorkflowPath, edited)
	if _, err := w.act(OwnerUser{ID: 501, Login: "olivia"}, OwnerAction{Kind: OwnerWorkflow, Pull: 7}); err != nil {
		t.Fatal(err)
	}

	merge := w.sha()
	w.setFile(w.shop, merge, policyPath, []byte(policy4))
	w.setFile(w.shop, merge, ingest.WorkflowPath, edited)
	w.editPull(p, func(pl *api.Pull) { pl.State, pl.Merged, pl.MergeCommitSHA = "closed", true, merge })
	e := w.pushEvent(w.shop, ingest.WorkflowPath)
	e.Before, e.HeadSHA = w.tip, merge
	w.handle(e)

	x := records.Index{Schema: records.SchemaProposalIndex}
	t4 := store.Tenant(w.shop.InstallationID)
	if err := records.Load(context.Background(), w.mem, t4, records.ProposalIndexKey(w.shop.FullName), &x); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		t.Fatal(err)
	}
	if len(x.Entries) != 0 {
		t.Errorf("proposal index = %v, want the workflow proposal consumed", x.Entries)
	}
}
