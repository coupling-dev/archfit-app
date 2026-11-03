package inputs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestReviewerRequestsOnlyMissingEligiblePrincipalsAndDedupesHead(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob", teamOwner, "dave", "agent"}, "agent")
	f.pulls.changed = []string{policy.BaselinePath}
	f.pulls.teams[teamOwner] = []string{"carol"}
	f.pulls.requestedUsers = []string{"dave"} // an existing CODEOWNERS/manual request
	f.pulls.reviews = []api.Review{approved("alice", headSHA), approved("bob", oldSHA)}
	target := pullTarget()
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), target, "alice", now); problem {
		t.Fatal("review request failed")
	}
	if !reflect.DeepEqual(f.pulls.lastRequestedUsers, []string{"bob"}) || !reflect.DeepEqual(f.pulls.lastRequestedTeams, []string{teamOwner}) {
		t.Fatalf("requested users/teams = %v / %v (calls=%d teamCalls=%v, requested=%v/%v), want only Bob and the team", f.pulls.lastRequestedUsers, f.pulls.lastRequestedTeams, f.pulls.requestCalls, f.pulls.teamCalls, f.pulls.requestedUsers, f.pulls.requestedTeams)
	}
	if f.pulls.requestCalls != 1 {
		t.Fatalf("request calls = %d, want one", f.pulls.requestCalls)
	}
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), target, "alice", now.Add(time.Minute)); problem {
		t.Fatal("deduped request reported a failure")
	}
	if f.pulls.requestCalls != 1 {
		t.Fatalf("unchanged head sent another notification: calls=%d", f.pulls.requestCalls)
	}
}

func TestReviewerRequestsNewOwnerAfterSettingsChangeAtSameHead(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	first := pullTarget()
	first.BaseSHA = "settings-one"
	f.repos.files[fileCall{first.BaseSHA, settings.Path}] = settings.Render(settings.Settings{
		Version: 1, Owners: []string{"alice", "bob"}, Gate: settings.GateRequired, Workflow: workflowPath,
	})
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), first, "alice", now); problem {
		t.Fatal("initial reviewer request failed")
	}
	if !reflect.DeepEqual(f.pulls.lastRequestedUsers, []string{"bob"}) {
		t.Fatalf("first requested users = %v, want Bob", f.pulls.lastRequestedUsers)
	}

	second := first
	second.BaseSHA = "settings-two"
	f.repos.files[fileCall{second.BaseSHA, settings.Path}] = settings.Render(settings.Settings{
		Version: 1, Owners: []string{"alice", "carol"}, Gate: settings.GateRequired, Workflow: workflowPath,
	})
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), second, "alice", now.Add(time.Minute)); problem {
		t.Fatal("review request after owner change failed")
	}
	if f.pulls.requestCalls != 2 || !reflect.DeepEqual(f.pulls.lastRequestedUsers, []string{"carol"}) {
		t.Fatalf("new-settings request calls/users = %d / %v, want one request for only Carol", f.pulls.requestCalls, f.pulls.lastRequestedUsers)
	}
	if !reflect.DeepEqual(f.pulls.requestedUsers, []string{"bob", "carol"}) {
		t.Fatalf("requested users after settings change = %v, want prior Bob and new Carol", f.pulls.requestedUsers)
	}
}

func TestReviewerRequestsSkipTeamContainingPRAuthor(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", teamOwner})
	f.pulls.changed = []string{policy.BaselinePath}
	f.pulls.teams[teamOwner] = []string{"alice"}
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), pullTarget(), "alice", now); problem {
		t.Fatal("a skipped self-review is not an API notification failure")
	}
	if f.pulls.requestCalls != 0 {
		t.Fatalf("requested a configured team containing the PR author: %+v", f.pulls.lastRequestedTeams)
	}
}

func TestReviewerRequestFailureIsBoundedAndDoesNotGrantAuthority(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	f.pulls.requestErr = errors.New("GitHub permission denied")
	target := pullTarget()
	for i := 0; i < reviewerAttempts; i++ {
		if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), target, "alice", now.Add(time.Duration(i)*time.Minute)); !problem {
			t.Fatalf("attempt %d did not disclose notification failure", i+1)
		}
	}
	if f.pulls.requestCalls != reviewerAttempts {
		t.Fatalf("request calls = %d, want bounded %d", f.pulls.requestCalls, reviewerAttempts)
	}
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), target, "alice", now.Add(time.Hour)); !problem {
		t.Fatal("exhausted notification problem was hidden")
	}
	if f.pulls.requestCalls != reviewerAttempts {
		t.Fatalf("attempt cap exceeded: calls=%d", f.pulls.requestCalls)
	}
	trusted := f.mustResolve(t, target)
	wantApproval(t, trusted, ApprovalMissing, "")
}

func TestReviewerMembershipFailureIsDisclosedAndSkipsTeam(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{teamOwner})
	f.pulls.changed = []string{policy.BaselinePath}
	f.pulls.teamErr = errors.New("membership endpoint unavailable")
	if problem := New(f.repos, f.pulls, nil, f.store).(*resolver).RequestEligibleReviewers(context.Background(), pullTarget(), "author", now); !problem {
		t.Fatal("membership failure was not disclosed")
	}
	if f.pulls.requestCalls != 0 {
		t.Fatal("requested a team without confirming the PR author is not in it")
	}
}
