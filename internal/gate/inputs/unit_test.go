package inputs

import (
	"fmt"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestProtectedPathsIntersection(t *testing.T) {
	all := []string{policy.PolicyPath, policy.BaselinePath, policy.LabelsPath, settings.Path, workflowPath}
	tests := []struct {
		name      string
		changed   []string
		truncated bool
		workflow  string
		want      []string
	}{
		{name: "none protected", changed: []string{"main.go", "docs/.archfit.yaml"}, workflow: workflowPath},
		{name: "policy only", changed: []string{"main.go", policy.PolicyPath}, workflow: workflowPath, want: []string{policy.PolicyPath}},
		{
			name:     "baseline and workflow in fixed order",
			changed:  []string{workflowPath, policy.BaselinePath},
			workflow: workflowPath,
			want:     []string{policy.BaselinePath, workflowPath},
		},
		{name: "settings", changed: []string{settings.Path}, workflow: workflowPath, want: []string{settings.Path}},
		{name: "labels", changed: []string{policy.LabelsPath}, workflow: workflowPath, want: []string{policy.LabelsPath}},
		{
			name:     "custom workflow path",
			changed:  []string{"ci/gate.yml", workflowPath},
			workflow: "ci/gate.yml",
			want:     []string{"ci/gate.yml"},
		},
		{
			name:     "policy renamed away lists the old path",
			changed:  []string{policy.PolicyPath, "archive/old-policy.yaml"},
			workflow: workflowPath,
			want:     []string{policy.PolicyPath},
		},
		{name: "truncated list", changed: []string{"main.go"}, truncated: true, workflow: workflowPath, want: all},
		{name: "all five", changed: []string{workflowPath, settings.Path, policy.LabelsPath, policy.BaselinePath, policy.PolicyPath}, workflow: workflowPath, want: all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantStrings(t, "protectedPaths", protectedPaths(tt.changed, tt.truncated, tt.workflow), tt.want)
		})
	}

	t.Run("truncated list requires approval", func(t *testing.T) {
		f := newFixture()
		f.pulls.truncated = true
		got := f.mustResolve(t, pullTarget())
		wantStrings(t, "ProtectedPaths", got.ProtectedPaths, all)
		if got.Approval != ApprovalMissing {
			t.Errorf("Approval = %q, want %q", got.Approval, ApprovalMissing)
		}
	})
}

func TestApprovalAlgorithm(t *testing.T) {
	owners := []string{"alice", teamOwner}
	tests := []struct {
		name          string
		owners        []string
		reviews       []api.Review
		want          string
		wantTeamCalls []string
	}{
		{name: "owner approved head", reviews: []api.Review{approved("alice", headSHA)}, want: "alice"},
		{name: "owner approved an older SHA", reviews: []api.Review{approved("alice", oldSHA)}},
		{
			name:          "non-owner approved head",
			reviews:       []api.Review{approved("mallory", headSHA)},
			wantTeamCalls: []string{teamOwner + ":mallory"},
		},
		{
			name: "owner approved then requested changes",
			reviews: []api.Review{
				approved("alice", headSHA),
				{Reviewer: "alice", State: "CHANGES_REQUESTED", CommitSHA: headSHA},
			},
		},
		{
			name: "owner approved then commented",
			reviews: []api.Review{
				approved("alice", headSHA),
				{Reviewer: "alice", State: "COMMENTED", CommitSHA: headSHA},
			},
			want: "alice",
		},
		{name: "owner approval dismissed", reviews: []api.Review{{Reviewer: "alice", State: "DISMISSED", CommitSHA: headSHA}}},
		{name: "login case differs", reviews: []api.Review{approved("Alice", headSHA)}, want: "Alice"},
		{
			name:          "team owner with member",
			reviews:       []api.Review{approved("carol", headSHA)},
			want:          "carol",
			wantTeamCalls: []string{teamOwner + ":carol"},
		},
		{
			name:          "team owner with non-member",
			reviews:       []api.Review{approved("dave", headSHA)},
			wantTeamCalls: []string{teamOwner + ":dave"},
		},
		{
			name:    "two owners, second approves",
			owners:  []string{"alice", "bob"},
			reviews: []api.Review{{Reviewer: "alice", State: "COMMENTED", CommitSHA: headSHA}, approved("bob", headSHA)},
			want:    "bob",
		},
		{
			name:    "no team lookup without a qualifying approval",
			reviews: []api.Review{approved("carol", oldSHA), {Reviewer: "dave", State: "CHANGES_REQUESTED", CommitSHA: headSHA}},
		},
		{
			name:    "a later review under another login case still wins",
			reviews: []api.Review{approved("alice", headSHA), {Reviewer: "ALICE", State: "DISMISSED", CommitSHA: headSHA}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pulls := newFixture().pulls
			if tt.owners == nil {
				tt.owners = owners
			}
			got, err := approver(tt.reviews, tt.owners, headSHA, func(team, login string) (bool, error) {
				return pulls.TeamMember(t.Context(), api.RepoRef{}, team, login)
			})
			if err != nil {
				t.Fatalf("approver: %v", err)
			}
			if got != tt.want {
				t.Errorf("approver = %q, want %q", got, tt.want)
			}
			wantStrings(t, "team lookups", pulls.teamCalls, tt.wantTeamCalls)
		})
	}
}

func TestSuspendedFormula(t *testing.T) {
	instants := []struct {
		name         string
		suspendAfter time.Time
		due          bool
	}{
		{"zero", time.Time{}, false},
		{"future", now.Add(time.Second), false},
		{"equal to now", now, true},
		{"past", now.Add(-time.Hour), true},
	}
	for _, private := range []bool{true, false} {
		for _, in := range instants {
			t.Run(fmt.Sprintf("private=%t/%s", private, in.name), func(t *testing.T) {
				want := private && in.due
				if got := suspended(private, Entitlement{SuspendAfter: in.suspendAfter}, now); got != want {
					t.Errorf("suspended = %t, want %t", got, want)
				}
			})
		}
	}
}

func TestEarliestWaiverExpiry(t *testing.T) {
	waiver := func(expires string) string {
		return "  - rule: no-cycles\n    reason: migration\n    expires: " + expires + "\n"
	}
	endOf := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 23, 59, 59, 0, time.UTC) }
	tests := []struct {
		name     string
		policy   string
		want     time.Time
		wantLogs int
	}{
		{name: "no waivers", policy: "version: 1\n"},
		{name: "one future", policy: "waivers:\n" + waiver("2026-10-01"), want: endOf(2026, 10, 1)},
		{name: "one expired and one future", policy: "waivers:\n" + waiver("2026-09-01") + waiver("2026-11-15"), want: endOf(2026, 11, 15)},
		{name: "earliest of two future", policy: "waivers:\n" + waiver("2026-11-15") + waiver("2026-10-01"), want: endOf(2026, 10, 1)},
		{name: "expires today still counts", policy: "waivers:\n" + waiver("2026-09-21"), want: endOf(2026, 9, 21)},
		{name: "all expired", policy: "waivers:\n" + waiver("2026-09-01") + waiver("2026-09-20")},
		{name: "malformed expires", policy: "waivers:\n" + waiver("soon") + waiver("2026-10-01"), wantLogs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			f := newFixture()
			f.repos.files[fileCall{baseRef, policy.PolicyPath}] = []byte(tt.policy)
			got := f.mustResolve(t, pushTarget())
			if !got.EarliestWaiverExpiry.Equal(tt.want) {
				t.Errorf("EarliestWaiverExpiry = %v, want %v", got.EarliestWaiverExpiry, tt.want)
			}
			if n := logLines(logs, "waivers"); n != tt.wantLogs {
				t.Errorf("waiver log lines = %d, want %d:\n%s", n, tt.wantLogs, logs)
			}
			wantStrings(t, "PolicyDigests", got.PolicyDigests, []string{policy.Digest([]byte(tt.policy))})
		})
	}
}
