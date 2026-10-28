package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	repairMarker = "<!-- archfit:repair main -->\n"
	repairTitle  = "Architecture repair needed on main"
)

var issueMethods = []string{"AppIssues", "CreateIssue", "UpdateIssueBody", "CloseIssue"}

// pushMain moves the default branch of r to a new commit, as a merge does, with a new push run of the workflow. The
// returned handle has no pull request.
func (w *world) pushMain(r api.Repo) *pull {
	p := &pull{repo: r, head: w.sha()}
	w.nextRun++
	p.runID = w.nextRun
	w.gh.heads[branchKey(r.FullName, r.DefaultBranch)] = p.head
	w.gh.runs[p.runID] = api.WorkflowRun{
		ID: p.runID, Attempt: 1, Event: eventPush, HeadSHA: p.head, HeadRepo: r.FullName,
		WorkflowPath: testWorkflow, Status: runCompleted,
	}
	return p
}

// anotherRun starts a second push run on the same commit, as a re-triggered workflow does.
func (w *world) anotherRun(p *pull) *pull {
	again := *p
	w.nextRun++
	again.runID = w.nextRun
	run := w.gh.runs[p.runID]
	run.ID, run.Attempt = again.runID, 1
	w.gh.runs[again.runID] = run
	return &again
}

// pushSpec is a consistent upload of a push run on branch.
func (w *world) pushSpec(p *pull, branch string, body []byte) uploadSpec {
	run := w.gh.runs[p.runID]
	id := manifest.Current().Identity
	ref := "refs/heads/" + branch
	return uploadSpec{
		body: body,
		env: envelope.Envelope{
			SchemaVersion: envelope.SchemaV1, Kind: envelope.KindReport, Repository: p.repo.FullName,
			Event: eventPush, HeadSHA: p.head, AnalyzedSHA: p.head, RunID: p.runID, RunAttempt: run.Attempt,
			EngineVersion: id.EngineVersion, ImageDigest: id.ImageDigest, Platform: id.Platform,
			PayloadDigest: envelope.PayloadDigest(body),
		},
		token: fmt.Sprintf("token-%d-%d", p.runID, run.Attempt),
		claims: oidc.Claims{
			Repository: p.repo.FullName, RepositoryID: p.repo.ID, RunID: p.runID, RunAttempt: run.Attempt,
			SHA: p.head, Ref: ref, EventName: eventPush,
			JobWorkflowRef: p.repo.FullName + "/" + testWorkflow + "@" + ref, RunnerEnvironment: "github-hosted",
		},
		url: "/v1/reports",
	}
}

func (w *world) uploadPush(p *pull, fixture string) answer {
	w.t.Helper()
	return w.send(w.pushSpec(p, p.repo.DefaultBranch, w.report(p.repo, fixture)))
}

// openIssues returns the App's open issues of r.
func (w *world) openIssues(r api.Repo) []fakeIssue {
	w.gh.mu.Lock()
	defer w.gh.mu.Unlock()
	var out []fakeIssue
	for _, i := range w.gh.issues[r.FullName] {
		if i.Open {
			out = append(out, i)
		}
	}
	return out
}

// theIssue returns the one open repair issue of r; any other number is a failure.
func (w *world) theIssue(r api.Repo) fakeIssue {
	w.t.Helper()
	open := w.openIssues(r)
	if len(open) != 1 {
		w.t.Fatalf("%d open issues, want exactly one", len(open))
	}
	return open[0]
}

// issueCalls counts the calls of the Issues role so far.
func (w *world) issueCalls() int {
	w.gh.mu.Lock()
	defer w.gh.mu.Unlock()
	n := 0
	for _, c := range w.gh.calls {
		if slices.Contains(issueMethods, c.method) {
			n++
		}
	}
	return n
}

func headLine(p *pull) string {
	return fmt.Sprintf("<!-- archfit:head %s run %d.1 -->", p.head, p.runID)
}

// An admin-bypass merge that breaks the default branch opens one issue: what broke, where, and what an agent repairs.
func TestBrokenDefaultBranchOpensOneRepairIssue(t *testing.T) {
	tests := []struct {
		name, fixture, conclusion, reason, state string
		wants                                    []string
	}{
		{"blocking findings", fixtureBlocked, "failure", "blocking_findings", "neutral", []string{
			"Cause: Blocking findings.", "### Blocking findings",
			"<!-- archfit:agent-tasks -->", `"rule_id":"no_direct_b_dependency"`, "<!-- /archfit:agent-tasks -->",
			"### Validation commands", "archfit check -c .archfit.yaml\n",
		}},
		{"missing evidence", fixtureCoverage, "action_required", "missing_required_coverage", "action_required", []string{
			"Cause: Missing required coverage.",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.pushMain(w.shop)
			w.wantAnswer(w.uploadPush(p, tt.fixture), 200, tt.conclusion, tt.reason)

			if c, ok := w.check(p, p.head, stateCheckName); !ok || c.Conclusion != tt.state {
				t.Errorf("%s on the pushed commit = %q (found %v), want %q", stateCheckName, c.Conclusion, ok, tt.state)
			}
			issue := w.theIssue(w.shop)
			if issue.Title != repairTitle || issue.Assignee != "" {
				t.Errorf("issue %q assigned to %q, want %q unassigned", issue.Title, issue.Assignee, repairTitle)
			}
			if !strings.HasPrefix(issue.Body, repairMarker+headLine(p)+"\n") {
				t.Errorf("issue body does not start with the repair and head markers:\n%.300s", issue.Body)
			}
			runLink := fmt.Sprintf("https://github.com/%s/actions/runs/%d/attempts/1", shopName, p.runID)
			for _, want := range append([]string{"HEAD `" + p.head + "`", runLink}, tt.wants...) {
				if !strings.Contains(issue.Body, want) {
					t.Errorf("issue body lacks %q", want)
				}
			}
		})
	}
}

// One issue per branch: a further broken push rewrites it, whatever broke, and an upload that changes nothing writes
// nothing.
func TestFurtherBrokenPushUpdatesTheSameIssue(t *testing.T) {
	tests := []struct{ name, first, second string }{
		{"the same findings", fixtureBlocked, fixtureBlocked},
		{"other findings", fixtureBlocked, fixtureCoverage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			first := w.pushMain(w.shop)
			w.uploadPush(first, tt.first)
			opened := w.theIssue(w.shop)

			second := w.pushMain(w.shop)
			spec := w.pushSpec(second, testBranch, w.report(w.shop, tt.second))
			w.send(spec)
			updated := w.theIssue(w.shop)
			if updated.Number != opened.Number || len(w.gh.issues[shopName]) != 1 {
				t.Fatalf("issue %d of %d, want issue %d updated in place", updated.Number, len(w.gh.issues[shopName]), opened.Number)
			}
			if !strings.Contains(updated.Body, headLine(second)) || strings.Contains(updated.Body, first.head) {
				t.Errorf("the issue does not name the second push only:\n%.300s", updated.Body)
			}

			edits := w.gh.issueEdits
			w.send(spec)
			if w.gh.issueEdits != edits {
				t.Errorf("the repeated upload edited the issue %d times, want none", w.gh.issueEdits-edits)
			}
		})
	}
}

// A push that passes closes the issue with a comment that names the commit that fixed the branch.
func TestPassingPushClosesTheRepairIssue(t *testing.T) {
	w := newWorld(t)
	w.uploadPush(w.pushMain(w.shop), fixtureBlocked)
	issue := w.theIssue(w.shop)

	fix := w.pushMain(w.shop)
	w.wantAnswer(w.uploadPush(fix, fixtureHealthy), 200, "success", "healthy")
	if open := w.openIssues(w.shop); len(open) != 0 {
		t.Fatalf("%d issues still open after the passing push", len(open))
	}
	comments := w.gh.comments[pullKey(shopName, issue.Number)]
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "HEAD `"+fix.head+"`") {
		t.Errorf("closing comments = %+v, want one naming %s", comments, fix.head)
	}
}

func TestAdvisoryRepositoryOpensNoRepairIssue(t *testing.T) {
	w := newWorld(t)
	w.setSettings(w.shop, settings.GateAdvisory)
	w.uploadPush(w.pushMain(w.shop), fixtureBlocked)
	if len(w.gh.issues[shopName]) != 0 {
		t.Fatalf("%d issues in an advisory repository, want none", len(w.gh.issues[shopName]))
	}

	// An issue opened while the repository was required still closes after the switch.
	w.setSettings(w.shop, settings.GateRequired)
	w.uploadPush(w.pushMain(w.shop), fixtureBlocked)
	w.theIssue(w.shop)
	w.setSettings(w.shop, settings.GateAdvisory)
	w.uploadPush(w.pushMain(w.shop), fixtureBlocked)
	w.theIssue(w.shop) // still the one issue, not updated or duplicated
	w.uploadPush(w.pushMain(w.shop), fixtureHealthy)
	if open := w.openIssues(w.shop); len(open) != 0 {
		t.Fatalf("%d issues open after a passing push, want none", len(open))
	}
}

func TestPassingPushWithoutAnIssueWritesNothing(t *testing.T) {
	w := newWorld(t)
	w.wantAnswer(w.uploadPush(w.pushMain(w.shop), fixtureHealthy), 200, "success", "healthy")
	if len(w.gh.issues[shopName]) != 0 || w.gh.commentOps != 0 {
		t.Errorf("%d issues and %d comment writes, want none", len(w.gh.issues[shopName]), w.gh.commentOps)
	}
	for _, c := range w.gh.calls {
		if c.method == "CreateIssue" || c.method == "UpdateIssueBody" || c.method == "CloseIssue" {
			t.Errorf("%s called", c.method)
		}
	}
}

// The App may not hold the Issues permission yet, and GitHub may be rate limited: the issue is a notification, so the
// check, the answer and the head's next publication are what they would be without it.
func TestIssueFailureNeverChangesTheUpload(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"no Issues permission", errors.New("fake: 403 Resource not accessible by integration")},
		{"rate limited", fmt.Errorf("fake: %w", api.ErrRateLimited)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			w := newWorld(t)
			for _, m := range issueMethods {
				w.gh.fail[m] = tt.err
			}
			p := w.pushMain(w.shop)
			w.wantAnswer(w.uploadPush(p, fixtureBlocked), 200, "failure", "blocking_findings")

			if c, ok := w.check(p, p.head, stateCheckName); !ok || c.Conclusion != "neutral" {
				t.Errorf("%s on the pushed commit = %q (found %v), want the published neutral", stateCheckName, c.Conclusion, ok)
			}
			if !strings.Contains(logs.String(), `"msg":"feedback repair issue"`) || !strings.Contains(logs.String(), `"failed":true`) {
				t.Errorf("no failed repair issue in the logs:\n%s", logs)
			}
			if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), leaseKeyPrefix+p.head); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("the head's lease = %v, want it released", err)
			}
		})
	}
}

// Like the PR comment, the issue follows the newest feedback: a late run of an older head, or an older run of the
// same head, never rewrites, closes or reopens it.
func TestOlderRunNeverRewritesTheIssue(t *testing.T) {
	t.Run("an older run after a newer break of the same commit", func(t *testing.T) {
		w := newWorld(t)
		older := w.pushMain(w.shop)
		newer := w.anotherRun(older)
		w.uploadPush(newer, fixtureBlocked)
		before := w.theIssue(w.shop)

		for _, fixture := range []string{fixtureHealthy, fixtureCoverage} {
			w.uploadPush(older, fixture)
			if got := w.theIssue(w.shop); got.Body != before.Body {
				t.Errorf("the older run's %s upload rewrote the issue of the newer run", fixture)
			}
		}
	})

	t.Run("an older break after a newer pass of the same commit", func(t *testing.T) {
		w := newWorld(t)
		older := w.pushMain(w.shop)
		newer := w.anotherRun(older)
		w.uploadPush(newer, fixtureHealthy)

		w.uploadPush(older, fixtureBlocked)
		if n := len(w.gh.issues[shopName]); n != 0 {
			t.Errorf("the older run opened %d issues over the newer pass", n)
		}
	})

	t.Run("a late break of a commit the branch left", func(t *testing.T) {
		w := newWorld(t)
		broken := w.pushMain(w.shop)
		w.uploadPush(broken, fixtureBlocked)
		w.uploadPush(w.pushMain(w.shop), fixtureHealthy)

		w.editRun(broken, func(r *api.WorkflowRun) { r.Attempt = 2 }) // the old commit's workflow is re-run
		w.send(w.pushSpec(broken, testBranch, w.report(w.shop, fixtureCoverage)))
		if open := w.openIssues(w.shop); len(open) != 0 {
			t.Errorf("the old commit reopened the issue: %d open", len(open))
		}
	})

	t.Run("a late break of a commit after a newer break", func(t *testing.T) {
		w := newWorld(t)
		old := w.pushMain(w.shop)
		w.uploadPush(old, fixtureBlocked)
		current := w.pushMain(w.shop)
		w.uploadPush(current, fixtureBlocked)

		w.editRun(old, func(r *api.WorkflowRun) { r.Attempt = 2 })
		w.send(w.pushSpec(old, testBranch, w.report(w.shop, fixtureCoverage)))
		if got := w.theIssue(w.shop); !strings.Contains(got.Body, headLine(current)) {
			t.Errorf("the issue does not name the current commit any more:\n%.300s", got.Body)
		}
	})
}

// Only a push to the default branch speaks for it.
func TestOnlyDefaultBranchPushesTouchIssues(t *testing.T) {
	t.Run("pull request", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, p))
		w.wantAnswer(w.upload(p, fixtureBlocked), 200, "failure", "blocking_findings")
		if n := w.issueCalls(); n != 0 {
			t.Errorf("%d Issues calls for a pull request", n)
		}
	})
	t.Run("push to another branch", func(t *testing.T) {
		w := newWorld(t)
		p := w.pushMain(w.shop)
		w.wantAnswer(w.send(w.pushSpec(p, "feature", w.report(w.shop, fixtureBlocked))), 200, "action_required", "unsupported_event")
		if n := w.issueCalls(); n != 0 {
			t.Errorf("%d Issues calls for a push to another branch", n)
		}
	})
}

func TestRepairIssueAssignee(t *testing.T) {
	tests := []struct {
		name         string
		unassignable bool
		want         string
	}{
		{"assigned", false, "alice"},
		{"GitHub refuses the assignee", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			w := newWorld(t)
			w.setFile(w.shop, testBranch, settings.Path, settings.Render(settings.Settings{
				Version: 1, Owners: []string{ownerLogin}, Gate: settings.GateRequired, Workflow: testWorkflow,
				DataMode: settings.DataCustomer, RepairAssignee: "alice",
			}))
			w.gh.unassignable["alice"] = tt.unassignable
			w.uploadPush(w.pushMain(w.shop), fixtureBlocked)

			if got := w.theIssue(w.shop); got.Assignee != tt.want {
				t.Errorf("assignee = %q, want %q", got.Assignee, tt.want)
			}
			if logged := strings.Contains(logs.String(), "opened without its assignee"); logged != tt.unassignable {
				t.Errorf("assignee refusal logged = %v, want %v", logged, tt.unassignable)
			}
		})
	}
}
