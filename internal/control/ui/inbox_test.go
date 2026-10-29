package ui

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

const movedSummary = "- **Next:** The target branch moved, and with it this pull request closes a module cycle: orders, billing. Update the branch to measure it again.\n" +
	"- Reason: `target_moved` (evidence)\n- Detail: orders, billing; aaaaaaaaaaaa..bbbbbbbbbbbb\n"

func advisoryRepo(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.onb.progress = onboarding.Progress{Stage: onboarding.StageAdvisory}
	return e
}

func record(pull int, finding string, status decision.Status, at time.Time) gate.DecisionRecord {
	return gate.DecisionRecord{
		PullRequest: pull, FindingID: finding, RuleID: "boundary.no-import", EdgeFrom: "orders", EdgeTo: "billing",
		HeadSHA: sha(pull), Requester: "agent-7", Status: status, CreatedAt: at,
	}
}

func TestOverviewNamesTheLastCheckedMergeOfTheDefaultBranch(t *testing.T) {
	e := advisoryRepo(t)
	started := time.Date(2026, 9, 21, 9, 30, 0, 0, time.UTC)
	e.gh.checks[repoName+"@"+sha(10)+"#"+checkState] = []api.CheckRun{
		{ID: 1, Conclusion: "success", Title: "Architecture healthy", StartedAt: started.Add(-time.Hour)},
		{ID: 2, Conclusion: "failure", Title: "Architecture violations found", StartedAt: started},
	}
	var body statusBody
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/overview", &body)
	if body.MainState != "violations found" || body.MainSHA != sha(10)[:shortSHA] || body.MainAt != "2026-09-21T09:30:00Z" {
		t.Errorf("main = %q on %q at %q; want the latest run", body.MainState, body.MainSHA, body.MainAt)
	}

	t.Run("no check, no time", func(t *testing.T) {
		e := advisoryRepo(t)
		var body statusBody
		e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/overview", &body)
		if body.MainState != stateNone || body.MainSHA != "" || body.MainAt != "" {
			t.Errorf("main = %q on %q at %q", body.MainState, body.MainSHA, body.MainAt)
		}
	})
}

func TestOverviewCountsPullRequestsToUpdateAfterTheTargetMoved(t *testing.T) {
	tests := []struct {
		name   string
		run    api.CheckRun
		rerun  string
		update int
	}{
		{
			"target_moved names the modules the comment's way",
			api.CheckRun{Conclusion: "action_required", Title: "Action required", Summary: movedSummary},
			"Pull request author: The target branch moved, and with it this pull request closes a module cycle: orders, billing. Update the branch to measure it again.", 1,
		},
		{
			"a move without a recorded step still asks for an update",
			api.CheckRun{Conclusion: "action_required", Title: "Action required", Summary: "- Reason: `target_moved` (evidence)\n"},
			"Pull request author: " + updateBranchSentence, 1,
		},
		{"another reason is not a move", api.CheckRun{Conclusion: "action_required", Title: "Action required", Summary: strings.Replace(movedSummary, "target_moved", "stale_inputs", 1)}, "", 0},
		{"a result that is not withdrawn is not a move", api.CheckRun{Conclusion: "success", Title: "Architecture healthy", Summary: movedSummary}, "", 0},
		{"a check without a summary", api.CheckRun{Conclusion: "action_required", Title: "Action required"}, "", 0},
		{"repository text in the step stays data", api.CheckRun{Conclusion: "action_required", Title: "Action required", Summary: "- **Next:** closes a cycle: &lt;img src=x&gt;. Update.\n- Reason: `target_moved` (evidence)\n"},
			"Pull request author: closes a cycle: <img src=x>. Update.", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := advisoryRepo(t)
			tt.run.ID, tt.run.HeadSHA = 9, sha(4)
			e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{tt.run}
			var body statusBody
			e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/overview", &body)
			if len(body.Pulls) != 1 || body.Pulls[0].Rerun != tt.rerun || body.PullsToUpdate != tt.update {
				t.Errorf("pulls = %+v, to update = %d; want rerun %q and %d", body.Pulls, body.PullsToUpdate, tt.rerun, tt.update)
			}
		})
	}
}

func TestOverviewListsPendingDecisionRequests(t *testing.T) {
	at := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	path := "/api/repos/" + repoName + "/overview"
	t.Run("counts all, lists the pending ones read", func(t *testing.T) {
		e := advisoryRepo(t)
		e.queue.queue = gate.PendingDecisions{Open: 7, Records: []gate.DecisionRecord{
			record(4, "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a", decision.StatusPending, at),
			record(4, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", decision.StatusStale, at),
		}}
		var body statusBody
		e.as("alice", permRead).getJSON(path, &body)
		want := decisionRow{
			Pull: 4, Title: "feat: read options", Status: "pending", Finding: "0a0a0a0a", Rule: "boundary.no-import", Edge: "orders → billing",
			Requester: "agent-7", Head: sha(4)[:shortSHA], Created: "2026-09-21T08:00:00Z", Href: "/r/" + repoName + "/pull/4",
		}
		if body.DecisionsPending != 7 || len(body.Decisions) != 1 || body.Decisions[0] != want || body.DecisionsError {
			t.Errorf("pending = %d, rows = %+v, error = %v; want 7 and [%+v]", body.DecisionsPending, body.Decisions, body.DecisionsError, want)
		}
		if len(e.queue.reads) != 1 || e.queue.limits[0] != maxNeedsYouDecisions || e.queue.reads[0].Repo.FullName != repoName {
			t.Errorf("reads = %+v with limits %v; want one read, bounded, for the repository", e.queue.reads, e.queue.limits)
		}
	})
	t.Run("text of an agent is cut", func(t *testing.T) {
		e := advisoryRepo(t)
		rec := record(4, "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a", decision.StatusPending, at)
		rec.RuleID, rec.EdgeFrom, rec.EdgeTo, rec.Requester = strings.Repeat("r", 500), strings.Repeat("é", 500), strings.Repeat("b", 500), strings.Repeat("u", 500)
		e.queue.queue = gate.PendingDecisions{Open: 1, Records: []gate.DecisionRecord{rec}}
		var body statusBody
		e.as("alice", permRead).getJSON(path, &body)
		r := body.Decisions[0]
		for name, text := range map[string]string{"rule": r.Rule, "requester": r.Requester} {
			if got := len([]rune(text)); got != maxRequestText+1 {
				t.Errorf("%s is %d runes; want %d and a mark of the cut", name, got, maxRequestText+1)
			}
		}
		if got := len([]rune(r.Edge)); got != 2*(maxRequestText+1)+len([]rune(" → ")) {
			t.Errorf("edge is %d runes", got)
		}
	})
	t.Run("a repository without an open pull request is not asked", func(t *testing.T) {
		e := advisoryRepo(t)
		delete(e.gh.pulls, repoName+"#4")
		var body statusBody
		e.as("alice", permRead).getJSON(path, &body)
		if len(e.queue.reads) != 0 || body.DecisionsPending != 0 || len(body.Decisions) != 0 {
			t.Errorf("reads = %d, body = %d %v", len(e.queue.reads), body.DecisionsPending, body.Decisions)
		}
	})
	t.Run("before the first report nothing is read", func(t *testing.T) {
		e := newEnv(t)
		e.onb.progress = onboarding.Progress{Stage: onboarding.StageWorkflowMerged}
		var body statusBody
		e.as("alice", permRead).getJSON(path, &body)
		if len(e.queue.reads) != 0 || body.DecisionsPending != 0 {
			t.Errorf("reads = %d, pending = %d", len(e.queue.reads), body.DecisionsPending)
		}
	})
	t.Run("a failed read is said, not shown as none", func(t *testing.T) {
		e := advisoryRepo(t)
		e.queue.err = errors.New("store down")
		e.queue.queue = gate.PendingDecisions{Open: 3}
		var body statusBody
		e.as("alice", permRead).getJSON(path, &body)
		if !body.DecisionsError || body.DecisionsPending != 0 || len(body.Decisions) != 0 || body.MainState == "" {
			t.Errorf("body = %+v", body)
		}
	})
}

func TestDecisionsPage(t *testing.T) {
	path := "/api/repos/" + repoName + "/decisions"
	at := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	t.Run("pending first, newest first, read only", func(t *testing.T) {
		e := advisoryRepo(t)
		e.queue.queue = gate.PendingDecisions{Open: 3, Records: []gate.DecisionRecord{
			record(4, "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c", decision.StatusStale, at.Add(3*time.Hour)),
			record(4, "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a", decision.StatusPending, at),
			record(4, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", decision.StatusPending, at.Add(time.Hour)),
		}}
		var body inboxBody
		e.as("alice", permRead).getJSON(path, &body)
		var order []string
		for _, r := range body.Requests {
			order = append(order, r.Finding+" "+r.Status)
		}
		if got, want := strings.Join(order, ","), "0b0b0b0b pending,0a0a0a0a pending,0c0c0c0c stale"; got != want {
			t.Errorf("order = %s; want %s", got, want)
		}
		if body.Requests[0].Title != "feat: read options" || body.Requests[0].Href != "/r/"+repoName+"/pull/4" || body.RequestsMore != 1 || body.RequestsError {
			t.Errorf("first row = %+v, more = %d", body.Requests[0], body.RequestsMore)
		}
		if res := e.as("alice", permRead).post(path, nil); res.Code != http.StatusMethodNotAllowed && res.Code != http.StatusNotFound {
			t.Errorf("POST answered %d", res.Code)
		}
		if len(e.approvals.approved)+len(e.approvals.reevaluated) != 0 {
			t.Error("the decisions page decided something")
		}
	})
	t.Run("empty", func(t *testing.T) {
		e := advisoryRepo(t)
		var body inboxBody
		e.as("alice", permRead).getJSON(path, &body)
		if body.Requests == nil || len(body.Requests) != 0 || body.Policy == nil || len(body.Policy) != 0 {
			t.Errorf("body = %+v; want empty lists, not null", body)
		}
	})
	t.Run("policy changes come from the card on the check", func(t *testing.T) {
		e := advisoryRepo(t)
		e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{{
			ID: 9, HeadSHA: sha(4), Conclusion: "success", Title: "Architecture gate passed", Summary: stateSummary(t, true),
		}}
		e.gh.pulls[repoName+"#5"] = api.Pull{Number: 5, Title: "docs", State: "open", HeadSHA: sha(5), BaseRef: mainBranch}
		e.gh.checks[repoName+"@"+sha(5)+"#"+checkState] = []api.CheckRun{{ID: 10, HeadSHA: sha(5), Conclusion: "success", Title: "Architecture healthy", Summary: stateSummary(t, false)}}
		for _, tt := range []struct {
			name, approval string
			grant          bool
			readErr        error
			by             string
		}{
			{"missing", approvalMissing, false, nil, ""},
			{"granted", approvalGranted, true, nil, "alice"},
			{"unreadable", approvalUnknown, false, errors.New("down"), ""},
			{"none needed", approvalNotNeeded, false, nil, ""},
		} {
			e.approvals.approval = gate.PullApproval{Granted: tt.grant, ApprovedBy: tt.by}
			e.approvals.readErr = tt.readErr
			if tt.approval != approvalNotNeeded {
				e.approvals.approval.Paths = []string{".archfit.yaml"}
			}
			var body inboxBody
			e.as("alice", permRead).getJSON(path, &body)
			want := policyChangeRow{Number: 4, Title: "feat: read options", Headline: "This pull request loosens the policy (1 change).", Approval: tt.approval, By: tt.by, Href: "/r/" + repoName + "/pull/4"}
			if len(body.Policy) != 1 || body.Policy[0] != want {
				t.Errorf("%s: policy changes = %+v; want [%+v] (pull request 5 has no card)", tt.name, body.Policy, want)
			}
		}
	})
	t.Run("a failed read of the requests leaves the pull requests", func(t *testing.T) {
		e := advisoryRepo(t)
		e.queue.err = errors.New("store down")
		var body inboxBody
		e.as("alice", permRead).getJSON(path, &body)
		if !body.RequestsError || len(body.Requests) != 0 || body.PolicyError {
			t.Errorf("body = %+v", body)
		}
	})
	t.Run("a failed list of pull requests is said", func(t *testing.T) {
		e := advisoryRepo(t)
		e.gh.fail["OpenPulls"] = api.ErrRateLimited
		var body inboxBody
		e.as("alice", permRead).getJSON(path, &body)
		if !body.PolicyError || len(body.Policy) != 0 {
			t.Errorf("body = %+v", body)
		}
	})
	t.Run("the page and the data need a session and the repository", func(t *testing.T) {
		e := advisoryRepo(t)
		wantStatus(t, e.anonymous().get(path), http.StatusUnauthorized)
		wantStatus(t, e.anonymous().get("/r/"+repoName+"/decisions"), http.StatusSeeOther)
		wantStatus(t, e.as("mallory", "").get(path), http.StatusNotFound)
		if len(e.queue.reads) != 0 {
			t.Error("the queue was read without access")
		}
	})
}
