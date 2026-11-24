package gate

import (
	"context"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestFirstPersonIsNeverATeamOrAPlaceholder(t *testing.T) {
	for _, tt := range []struct {
		owners []string
		want   string
	}{
		{[]string{"alexei-led", "oscar"}, "alexei-led"},
		{[]string{"acme/arch", "@oscar"}, "oscar"},
		{[]string{"<your-login>", "acme/arch"}, ""},
		{[]string{"-bad", "bad-", "a--b"}, ""},
		{nil, ""},
	} {
		if got := firstPerson(tt.owners); got != tt.want {
			t.Errorf("firstPerson(%v) = %q, want %q", tt.owners, got, tt.want)
		}
	}
}

func TestStoryReaderFollowsTheAnswerToThePolicyPullRequest(t *testing.T) {
	ctx := context.Background()
	gh := newFakeGitHub()
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, DefaultBranch: "main"}
	gh.files[fileKey(repo.FullName, "main", policyPath)] = []byte("version: 1\nowners: [acme/arch, \"@alexei-led\"]\nmodules:\n  a:\n    paths: [a/**]\n")
	s := store.NewMemory()
	put := func(key string, v records.Value) {
		raw, err := records.Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, store.Tenant(7), key, raw); err != nil {
			t.Fatal(err)
		}
	}
	put(records.RequestKey(repo.FullName, 40), &records.Request{State: records.StateAnswered, Approval: "a1"})
	put(records.ApprovalKey(repo.FullName, "a1"), &records.Approval{Proposal: "p1", State: records.StateOpen})
	put(records.ProposalKey(repo.FullName, "p1"), &records.Proposal{PullRequest: 44, State: records.StateOpen})
	for _, tt := range []struct {
		kind, owner string
	}{{"User", "alexei-led"}, {"Organization", ""}, {"", ""}} {
		gh.accounts = map[string]string{}
		if tt.kind != "" {
			gh.accounts["alexei-led"] = tt.kind
		}
		got, err := storyReader(gh, s)(ctx, repo, 40)
		want := feedback.StoryFacts{State: records.StateAnswered, PolicyPull: 44, Owner: tt.owner}
		if err != nil || got != want {
			t.Errorf("account %q: facts = %+v, %v; want %+v", tt.kind, got, err, want)
		}
	}
}

func TestStoryReaderSaysWhenAnOpenRequestWaitsOnTheFirstApproval(t *testing.T) {
	ctx := context.Background()
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, DefaultBranch: "main"}
	for _, tt := range []struct {
		name     string
		state    string
		approved string
		policy   bool
		want     bool
	}{
		{"open, no first approval", records.StateOpen, "", true, true},
		{"open, approved policy", records.StateOpen, "sha256:approved", true, false},
		{"answered", records.StateAnswered, "", true, false},
		{"open, no valid policy to approve", records.StateOpen, "", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			put := func(key string, v records.Value) {
				raw, err := records.Encode(v)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Put(ctx, store.Tenant(7), key, raw); err != nil {
					t.Fatal(err)
				}
			}
			put(records.RequestKey(repo.FullName, 40), &records.Request{State: tt.state})
			if tt.approved != "" {
				put(records.RepoKey(repo.FullName), &records.Repo{ApprovedDigest: tt.approved})
			}
			gh := newFakeGitHub()
			if tt.policy {
				gh.files[fileKey(repo.FullName, "main", policyPath)] = []byte("version: 1\nowners: [acme/arch]\nmodules:\n  a:\n    paths: [a/**]\n")
			}
			got, err := storyReader(gh, s)(ctx, repo, 40)
			if err != nil || got.FirstApproval != tt.want {
				t.Errorf("facts = %+v, %v; want first approval %v", got, err, tt.want)
			}
		})
	}
}
