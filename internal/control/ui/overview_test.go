package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const movedSummary = "- **Next:** The target branch moved, and with it this pull request closes a module cycle: orders, billing. Update the branch to measure it again.\n" +
	"- Reason: `target_moved` (evidence)\n- Detail: orders, billing; aaaaaaaaaaaa..bbbbbbbbbbbb\n"

func advisoryRepo(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.onb.progress = onboarding.Progress{Stage: onboarding.StageAdvisory}
	return e
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

// A pull request whose check could not be read is counted as unread: a count built from the others is a floor.
func TestUnreadChecksAreNeverCountedAsNothing(t *testing.T) {
	e := advisoryRepo(t)
	e.gh.fail["CheckRuns "+sha(4)+"#"+checkState] = errors.New("github 502")

	var overview statusBody
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/overview", &overview)
	if overview.PullsError || overview.PullsUnread != 1 {
		t.Errorf("overview: pulls error %v, unread %d; want the list read and 1 unread", overview.PullsError, overview.PullsUnread)
	}
}
