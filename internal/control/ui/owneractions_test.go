package ui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/control/answers"
	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

const ownerActionsAPI = "/api/owner-actions"

var waitingSince = time.Date(2026, 11, 19, 8, 30, 0, 0, time.UTC)

func workflowPull(e *env, updated time.Time) {
	e.gh.pulls[repoName+"#4"] = api.Pull{Number: 4, State: "open", HeadSHA: sha(4), HeadRepo: repoName, BaseRef: mainBranch, UpdatedAt: updated}
	e.gh.setFile(repoName, sha(4), workflow, "the changed workflow")
	e.gh.setFile(repoName, sha(4), ".archfit.yaml", homePolicy)
}

func TestOwnerActionsListsWorkflowChangeWithLinkAndAge(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	workflowPull(e, waitingSince)

	got := decodeAs[ownerActionsBody](t, e.as("alice", permRead).get(ownerActionsAPI))

	want := ownerActionRow{Repo: repoName, Kind: gate.OwnerWorkflow, Label: "Review the workflow change", Text: "Workflow change in pull request #4 waits for an owner.", Pull: 4,
		Since: "2026-11-19T08:30:00Z", Href: "/r/" + repoName + "/owner/workflow?pull=4"}
	if len(got.Actions) != 1 || got.Actions[0] != want || got.Unread != 0 || got.Truncated {
		t.Fatalf("owner actions = %+v, want only %+v", got, want)
	}
}

func TestOwnerActionsListsConfirmAndRevertForAPolicyChangedOutsideTheFlow(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	homeEditRepo(t, e, func(rec map[string]any) { rec["outside_flow"] = map[string]any{"digest": "other"} })

	got := decodeAs[ownerActionsBody](t, e.as("alice", permRead).get(ownerActionsAPI))

	var hrefs []string
	for _, a := range got.Actions {
		if a.Text == "" {
			t.Fatalf("missing text: %+v", a)
		}
		hrefs = append(hrefs, a.Href)
		if a.Pull != 0 || a.Since != "" {
			t.Fatalf("a repository action names no pull request or time: %+v", a)
		}
	}
	if len(hrefs) != 2 || hrefs[0] != "/r/"+repoName+"/owner/confirm" || hrefs[1] != "/r/"+repoName+"/owner/revert" {
		t.Fatalf("hrefs = %v", hrefs)
	}
}

func TestOwnerActionsSaysNothingForARepositoryWithoutAnApprovedPolicyAndReadsNoGitHub(t *testing.T) {
	e := newEnv(t)
	e.grant("alice", repoName, permRead)

	got := decodeAs[ownerActionsBody](t, e.signIn("alice").get(ownerActionsAPI))

	if len(got.Actions) != 0 || got.Unread != 0 {
		t.Fatalf("owner actions = %+v", got)
	}
	if calls := e.gh.called("OpenPulls"); len(calls) != 0 {
		t.Fatalf("a repository that is not set up read pulls: %v", calls)
	}
}

func TestOwnerActionsCountsARepositoryItCouldNotRead(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	e.gh.fail["OpenPulls"] = errors.New("boom")

	got := decodeAs[ownerActionsBody](t, e.as("alice", permRead).get(ownerActionsAPI))

	if len(got.Actions) != 0 || got.Unread != 1 {
		t.Fatalf("an unread repository is not an empty one: %+v", got)
	}
}

func TestOwnerActionsSaysWhenItReadsOnlyTheFirstRepositories(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	for i := maxOverviewRepos; i >= 0; i-- {
		name := fmt.Sprintf("acme/repo-%03d", i)
		e.addRepo(name, installA, int64(100+i))
		e.grant("alice", name, permRead)
	}

	got := decodeAs[ownerActionsBody](t, e.signIn("alice").get(ownerActionsAPI))

	if !got.Truncated {
		t.Fatalf("101 repositories are read as complete: %+v", got)
	}
}

func TestOwnerActionsNeedASession(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	wantStatus(t, e.anonymous().get(ownerActionsAPI), 401)
}

func TestHomeShowsOwnerActionsFirstWithTheirAge(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	workflowPull(e, waitingSince)
	homePut(t, e, "request-index", map[string]any{"schema": "archfit-app.request-index.v1", "entries": []any{map[string]any{"key": "graph app other", "id": "431"}}})
	homePut(t, e, "request/431", map[string]any{"schema": "archfit-app.request.v1", "key": "graph app other", "state": "open", "reason": "reason", "numbers": map[string]any{}})
	e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
		if a.Kind == gate.OwnerWorkflow {
			return nil
		}
		return gate.ErrOwnerNotDue
	}

	var got homeJSON
	e.as("alice", permRead).getJSON(homeAPI, &got)

	if len(got.NeedsYou) < 2 || got.NeedsYou[0].Kind != "workflow" || got.NeedsYou[0].Since != "2026-11-19T08:30:00Z" || got.NeedsYou[1].Kind != "request" {
		t.Fatalf("Needs you = %+v, want the workflow change first, with its time", got.NeedsYou)
	}
}

func TestPullShowsTheOwnerActionWhenNoResultIsStoredYet(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	workflowPull(e, waitingSince)
	e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
		if a.Kind == gate.OwnerWorkflow {
			return nil
		}
		return gate.ErrOwnerNotDue
	}

	var got pullJSON
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &got)

	if got.Review == nil || got.Review.Label != "Review workflow change" || got.Review.Href != "/r/"+repoName+"/owner/workflow?pull=4" {
		t.Fatalf("owner action = %+v", got.Review)
	}
}

func TestOwnerActionsIncludesRequestsExceptionsAndDriftInWaitOrder(t *testing.T) {
	e := newHomeEnv(t, homeWithException("2026-11-17"))
	workflowPull(e, waitingSince)
	homePut(t, e, "request-index", map[string]any{"schema": "archfit-app.request-index.v1", "entries": []any{map[string]any{"key": "graph app other", "id": "431"}, map[string]any{"key": "graph app util", "id": "432"}}})
	homePut(t, e, "request/431", map[string]any{"schema": "archfit-app.request.v1", "key": "graph app other", "state": "open", "reason": "reason", "numbers": map[string]any{}, "asked_at": "2026-11-01T10:00:00Z"})
	homePut(t, e, "request/432", map[string]any{"schema": "archfit-app.request.v1", "key": "graph app util", "state": "open", "reason": "reason", "numbers": map[string]any{}})
	homeEditRepo(t, e, func(rec map[string]any) { rec["drift_issue"] = 72 })
	got := decodeAs[ownerActionsBody](t, e.as("alice", permRead).get(ownerActionsAPI))
	for _, row := range got.Actions {
		if row.Text == "" {
			t.Fatalf("missing text: %+v", row)
		}
	}
	if got.Unread != 0 || got.Truncated || len(got.Actions) != 5 {
		t.Fatalf("actions = %+v", got)
	}
	if got.Actions[0].Kind != "request" || got.Actions[0].Since != "2026-11-01T10:00:00Z" || got.Actions[0].Href != "/r/"+repoName+"/requests/431" || got.Actions[1].Kind != "workflow" {
		t.Fatalf("ordered actions = %+v", got.Actions)
	}
	kinds := map[string]bool{}
	for _, row := range got.Actions[2:] {
		if row.Since != "" {
			t.Fatalf("undated row = %+v", row)
		}
		kinds[row.Kind] = true
	}
	if !kinds["request"] || !kinds["exception"] || !kinds["drift"] {
		t.Fatalf("missing undated kinds: %+v", got.Actions)
	}
}
