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

// hub is a fake GitHub: a linear history of the default branch, the policy at each commit, pull requests, and the
// issues the App opens.
type hub struct {
	mu        sync.Mutex
	history   []string          // the default branch's commits, oldest first; the last is its tip
	files     map[string]string // commit → policy; a missing commit has no policy
	workflows map[string]string // commit → the workflow file; a missing commit has none
	pulls     map[int]api.Pull
	issues    map[int]bool // number → open
	comments  map[int][]string
	assignees map[int][]string
	checks    []api.CheckRun
	onHead    func() // runs under the fake GitHub lock before reading the default tip
	checkErr  error  // CreateCheckRun refuses with it
	next      int
	fail      map[string]error
}

func (h *hub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fail["File"]; err != nil {
		return nil, "", err
	}
	files := map[string]map[string]string{PolicyPath: h.files, WorkflowPath: h.workflows}[path]
	text, ok := files[ref]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return []byte(text), "blob-" + ref, nil
}

func (h *hub) BranchHead(_ context.Context, _ api.RepoRef, branch string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onHead != nil {
		h.onHead()
	}
	if branch != testRepo.DefaultBranch || len(h.history) == 0 {
		return "", api.ErrNotFound
	}
	return h.history[len(h.history)-1], nil
}

// Compare answers from the history: identical, ahead (base before head), behind; a commit off it is not found.
func (h *hub) Compare(_ context.Context, _ api.RepoRef, base, head string) (api.Comparison, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, hd := slices.Index(h.history, base), slices.Index(h.history, head)
	switch {
	case b < 0 || hd < 0:
		return api.Comparison{}, api.ErrNotFound
	case b == hd:
		return api.Comparison{Status: "identical"}, nil
	case b < hd:
		var commits []api.Commit
		for i := b + 1; i <= hd; i++ {
			commits = append(commits, api.Commit{SHA: h.history[i], Parents: []string{h.history[i-1]}})
		}
		return api.Comparison{Status: "ahead", TotalCommits: len(commits), Commits: commits}, nil
	}
	return api.Comparison{Status: "behind"}, nil
}

// commit appends commit, whose policy is policy, to the default branch: it is the tip now.
func (h *hub) commit(commit, policy string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[commit] = policy
	if !slices.Contains(h.history, commit) {
		h.history = append(h.history, commit)
	}
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

func (h *hub) CheckRuns(_ context.Context, _ api.RepoRef, headSHA, name string) ([]api.CheckRun, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []api.CheckRun
	for i, c := range h.checks {
		if c.HeadSHA == headSHA && c.Name == name {
			c.ID = int64(i + 1)
			out = append(out, c)
		}
	}
	return out, nil
}

func (h *hub) CreateCheckRun(_ context.Context, _ api.RepoRef, c api.CheckRun) (api.CheckRun, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.checkErr != nil {
		return api.CheckRun{}, h.checkErr
	}
	h.checks = append(h.checks, c)
	return c, nil
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
	gh := &hub{files: map[string]string{}, workflows: map[string]string{}, pulls: map[int]api.Pull{}, issues: map[int]bool{}, comments: map[int][]string{},
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

// merge merges pull request pr as commit, whose policy is policyV(n): the default branch's tip now.
func (w *world) merge(pr int, commit string, n int) {
	w.gh.commit(commit, policyV(n))
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
	w.gh.commit("c0", policyV(0))
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
	w.gh.commit("c0", policyV(0))
	a := w.answered(431, "graph a b", "p1", 21, digestV(1))
	if err := records.Join(context.Background(), w.mem, w.tenant(), testRepo.FullName, a, "p1", "head-rewritten", digestV(3), 0); err != nil {
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
	w.gh.commit("c0", policyV(0))
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

	w.gh.commit("c3", policyV(9))
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
	w.gh.commit("c0", policyV(0))
	w.gh.commit("c1", strings.Replace(policyV(1), "owners: [olivia, \"@oscar\", acme/arch]", "owners: [mallory]", 1))

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

	w.gh.commit("c2", policyV(2))
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
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")

	w.gh.commit("c2", policyV(0)) // the policy before the answer, approved once
	w.wantOutside(w.push("c1", "c2"), digestV(0))
}

// TestAnApprovalIsUsedOnce: after its consume, the same policy brought by another commit, as a merge of the same pull
// request would, is outside the flow, and the approval joins no other proposal.
func TestAnApprovalIsUsedOnce(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	a := w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")

	w.gh.commit("c2", policyV(0))
	w.push("c1", "c2") // reverted by hand: outside the flow
	w.gh.commit("c3", policyV(1))
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
	if err := records.Join(context.Background(), w.mem, w.tenant(), testRepo.FullName, a, "p9", "h", "d", 0); err == nil {
		t.Error("the consumed approval joined another proposal")
	}
}

// TestAMergeOfAnotherPolicyVoidsItsProposal: the pull request merged a head from before the last answer joined (or
// over a changed policy): the push is outside the flow, the proposal can never be consumed, its requests open again.
func TestAMergeOfAnotherPolicyVoidsItsProposal(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
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
	w.gh.commit("c0", policyV(0))
	w.gh.commit("c1", policyV(0))
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
	w.gh.commit("c0", policyV(0))
	w.gh.commit("c1", policyV(1))
	if out := w.push("c0", "c1"); out.Judged != JudgedUnguarded || len(w.gh.openIssues()) != 0 || w.repo().OutsideFlow != nil {
		t.Errorf("outcome %+v, issues %v, repository %+v", out, w.gh.openIssues(), w.repo())
	}
	// Only a first approval starts change control (M2 review H1): an ordinary proposal's merge approves nothing.
	w.answered(431, "graph a b", "p1", 21, digestV(2))
	w.merge(21, "c2", 2)
	if out := w.push("c1", "c2"); out.Judged != JudgedConsumed || w.repo().ApprovedDigest != "" {
		t.Errorf("outcome %+v, repository %+v; want the proposal consumed and nothing approved", out, w.repo())
	}
}

// TestAConsumeOutsideTheFlowKeepsTheState: an answer made before a direct push merges after it; the merged policy also
// carries the change no approval made.
func TestAConsumeOutsideTheFlowKeepsTheState(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(2))
	w.gh.pulls[21] = api.Pull{Number: 21, State: "open"}
	w.gh.commit("c1", policyV(1))
	outside := w.push("c0", "c1")
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
	w.gh.commit("c0", policyV(0))
	w.gh.commit("c1", policyV(1))
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
		"duplicate": duplicateNote(12), "merged other": mergedOtherNote(21), "reverted": revertedNote(40, "m3"),
		"first title": firstTitle, "first body": firstBody("main", Change{Commit: "c0", Policy: "sha256:p"}, "olivia", "pk"),
		"first pull":    approvedPullNote(Change{Kind: records.KindFirst, HeadSHA: "h1", Policy: "sha256:p"}, "olivia", "pk"),
		"workflow pull": approvedPullNote(Change{Kind: records.KindWorkflow, HeadSHA: "h1"}, "olivia", "pk"),
		"confirmed":     confirmedNote(Change{Commit: "c1", Policy: "sha256:x"}, "olivia", "pk"),
	} {
		if words := wording.NotUsedIn(text); len(words) > 0 {
			t.Errorf("%s uses %v:\n%s", name, words, text)
		}
	}
}

// TestAConsumeHandledLateKeepsTheNewerApproval: two merges handled out of order; the older one uses its approvals but
// never puts its digest back.
func TestAConsumeHandledLateKeepsTheNewerApproval(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.answered(432, "graph c d", "p2", 22, digestV(2))
	w.merge(22, "c2", 2)

	if out := w.push("c1", "c2"); out.Judged != JudgedConsumed || out.Proposal != "p2" {
		t.Fatalf("the later push = %+v", out)
	}
	if out := w.push("c0", "c1"); out.Judged != JudgedConsumed || out.Proposal != "p1" {
		t.Fatalf("the earlier push = %+v", out)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(2) {
		t.Errorf("approved digest is the older policy's, want the tip's")
	}
	if idx := w.requestIndex(); len(idx) != 0 {
		t.Errorf("request index %v, want both consumed requests out", idx)
	}
}

// TestAFirstConsumeAfterAnUnjudgedDirectPushApprovesNothing: the direct push is handled first, while the repository
// has no approved policy; the first consume then finds a tip no approval made and approves nothing, and the direct
// push delivered again is still not outside the flow.
func TestAFirstConsumeAfterAnUnjudgedDirectPushApprovesNothing(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.gh.commit("c2", policyV(9))

	if out := w.push("c1", "c2"); out.Judged != JudgedUnguarded {
		t.Fatalf("the direct push = %+v, want unguarded", out)
	}
	if out := w.push("c0", "c1"); out.Judged != JudgedConsumed {
		t.Fatalf("the merge = %+v, want consumed", out)
	}
	if r := w.repo(); r.ApprovedDigest != "" || r.OutsideFlow != nil {
		t.Errorf("repository %+v, want no approved policy: the tip holds a change no approval made", r)
	}
	if out := w.push("c1", "c2"); out.Judged != JudgedUnguarded || len(w.gh.openIssues()) != 0 {
		t.Errorf("the direct push again = %+v, issues %v; want unguarded, no issue", out, w.gh.openIssues())
	}
}

// TestAPushBackToAConsumedCommitIsOutsideTheFlow: a force-push of the branch back to an earlier proposal's merge
// commit brings an older approved policy; that push was never judged and is outside the flow.
func TestAPushBackToAConsumedCommitIsOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")
	w.answered(432, "graph c d", "p2", 22, digestV(2))
	w.merge(22, "c2", 2)
	w.push("c1", "c2")

	w.wantOutside(w.push("c2", "c1"), digestV(1))
}

// TestAMergeQueueBatchIsConsumed: the queue lands the proposal's merge commit and a code change in one push, so the
// pushed commit is not the merge commit.
func TestAMergeQueueBatchIsConsumed(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.gh.commit("c2", policyV(1)) // a code pull request of the same batch

	out := w.push("c0", "c2")
	if out.Judged != JudgedConsumed || out.Proposal != "p1" {
		t.Fatalf("outcome %+v, want p1 consumed", out)
	}
	if p := w.proposal("p1"); p.Commit != "c1" {
		t.Errorf("proposal consumed by %q, want its merge commit c1", p.Commit)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(1) {
		t.Errorf("approved digest is not the batch's policy")
	}

	w.answered(432, "graph c d", "p2", 22, digestV(2))
	w.merge(22, "c3", 2)
	w.gh.commit("c4", policyV(9)) // a direct change after it, in the same push
	if out := w.push("c2", "c4"); out.Judged != JudgedOutside || out.Voided != "p2" {
		t.Errorf("a batch whose last policy no approval made = %+v, want outside the flow and p2 void", out)
	}
}

// joinOnVoid is a Store double whose first void of proposal p1 is preceded by an answer joining it: the approvals the
// void read are then not all of the proposal's.
type joinOnVoid struct {
	store.Store
	w      *world
	joined bool
}

func (j *joinOnVoid) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	if !j.joined && len(swaps) == 2 && swaps[0].Key == records.ProposalKey(testRepo.FullName, "p1") {
		j.joined = true
		j.w.answered(433, "graph e f", "p1", 21, digestV(3))
	}
	return j.Store.CompareAndSwapAll(ctx, t, swaps)
}

// TestAnAnswerThatJoinsDuringTheVoidIsVoidedToo: no approval stays joined to a void proposal with its request covering
// its key for ever.
func TestAnAnswerThatJoinsDuringTheVoidIsVoidedToo(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 9) // merged another policy
	w.svc = New(w.gh, &joinOnVoid{Store: w.mem, w: w})

	out := w.push("c0", "c1")
	if out.Voided != "p1" || w.proposal("p1").State != records.StateVoid {
		t.Fatalf("outcome %+v, proposal %+v; want p1 void", out, w.proposal("p1"))
	}
	if idx := w.requestIndex(); len(idx) != 2 {
		t.Errorf("request index %v, want both requests open again", idx)
	}
	if !w.gh.issues[433] {
		t.Error("the request that joined during the void was not reopened")
	}
}

// TestALatePushFromBeforeTheFirstApprovalIsNotOutsideTheFlow: a direct push made before any approval, delivered after
// the first consume, was made outside change control; a push back to the first approval's commit is judged.
func TestALatePushFromBeforeTheFirstApprovalIsNotOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.gh.commit("c1", policyV(1)) // a direct push, its event delayed
	w.firstApproved("p1", 21, digestV(2))
	w.merge(21, "c2", 2)

	if out := w.push("c1", "c2"); out.Judged != JudgedConsumed || w.repo().ApprovedDigest != digestV(2) {
		t.Fatalf("the first consume = %+v, repository %+v", out, w.repo())
	}
	if out := w.push("c0", "c1"); out.Judged != JudgedUnguarded || w.repo().OutsideFlow != nil || len(w.gh.openIssues()) != 0 {
		t.Errorf("the late push = %+v, repository %+v; want unguarded, no state, no issue", out, w.repo())
	}

	w.gh.commit("c3", policyV(3))
	w.push("c2", "c3") // outside the flow
	w.wantOutside(w.push("c3", "c1"), digestV(1))
}

// consumeRace is a Store double that lands another consume just before the first consume swap of p1, after p1's
// handler judged the tip.
type consumeRace struct {
	store.Store
	land func()
	done bool
}

func (c *consumeRace) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	if !c.done && len(swaps) == 5 && swaps[0].Key == records.ProposalKey(testRepo.FullName, "p1") {
		c.done = true
		c.land()
	}
	return c.Store.CompareAndSwapAll(ctx, t, swaps)
}

// TestAConsumeThatLosesARaceKeepsTheNewerApproval: p1's handler finds its digest at the tip, then p2 merges and is
// consumed before p1's swap; p1 judges again and approves nothing, so the newer approval stays.
func TestAConsumeThatLosesARaceKeepsTheNewerApproval(t *testing.T) {
	w := newWorld(t)
	w.approved(digestV(0))
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.answered(432, "graph c d", "p2", 22, digestV(2))
	other := New(w.gh, w.mem)
	w.svc = New(w.gh, &consumeRace{Store: w.mem, land: func() {
		w.merge(22, "c2", 2)
		if out, err := other.Pushed(context.Background(), Push{Repo: testRepo, Before: "c1", After: "c2"}); err != nil ||
			out.Judged != JudgedConsumed {
			t.Errorf("p2's push = %+v, %v", out, err)
		}
	}})

	if out := w.push("c0", "c1"); out.Judged != JudgedConsumed || out.Proposal != "p1" {
		t.Fatalf("p1's push = %+v", out)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(2) || r.ApprovedCommit != "c2" {
		t.Errorf("repository %+v, want p2's policy approved at c2", r)
	}
}

// TestARewrittenHistoryDoesNotStopLaterApprovals: the branch's history is rewritten with the same policy, so the
// approved commit is off it; the next proposal is still approved by the tip.
func TestARewrittenHistoryDoesNotStopLaterApprovals(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.approved(digestV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")

	w.gh.history = nil // a rewrite: new commits, the same policy
	w.gh.commit("r0", policyV(0))
	w.gh.commit("r1", policyV(1))
	if out := w.push("c1", "r1"); out.Judged != JudgedNone {
		t.Fatalf("the rewrite = %+v, want none: the policy did not change", out)
	}
	w.answered(432, "graph c d", "p2", 22, digestV(2))
	w.merge(22, "r2", 2)
	if out := w.push("r1", "r2"); out.Judged != JudgedConsumed {
		t.Fatalf("the next merge = %+v", out)
	}
	if r := w.repo(); r.ApprovedDigest != digestV(2) || r.ApprovedCommit != "r2" {
		t.Errorf("repository %+v, want the next policy approved at r2", r)
	}
}

// TestALateBranchCreationFromBeforeTheFirstApprovalIsNotOutsideTheFlow: the push that created the branch, handled
// after the first consume, has no commit before; its commit is behind the first approval's.
func TestALateBranchCreationFromBeforeTheFirstApprovalIsNotOutsideTheFlow(t *testing.T) {
	w := newWorld(t)
	w.gh.commit("c0", policyV(0))
	w.answered(431, "graph a b", "p1", 21, digestV(1))
	w.merge(21, "c1", 1)
	w.push("c0", "c1")
	if out := w.push("", "c0"); out.Judged != JudgedUnguarded || w.repo().OutsideFlow != nil {
		t.Errorf("the late creation push = %+v, repository %+v; want unguarded", out, w.repo())
	}
}
