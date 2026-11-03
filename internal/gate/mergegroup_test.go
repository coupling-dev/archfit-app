package gate

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

// group is one merge group of the fake queue: its commit and the merge_group run that measures it.
type group struct {
	repo  api.Repo
	sha   string
	runID int64
	last  *pull // the entry whose commit sha is
}

func (f *fakeGitHub) MergeQueue(_ context.Context, r api.RepoRef, branch string) ([]api.QueueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("MergeQueue", r); err != nil {
		return nil, err
	}
	return slices.Clone(f.queue[branchKey(r.FullName, branch)]), nil
}

// enqueue puts the pulls in the default branch's merge queue in this order and returns their groups. The default
// branch head becomes a commit that holds every default-branch file; each group commit holds them too, so a test
// changes a group's files on purpose.
func (w *world) enqueue(pulls ...*pull) []*group {
	w.t.Helper()
	r := pulls[0].repo
	tip := w.sha()
	w.gh.heads[branchKey(r.FullName, testBranch)] = tip
	prefix := r.FullName + "|" + testBranch + "|"
	var files []string
	for k := range w.gh.files {
		if strings.HasPrefix(k, prefix) {
			files = append(files, strings.TrimPrefix(k, prefix))
		}
	}
	var entries []api.QueueEntry
	var out []*group
	for i, p := range pulls {
		g := &group{repo: r, sha: w.sha(), last: p}
		for _, path := range files {
			raw := w.gh.files[fileKey(r.FullName, testBranch, path)]
			w.setFile(r, tip, path, raw)
			w.setFile(r, g.sha, path, raw)
			if _, ok := w.gh.files[fileKey(r.FullName, p.head, path)]; !ok {
				w.setFile(r, p.head, path, raw)
			}
		}
		w.nextRun++
		g.runID = w.nextRun
		w.gh.runs[g.runID] = api.WorkflowRun{ID: g.runID, Attempt: 1, Event: "merge_group", HeadSHA: g.sha, HeadRepo: r.FullName,
			WorkflowPath: testWorkflow, Status: runCompleted}
		entries = append(entries, api.QueueEntry{Position: i + 1, HeadSHA: g.sha, BaseSHA: tip, PullRequest: p.number, PullHeadSHA: p.head})
		out = append(out, g)
	}
	if w.gh.queue == nil {
		w.gh.queue = map[string][]api.QueueEntry{}
	}
	w.gh.queue[branchKey(r.FullName, testBranch)] = entries
	return out
}

// dequeueAll empties the queue, as GitHub does when it destroys the groups.
func (w *world) dequeueAll(r api.Repo) {
	w.gh.queue[branchKey(r.FullName, testBranch)] = nil
}

func (w *world) groupSpec(g *group, body []byte) uploadSpec {
	id := manifest.Current().Identity
	ref := fmt.Sprintf("refs/heads/gh-readonly-queue/%s/pr-%d-%s", testBranch, g.last.number, g.sha)
	return uploadSpec{
		body: body,
		env: envelope.Envelope{
			SchemaVersion: envelope.SchemaV1, Kind: envelope.KindReport, Repository: g.repo.FullName,
			Event: "merge_group", HeadSHA: g.sha, AnalyzedSHA: g.sha, RunID: g.runID, RunAttempt: 1,
			EngineVersion: id.EngineVersion, ImageDigest: id.ImageDigest, Platform: id.Platform,
			PayloadDigest: envelope.PayloadDigest(body),
		},
		token: fmt.Sprintf("token-%d-1", g.runID),
		claims: oidc.Claims{
			Repository: g.repo.FullName, RepositoryID: g.repo.ID, RunID: g.runID, RunAttempt: 1,
			SHA: g.sha, Ref: ref, EventName: "merge_group",
			JobWorkflowRef: g.repo.FullName + "/" + testWorkflow + "@" + ref, RunnerEnvironment: "github-hosted",
		},
		url: "/v1/reports",
	}
}

func (w *world) uploadGroup(g *group, fixture string, muts ...func(map[string]any)) answer {
	w.t.Helper()
	return w.send(w.groupSpec(g, w.report(g.repo, fixture, muts...)))
}

func (w *world) groupCheck(g *group) (api.CheckRun, bool) {
	w.t.Helper()
	runs := w.gh.checkRuns(g.repo.FullName, g.sha, gateCheckName)
	if len(runs) > 1 {
		w.t.Fatalf("%d gate checks on group %s", len(runs), g.sha)
	}
	if len(runs) == 0 {
		return api.CheckRun{}, false
	}
	return runs[0], true
}

func (w *world) wantGroupGate(g *group, conclusion string) api.CheckRun {
	w.t.Helper()
	c, ok := w.groupCheck(g)
	if !ok || c.Conclusion != conclusion {
		w.t.Fatalf("group gate check = %+v (found %v), want conclusion %q", c, ok, conclusion)
	}
	return c
}

func (w *world) groupEvent(kind webhook.EventKind, g *group, reason string) webhook.Event {
	return webhook.Event{Kind: kind, DeliveryID: "delivery-group", InstallationID: g.repo.InstallationID, Repo: g.repo.FullName,
		HeadSHA: g.sha, HeadRepo: g.repo.FullName, BaseRef: testBranch, DefaultBranch: testBranch, GroupReason: reason}
}

// changePolicyIn makes p change the policy to raw, at its head and in every group commit at or behind it.
func (w *world) changePolicyIn(p *pull, raw string, groups ...*group) {
	w.changes(p, "pkg/a/a.go", policy.PolicyPath)
	w.setFile(p.repo, p.head, policy.PolicyPath, []byte(raw))
	for _, g := range groups {
		w.setFile(p.repo, g.sha, policy.PolicyPath, []byte(raw))
	}
}

const loosenedPolicy = basePolicy + "  b:\n    paths: [\"pkg/b/**\"]\n"

func TestMergeGroupThatViolatesPolicyFailsOnTheGroupCommit(t *testing.T) {
	w := newWorld(t)
	one, two := w.openPull(w.shop, 1), w.openPull(w.shop, 2)
	w.wantAnswer(w.upload(one, fixtureHealthy), http.StatusOK, "success", "healthy")
	w.wantAnswer(w.upload(two, fixtureHealthy), http.StatusOK, "success", "healthy")
	groups := w.enqueue(one, two)

	// Together the two pull requests break a rule that neither breaks alone: the group commit is what is measured.
	w.wantAnswer(w.uploadGroup(groups[1], fixtureBlocked), http.StatusOK, "failure", "blocking_findings")
	w.wantGroupGate(groups[1], "failure")
	// The pull request heads keep their own results, and no comment is written for a group.
	w.wantGate(one, runCompleted, "success")
	w.wantGate(two, runCompleted, "success")
	if n := len(w.comments(two)); n != 1 {
		t.Errorf("pull request #2 has %d comments, want only its own", n)
	}
}

func TestValidMergeGroupSucceedsOnTheGroupCommitOnly(t *testing.T) {
	w := newWorld(t)
	one, two := w.openPull(w.shop, 1), w.openPull(w.shop, 2)
	groups := w.enqueue(one, two)
	w.wantAnswer(w.uploadGroup(groups[1], fixtureHealthy), http.StatusOK, "success", "healthy")
	c := w.wantGroupGate(groups[1], "success")
	if c.DetailsURL != "" {
		t.Errorf("group check links to %q; a group has no pull request page", c.DetailsURL)
	}
	for _, p := range []*pull{one, two} {
		if _, ok := w.check(p, p.head, gateCheckName); ok {
			t.Errorf("the group upload wrote a gate check on pull request #%d's head", p.number)
		}
	}
	if _, ok := w.groupCheck(groups[0]); ok {
		t.Error("the upload of one group wrote on another group's commit")
	}
}

func TestMergeGroupUploadNotQueuedIsRefused(t *testing.T) {
	w := newWorld(t)
	one := w.openPull(w.shop, 1)
	groups := w.enqueue(one)
	w.dequeueAll(w.shop)
	a := w.uploadGroup(groups[0], fixtureHealthy)
	if a.status != http.StatusConflict || a.Error != "stale_group" {
		t.Fatalf("answer = %d %s, want 409 stale_group", a.status, a.raw)
	}
	if _, ok := w.groupCheck(groups[0]); ok {
		t.Error("a refused upload wrote a check")
	}
}

func TestMergeGroupWithUnapprovedProtectedChangeInAnyMember(t *testing.T) {
	w := newWorld(t)
	one, two := w.openPull(w.shop, 1), w.openPull(w.shop, 2)
	groups := w.enqueue(one, two)
	w.changePolicyIn(one, loosenedPolicy, groups...)
	digest := policy.Digest([]byte(loosenedPolicy))
	// #1 is ahead of #2: the group of #2 carries #1's unapproved policy change, also if GitHub let #1's own group fail.
	w.wantAnswer(w.uploadGroup(groups[1], fixtureHealthy, configHash(digest)), http.StatusOK, "action_required", "owner_approval_missing")
	w.noSuccess()
}

func TestMergeGroupWithOwnerApprovedPolicyChange(t *testing.T) {
	w := newWorld(t)
	one, two := w.openPull(w.shop, 1), w.openPull(w.shop, 2)
	groups := w.enqueue(one, two)
	w.changePolicyIn(one, loosenedPolicy, groups...)
	w.review(one, ownerLogin, "APPROVED", one.head)
	digest := policy.Digest([]byte(loosenedPolicy))
	w.wantAnswer(w.uploadGroup(groups[1], fixtureHealthy, configHash(digest)), http.StatusOK, "success", "healthy")
	// The default branch's own policy is not the group's governing blob any more.
	w2 := newWorld(t)
	one2, two2 := w2.openPull(w2.shop, 1), w2.openPull(w2.shop, 2)
	groups2 := w2.enqueue(one2, two2)
	w2.changePolicyIn(one2, loosenedPolicy, groups2...)
	w2.review(one2, ownerLogin, "APPROVED", one2.head)
	w2.wantAnswer(w2.uploadGroup(groups2[1], fixtureHealthy), http.StatusOK, "action_required", "policy_mismatch")
}

func TestMergeGroupConflictingPolicyChanges(t *testing.T) {
	w := newWorld(t)
	one, two := w.openPull(w.shop, 1), w.openPull(w.shop, 2)
	groups := w.enqueue(one, two)
	w.changePolicyIn(one, loosenedPolicy, groups...)
	w.changePolicyIn(two, loosenedPolicy+"  c:\n    paths: [\"pkg/c/**\"]\n", groups[1])
	w.review(one, ownerLogin, "APPROVED", one.head)
	w.review(two, ownerLogin, "APPROVED", two.head)
	digest := w.digestOf(w.shop, groups[1].sha, policy.PolicyPath)
	w.wantAnswer(w.uploadGroup(groups[1], fixtureHealthy, configHash(digest)), http.StatusOK, "action_required", "merge_group_conflict")
	// Each approved change alone still passes in its own group.
	w.wantAnswer(w.uploadGroup(groups[0], fixtureHealthy, configHash(policy.Digest([]byte(loosenedPolicy)))), http.StatusOK, "success", "healthy")
}

func TestMergeGroupWithForkMember(t *testing.T) {
	w := newWorld(t)
	one := w.openPull(w.shop, 1)
	w.editPull(one, func(p *api.Pull) { p.HeadRepo = "mallory/shop" })
	groups := w.enqueue(one)
	w.wantAnswer(w.uploadGroup(groups[0], fixtureHealthy), http.StatusOK, "action_required", "fork_unsupported")
}

func TestMergeGroupDestroyedInvalidatesItsResult(t *testing.T) {
	w := newWorld(t)
	one := w.openPull(w.shop, 1)
	groups := w.enqueue(one)
	w.handle(w.groupEvent(webhook.MergeGroupRequested, groups[0], ""))
	if c, ok := w.groupCheck(groups[0]); !ok || c.Status != runInProgress {
		t.Fatalf("checks_requested made %+v (found %v), want a pending gate check", c, ok)
	}
	w.wantAnswer(w.uploadGroup(groups[0], fixtureHealthy), http.StatusOK, "success", "healthy")

	w.dequeueAll(w.shop)
	w.handle(w.groupEvent(webhook.MergeGroupDestroyed, groups[0], "dequeued"))
	c := w.wantGroupGate(groups[0], "action_required")
	if !strings.Contains(c.Summary, "merge_group_unverified") {
		t.Errorf("destroyed group summary does not name merge_group_unverified:\n%s", c.Summary)
	}
	// A late upload or a retry of the destroyed group writes nothing.
	if a := w.uploadGroup(groups[0], fixtureHealthy); a.status != http.StatusConflict || a.Error != "stale_group" {
		t.Fatalf("late upload = %d %s, want 409 stale_group", a.status, a.raw)
	}
	w.wantGroupGate(groups[0], "action_required")
}

func TestMergeGroupMergedKeepsItsResult(t *testing.T) {
	w := newWorld(t)
	one := w.openPull(w.shop, 1)
	groups := w.enqueue(one)
	w.wantAnswer(w.uploadGroup(groups[0], fixtureHealthy), http.StatusOK, "success", "healthy")
	w.dequeueAll(w.shop)
	w.handle(w.groupEvent(webhook.MergeGroupDestroyed, groups[0], "merged"))
	w.wantGroupGate(groups[0], "success")
}

func TestOwnerWithdrawalOnAQueuedPullDowngradesItsGroups(t *testing.T) {
	w := newWorld(t)
	one, two := w.openPull(w.shop, 1), w.openPull(w.shop, 2)
	groups := w.enqueue(one, two)
	w.changePolicyIn(one, loosenedPolicy, groups...)
	w.review(one, ownerLogin, "APPROVED", one.head)
	digest := policy.Digest([]byte(loosenedPolicy))
	for _, g := range groups {
		w.wantAnswer(w.uploadGroup(g, fixtureHealthy, configHash(digest)), http.StatusOK, "success", "healthy")
	}
	w.review(one, ownerLogin, "CHANGES_REQUESTED", one.head)
	w.handle(w.event(webhook.ReviewChanged, one))
	for _, g := range groups {
		w.wantGroupGate(g, "action_required")
	}
}

func TestMergeGroupWithoutSettingsGetsNoPendingCheck(t *testing.T) {
	w := newWorld(t)
	one := w.openPull(w.shop, 1)
	groups := w.enqueue(one)
	w.removeFile(w.shop, testBranch, ".archfit-app.yaml")
	w.handle(w.groupEvent(webhook.MergeGroupRequested, groups[0], ""))
	if _, ok := w.groupCheck(groups[0]); ok {
		t.Error("a repository without settings got a pending gate check")
	}
}

func TestMergeGroupQueueUnreadableIsNeverASuccess(t *testing.T) {
	w := newWorld(t)
	one := w.openPull(w.shop, 1)
	groups := w.enqueue(one)
	w.gh.fail["MergeQueue"] = api.ErrQueueForbidden
	a := w.uploadGroup(groups[0], fixtureHealthy)
	if a.status != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d %s, want 503", a.status, a.raw)
	}
	w.noSuccess()
	// A review event is still handled without the permission: no group result can exist to withdraw.
	w.handle(w.event(webhook.ReviewChanged, one))
}
