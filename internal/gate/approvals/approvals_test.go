package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

var testRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, ID: 9, DefaultBranch: "main"}

// policyV is a schema 3 policy; n makes each version's bytes, and so its digest, distinct.
func policyV(n int) string {
	return "version: 3\nowners: [olivia, \"@oscar\", acme/arch]\nmodules:\n  app:\n    paths: [cmd/**]\n# v" + strconv.Itoa(n) + "\n"
}

func digestV(n int) string { return policy3.Digest([]byte(policyV(n))) }

// hub is a fake GitHub: the policy at each commit, pull requests, and the issues the App opens.
type hub struct {
	mu        sync.Mutex
	files     map[string]string // commit → policy; a missing commit has no policy
	pulls     map[int]api.Pull
	issues    map[int]bool // number → open
	comments  map[int][]string
	assignees map[int][]string
	next      int
	fail      map[string]error
}

func (h *hub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fail["File"]; err != nil {
		return nil, "", err
	}
	text, ok := h.files[ref]
	if !ok || path != PolicyPath {
		return nil, "", api.ErrNotFound
	}
	return []byte(text), "blob-" + ref, nil
}

func (h *hub) Pull(_ context.Context, _ api.RepoRef, number int) (api.Pull, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.pulls[number]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return p, nil
}

func (h *hub) CreateIssue(_ context.Context, _ api.RepoRef, i api.NewIssue) (api.Issue, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fail["CreateIssue"]; err != nil {
		return api.Issue{}, err
	}
	h.next++
	h.issues[h.next] = true
	h.comments[h.next] = append(h.comments[h.next], i.Title+"\n"+i.Body)
	return api.Issue{Number: h.next, Body: i.Body}, nil
}

func (h *hub) CreateComment(_ context.Context, _ api.RepoRef, number int, body string) (api.Comment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.comments[number] = append(h.comments[number], body)
	return api.Comment{Body: body}, nil
}

func (h *hub) CloseIssue(_ context.Context, _ api.RepoRef, number int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.issues[number] = false
	return nil
}

func (h *hub) ReopenIssue(_ context.Context, _ api.RepoRef, number int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.issues[number] = true
	return nil
}

func (h *hub) SetAssigneesAndLabels(_ context.Context, _ api.RepoRef, number int, assignees, labels []string) ([]string, []string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.assignees[number] = assignees
	return assignees, labels, nil
}

// openIssues are the open issues the App opened (numbers from 1000), not the request issues.
func (h *hub) openIssues() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []int
	for n, open := range h.issues {
		if open && n > 1000 {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

type world struct {
	t   *testing.T
	gh  *hub
	mem store.Store
	svc *Service
}

func newWorld(t *testing.T) *world {
	gh := &hub{files: map[string]string{}, pulls: map[int]api.Pull{}, issues: map[int]bool{}, comments: map[int][]string{},
		assignees: map[int][]string{}, next: 1000, fail: map[string]error{}}
	mem := store.NewMemory()
	return &world{t: t, gh: gh, mem: mem, svc: New(gh, mem)}
}

func (w *world) tenant() store.Tenant { return store.Tenant(testRepo.InstallationID) }

// approved records digest as the repository's approved policy: the repository is under change control.
func (w *world) approved(digest string) {
	w.t.Helper()
	raw, err := records.Encode(&records.Repo{ApprovedDigest: digest})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), w.tenant(), records.RepoKey(testRepo.FullName), raw); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) repo() records.Repo {
	w.t.Helper()
	var r records.Repo
	if err := records.Load(context.Background(), w.mem, w.tenant(), records.RepoKey(testRepo.FullName), &r); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		w.t.Fatal(err)
	}
	return r
}

// answered opens request number with key and records its answer joining proposal on pull request pr, pinned at
// digest, as gate/proposals records an answer (records.Answer). waiting are the pull requests waiting on it.
func (w *world) answered(number int, key, proposal string, pr int, digest string, waiting ...int) string {
	w.t.Helper()
	ctx := context.Background()
	err := records.Indexed(ctx, w.mem, w.tenant(), records.RequestKey(testRepo.FullName, number),
		records.RequestIndexKey(testRepo.FullName), records.SchemaRequestIndex, func(_ []byte, x *records.Index) ([]byte, error) {
			if err := x.Add(key, strconv.Itoa(number)); err != nil {
				return nil, err
			}
			return records.Encode(&records.Request{Key: key, Reason: "why", State: records.StateOpen, Waiting: waiting})
		})
	if err != nil {
		w.t.Fatal(err)
	}
	id := "a" + strconv.Itoa(number)
	a := records.Approval{Kind: records.KindApprove, Request: number, Change: json.RawMessage(`{}`), Owner: 1, Login: "olivia"}
	pin := &records.Pin{Proposal: proposal, PullRequest: pr, HeadSHA: "head-" + digest, Digest: digest}
	if err := records.Answer(ctx, w.mem, w.tenant(), testRepo.FullName, id, a, pin); err != nil {
		w.t.Fatal(err)
	}
	w.gh.issues[number] = false // the answer closed the request issue
	return id
}

// merge merges pull request pr as commit, whose policy is policyV(n).
func (w *world) merge(pr int, commit string, n int) {
	w.gh.files[commit] = policyV(n)
	w.gh.pulls[pr] = api.Pull{Number: pr, State: "closed", Merged: true, MergeCommitSHA: commit}
}

func (w *world) push(before, after string, paths ...string) Outcome {
	w.t.Helper()
	out, err := w.svc.Pushed(context.Background(), Push{Repo: testRepo, Before: before, After: after, ChangedPaths: paths})
	if err != nil {
		w.t.Fatalf("Pushed(%s): %v", after, err)
	}
	return out
}

func (w *world) proposal(id string) records.Proposal {
	w.t.Helper()
	var p records.Proposal
	if err := records.Load(context.Background(), w.mem, w.tenant(), records.ProposalKey(testRepo.FullName, id), &p); err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) requestIndex() []records.Entry {
	w.t.Helper()
	x := records.Index{Schema: records.SchemaRequestIndex}
	if err := records.Load(context.Background(), w.mem, w.tenant(), records.RequestIndexKey(testRepo.FullName), &x); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		w.t.Fatal(err)
	}
	return x.Entries
}

func (w *world) wantOutside(out Outcome, digest string) {
	w.t.Helper()
	if out.Judged != JudgedOutside || out.Issue == 0 {
		w.t.Fatalf("outcome %+v, want outside the flow with an issue", out)
	}
	if r := w.repo(); r.OutsideFlow == nil || r.OutsideFlow.Digest != digest || r.OutsideFlow.Issue != out.Issue {
		w.t.Errorf("repository %+v, want outside the flow at the pushed digest with issue %d", r, out.Issue)
	}
}

// TestTwoJoinedAnswersAreConsumedByOnePush: "merge later", then a second answer joins and the pull request merges.
func TestTwoJoinedAnswersAreConsumedByOnePush(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	w.answered(431, "graph a b", "p1", 21, digestV(1), 5)
	w.answered(432, "graph c d", "p1", 21, digestV(2), 5, 6)
	w.merge(21, "c1", 2)

	out := w.push("c0", "c1")
	if out.Judged != JudgedConsumed || out.Proposal != "p1" || !slices.Equal(out.Requests, []int{431, 432}) ||
		!slices.Equal(out.Waiting, []int{5, 6}) {
		t.Errorf("outcome %+v, want p1 consumed with both requests and their waiting pull requests", out)
	}
	if p := w.proposal("p1"); p.State != records.StateMerged || p.Commit != "c1" {
		t.Errorf("proposal %+v, want merged by c1", p)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(2) || r.OutsideFlow != nil {
		t.Errorf("repository %+v, want the merged policy approved", r)
	}
	if idx := w.requestIndex(); len(idx) != 0 {
		t.Errorf("request index %v, want both requests out once consumed", idx)
	}
	if issues := w.gh.openIssues(); len(issues) != 0 {
		t.Errorf("issues opened %v", issues)
	}
}

// TestARewrittenProposalIsConsumed: the target moved, the App rewrote the branch from the new tip and pinned a new
// head and digest; the merge of that head consumes, the merge of the head before would not.
func TestARewrittenProposalIsConsumed(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	a := w.answered(431, "graph a b", "p1", 21, digestV(1))
	if err := records.Join(context.Background(), w.mem, w.tenant(), testRepo.FullName, a, "p1", "head-rewritten", digestV(3)); err != nil {
		t.Fatal(err)
	}
	w.merge(21, "c1", 3)
	if out := w.push("c0", "c1"); out.Judged != JudgedConsumed || out.Proposal != "p1" {
		t.Errorf("outcome %+v, want the rewritten proposal consumed", out)
	}
}

// TestTheSamePushTwice: a delivery of the same push again changes nothing, also after another push was consumed; an
// outside-the-flow push again opens no second issue and adds no note.
func TestTheSamePushTwice(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	w.answered(431, "graph a b", "p1", 21, digestV(1), 5)
	w.merge(21, "c1", 1)
	first := w.push("c0", "c1")
	if again := w.push("c0", "c1"); !equalOutcome(first, again) {
		t.Errorf("again = %+v, want %+v", again, first)
	}

	w.answered(432, "graph c d", "p2", 22, digestV(2))
	w.merge(22, "c2", 2)
	if out := w.push("c1", "c2"); out.Judged != JudgedConsumed || out.Proposal != "p2" {
		t.Fatalf("second consume = %+v", out)
	}
	if late := w.push("c0", "c1"); !equalOutcome(first, late) {
		t.Errorf("a late delivery = %+v, want %+v", late, first)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(2) || r.OutsideFlow != nil {
		t.Errorf("repository %+v after a late delivery, want p2's policy approved and inside the flow", r)
	}

	w.gh.files["c3"] = policyV(9)
	out := w.push("c2", "c3")
	w.wantOutside(out, digestV(9))
	notes := len(w.gh.comments[out.Issue])
	if again := w.push("c2", "c3"); again.Judged != JudgedOutside || again.Issue != out.Issue {
		t.Errorf("again = %+v, want the same issue", again)
	}
	if issues := w.gh.openIssues(); !slices.Equal(issues, []int{out.Issue}) || len(w.gh.comments[out.Issue]) != notes {
		t.Errorf("issues %v with %d notes, want one issue and no new note", issues, len(w.gh.comments[out.Issue]))
	}
}

func equalOutcome(a, b Outcome) bool {
	return a.Judged == b.Judged && a.Proposal == b.Proposal && slices.Equal(a.Requests, b.Requests) &&
		slices.Equal(a.Waiting, b.Waiting) && a.Issue == b.Issue && a.Voided == b.Voided
}

// TestADirectPushIsOutsideTheFlow: one issue to the owners of the policy it replaced; a later direct push is noted on
// it; the approved digest stays.
func TestADirectPushIsOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	w.gh.files["c1"] = strings.Replace(policyV(1), "owners: [olivia, \"@oscar\", acme/arch]", "owners: [mallory]", 1)

	out := w.push("c0", "c1", "README.md", PolicyPath)
	w.wantOutside(out, policy3.Digest([]byte(w.gh.files["c1"])))
	if got := w.gh.assignees[out.Issue]; !slices.Equal(got, []string{"olivia", "oscar"}) {
		t.Errorf("assignees %v, want the owners of the policy before the push", got)
	}
	body := w.gh.comments[out.Issue][0]
	if !strings.Contains(body, "c1") || !strings.Contains(body, "`main`") || !strings.Contains(body, "confirms or reverts") {
		t.Errorf("issue:\n%s", body)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(0) {
		t.Errorf("approved digest %q, want the one before", r.ApprovedDigest)
	}

	w.gh.files["c2"] = policyV(2)
	again := w.push("c1", "c2")
	w.wantOutside(again, digestV(2))
	if again.Issue != out.Issue || len(w.gh.openIssues()) != 1 {
		t.Errorf("a second direct push opened issue %d, want the first %d only", again.Issue, out.Issue)
	}
	if notes := w.gh.comments[out.Issue]; len(notes) != 2 || !strings.Contains(notes[1], "changed again in c2") {
		t.Errorf("notes %q, want one note naming c2", notes)
	}
}

// TestAnOlderApprovedPolicyPushedAgainIsOutsideTheFlow: approvals are used once, for one step (design 9.2, 19).
func TestAnOlderApprovedPolicyPushedAgainIsOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")

	w.gh.files["c2"] = policyV(0) // the policy before the answer, approved once
	w.wantOutside(w.push("c1", "c2"), digestV(0))
}

// TestAnApprovalIsUsedOnce: after its consume, the same policy brought by another commit, as a merge of the same pull
// request would, is outside the flow, and the approval joins no other proposal.
func TestAnApprovalIsUsedOnce(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	a := w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")

	w.gh.files["c2"] = policyV(0)
	w.push("c1", "c2") // reverted by hand: outside the flow
	w.gh.files["c3"] = policyV(1)
	w.wantOutside(w.push("c2", "c3"), digestV(1))
	if p := w.proposal("p1"); p.Commit != "c1" {
		t.Errorf("proposal consumed by %q, want c1 only", p.Commit)
	}

	err := records.Answer(context.Background(), w.mem, w.tenant(), testRepo.FullName, "a9",
		records.Approval{Kind: records.KindApprove, Request: 431, Change: json.RawMessage(`{}`)},
		&records.Pin{Proposal: "p9", PullRequest: 29, HeadSHA: "h", Digest: "d"})
	if !errors.Is(err, records.ErrAnswered) {
		t.Errorf("a second answer of the consumed request = %v, want ErrAnswered", err)
	}
	if err := records.Join(context.Background(), w.mem, w.tenant(), testRepo.FullName, a, "p9", "h", "d"); err == nil {
		t.Error("the consumed approval joined another proposal")
	}
}

// TestAMergeOfAnotherPolicyVoidsItsProposal: the pull request merged a head from before the last answer joined (or
// over a changed policy): the push is outside the flow, the proposal can never be consumed, its requests open again.
func TestAMergeOfAnotherPolicyVoidsItsProposal(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"] = policyV(0)
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.answered(432, "graph c d", "p1", 21, digestV(2))
	w.merge(21, "c1", 1) // the head before the second answer joined

	out := w.push("c0", "c1")
	w.wantOutside(out, digestV(1))
	if out.Voided != "p1" {
		t.Errorf("voided %q, want p1", out.Voided)
	}
	if p := w.proposal("p1"); p.State != records.StateVoid {
		t.Errorf("proposal %q, want void", p.State)
	}
	for _, n := range []int{431, 432} {
		if !w.gh.issues[n] || !strings.Contains(w.gh.comments[n][len(w.gh.comments[n])-1], "#21 merged a policy other than") {
			t.Errorf("request %d: open %v, notes %q", n, w.gh.issues[n], w.gh.comments[n])
		}
	}
	if idx := w.requestIndex(); len(idx) != 2 {
		t.Errorf("request index %v, want both requests open again", idx)
	}
}

// TestAPushIsJudgedOnlyWhenItChangesThePolicy: a push of other files is not read; one whose paths are unknown is read
// and judged by the policy before and after it.
func TestAPushIsJudgedOnlyWhenItChangesThePolicy(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.fail["File"] = errors.New("not read")
	if out := w.push("c0", "c1", "go.mod", "pkg/a.go"); out.Judged != JudgedNone {
		t.Errorf("a push of other files = %+v", out)
	}
	delete(w.gh.fail, "File")
	w.gh.files["c0"], w.gh.files["c1"] = policyV(0), policyV(0)
	if out := w.push("c0", "c1"); out.Judged != JudgedNone {
		t.Errorf("unknown paths, the same policy = %+v", out)
	}
	if out := w.push("", "c1"); out.Judged != JudgedOutside {
		t.Errorf("no commit before = %+v, want judged (outside the flow)", out)
	}
	delete(w.gh.files, "c2")
	if out := w.push("c1", "c2"); out.Judged != JudgedOutside || w.repo().OutsideFlow.Digest != "" {
		t.Errorf("the policy removed = %+v, want outside the flow", out)
	}
}

// TestARepositoryWithoutAnApprovedPolicyIsNotJudged: change control starts with the first approval (M2 task 23); until
// then no state is set and no issue opened, but a proposal's merge is consumed and starts it.
func TestARepositoryWithoutAnApprovedPolicyIsNotJudged(t *testing.T) {
	w := newWorld(t)
	w.gh.files["c0"], w.gh.files["c1"] = policyV(0), policyV(1)
	if out := w.push("c0", "c1"); out.Judged != JudgedUnguarded || len(w.gh.openIssues()) != 0 || w.repo().OutsideFlow != nil {
		t.Errorf("outcome %+v, issues %v, repository %+v", out, w.gh.openIssues(), w.repo())
	}
	w.answered(431, "graph a b", "p1", 21, digestV(2))
	w.merge(21, "c2", 2)
	if out := w.push("c1", "c2"); out.Judged != JudgedConsumed || w.repo().ApprovedDigest != digestV(2) {
		t.Errorf("outcome %+v, repository %+v; want the first consume to approve", out, w.repo())
	}
}

// TestAConsumeOutsideTheFlowKeepsTheState: the merged policy also carries the change no approval made.
func TestAConsumeOutsideTheFlowKeepsTheState(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"], w.gh.files["c1"] = policyV(0), policyV(1)
	outside := w.push("c0", "c1")
	w.answered(431, "graph a b", "p1", 21, digestV(2))
	w.merge(21, "c2", 2)
	if out := w.push("c1", "c2"); out.Judged != JudgedConsumed {
		t.Fatalf("outcome %+v, want consumed", out)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(0) || r.OutsideFlow == nil || r.OutsideFlow.Issue != outside.Issue {
		t.Errorf("repository %+v, want still outside the flow with the approved digest before", r)
	}
}

// TestAFailedIssueIsOpenedOnTheRetry: the state is recorded first, so every check is unknown at once; the delivery is
// retried and the retry opens the one issue.
func TestAFailedIssueIsOpenedOnTheRetry(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.files["c0"], w.gh.files["c1"] = policyV(0), policyV(1)
	w.gh.fail["CreateIssue"] = api.ErrRateLimited
	if _, err := w.svc.Pushed(context.Background(), Push{Repo: testRepo, Before: "c0", After: "c1"}); !errors.Is(err, api.ErrRateLimited) {
		t.Fatalf("Pushed = %v, want the rate limit", err)
	}
	if r := w.repo(); r.OutsideFlow == nil || r.OutsideFlow.Issue != 0 {
		t.Errorf("repository %+v, want outside the flow without an issue yet", r)
	}
	delete(w.gh.fail, "CreateIssue")
	w.wantOutside(w.push("c0", "c1"), digestV(1))
	if issues := w.gh.openIssues(); len(issues) != 1 {
		t.Errorf("issues %v, want one", issues)
	}
}

func TestTextsUseNoNotUsedWord(t *testing.T) {
	for name, text := range map[string]string{
		"title": outsideTitle, "body": outsideBody("main", "c1"), "changed again": changedAgainNote("c2"),
		"duplicate": duplicateNote(12), "merged other": mergedOtherNote(21),
	} {
		if words := wording.NotUsedIn(text); len(words) > 0 {
			t.Errorf("%s uses %v:\n%s", name, words, text)
		}
	}
}
