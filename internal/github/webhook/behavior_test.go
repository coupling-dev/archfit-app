package webhook

import (
	"reflect"
	"testing"
)

func TestReplayedDeliveryYieldsSameEvent(t *testing.T) {
	secret := []byte(testSecret)
	for _, tt := range fixtureCases {
		t.Run(tt.file, func(t *testing.T) {
			payload := fixture(t, tt.file)
			header := sign(secret, payload)

			deliver := func(deliveryID string) Event {
				t.Helper()
				if err := VerifyAny([][]byte{secret}, header, payload); err != nil {
					t.Fatalf("VerifyAny() error = %v", err)
				}
				ev, err := Parse(tt.eventType, deliveryID, payload)
				if err != nil {
					t.Fatalf("Parse() error = %v", err)
				}
				return ev
			}

			first, replay := deliver(testDelivery), deliver(testDelivery)
			if !reflect.DeepEqual(first, replay) {
				t.Errorf("replay =\n %+v, want\n %+v", replay, first)
			}

			const otherDelivery = "0b989ba4-242f-11e5-81e1-c7b6966d2516"
			redelivered := deliver(otherDelivery)
			if redelivered.DeliveryID != otherDelivery {
				t.Errorf("DeliveryID = %q, want %q", redelivered.DeliveryID, otherDelivery)
			}
			redelivered.DeliveryID = first.DeliveryID
			if !reflect.DeepEqual(first, redelivered) {
				t.Errorf("redelivery =\n %+v, want\n %+v apart from DeliveryID", redelivered, first)
			}
		})
	}
}

func TestPullRequestLifecycle(t *testing.T) {
	const (
		sha1 = "1111111111111111111111111111111111111111"
		sha2 = "2222222222222222222222222222222222222222"
	)
	steps := []struct {
		name      string
		eventType string
		payload   obj
		wantKind  EventKind
		wantSHA   string
	}{
		{"open", "pull_request", prPayload("opened", sha1), PullOpened, sha1},
		{"push to the PR", "pull_request", prPayload("synchronize", sha2), PullSynchronized, sha2},
		{"review submitted", "pull_request_review", reviewPayload("submitted", sha2), ReviewChanged, sha2},
		{"review dismissed", "pull_request_review", reviewPayload("dismissed", sha2), ReviewChanged, sha2},
		{"convert to draft", "pull_request", prPayload("converted_to_draft", sha2), Ignored, ""},
		{"ready for review", "pull_request", prPayload("ready_for_review", sha2), PullOpened, sha2},
		{"close", "pull_request", prPayload("closed", sha2), PullClosed, sha2},
		{"reopen", "pull_request", prPayload("reopened", sha2), PullOpened, sha2},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			ev := mustParse(t, step.eventType, step.payload)
			if ev.Kind != step.wantKind {
				t.Errorf("Kind = %q, want %q", ev.Kind, step.wantKind)
			}
			if ev.HeadSHA != step.wantSHA {
				t.Errorf("HeadSHA = %q, want %q", ev.HeadSHA, step.wantSHA)
			}
			if step.wantKind != Ignored && ev.PullRequest != 7 {
				t.Errorf("PullRequest = %d, want 7", ev.PullRequest)
			}
		})
	}
}

func TestInstallationLifecycle(t *testing.T) {
	steps := []struct {
		name      string
		eventType string
		payload   obj
		wantKind  EventKind
		wantID    int64
	}{
		{"installation created", "installation", installationPayload("created", 6001), InstallationAdded, 6001},
		{"repositories added", "installation_repositories", installationReposPayload("added", 6002), InstallationAdded, 6002},
		{"repositories removed", "installation_repositories", installationReposPayload("removed", 6003), ReposRemoved, 6003},
		{"suspend", "installation", installationPayload("suspend", 6004), Ignored, 6004},
		{"installation deleted", "installation", installationPayload("deleted", 6005), InstallationRemoved, 6005},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			ev := mustParse(t, step.eventType, step.payload)
			if ev.Kind != step.wantKind {
				t.Errorf("Kind = %q, want %q", ev.Kind, step.wantKind)
			}
			if ev.InstallationID != step.wantID {
				t.Errorf("InstallationID = %d, want %d", ev.InstallationID, step.wantID)
			}
			if ev.Repo != "" || ev.RepoPrivate || ev.PullRequest != 0 || ev.HeadSHA != "" {
				t.Errorf("installation event carries repository or pull request fields: %+v", ev)
			}
		})
	}
}

func TestBranchPushFields(t *testing.T) {
	payload := set(set(pushPayload("refs/heads/feature/x", "main"), "after", shaA), "sender.login", "octocat")
	ev := mustParse(t, "push", payload)
	want := Event{
		Kind: BranchPush, DeliveryID: testDelivery, InstallationID: 5001, Repo: "acme/shop", RepoPrivate: false,
		HeadSHA: shaA, HeadRepo: "acme/shop", DefaultBranch: "main", Branch: "feature/x", Pusher: "octocat",
	}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("Parse() = %+v, want %+v", ev, want)
	}
}

func TestPushKinds(t *testing.T) {
	const zeroSHA = "0000000000000000000000000000000000000000"
	tests := []struct {
		name     string
		payload  obj
		wantKind EventKind
		wantBase string
	}{
		{"push to main (default)", pushPayload("refs/heads/main", "main"), DefaultBranchPush, "main"},
		{"push to a feature branch", pushPayload("refs/heads/feature/x", "main"), BranchPush, ""},
		{"tag push", pushPayload("refs/tags/v1.0.0", "main"), Ignored, ""},
		{"deletion of a branch", set(set(pushPayload("refs/heads/main", "main"), "deleted", true), "after", zeroSHA), Ignored, ""},
		{"deletion of a feature branch", set(set(pushPayload("refs/heads/feature/x", "main"), "deleted", true), "after", zeroSHA), Ignored, ""},
		{"default branch is trunk", pushPayload("refs/heads/trunk", "trunk"), DefaultBranchPush, "trunk"},
		{"push to main when the default is trunk", pushPayload("refs/heads/main", "trunk"), BranchPush, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := mustParse(t, "push", tt.payload)
			if ev.Kind != tt.wantKind {
				t.Fatalf("Kind = %q, want %q", ev.Kind, tt.wantKind)
			}
			if ev.BaseRef != tt.wantBase {
				t.Errorf("BaseRef = %q, want %q", ev.BaseRef, tt.wantBase)
			}
			if tt.wantKind == DefaultBranchPush && (ev.HeadRepo != ev.Repo || ev.HeadSHA != shaA || ev.PullRequest != 0) {
				t.Errorf("push fields do not follow the Field sources rule: %+v", ev)
			}
		})
	}
}

func TestBranchPushCarriesPusherType(t *testing.T) {
	tests := []struct {
		name       string
		pusherType any
		want       string
	}{
		{"absent", nil, ""},
		{"user", "user", "user"},
		{"deploy key", "deploy_key", "deploy_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := set(pushPayload("refs/heads/feature/x", "main"), "sender.login", "octocat")
			if tt.pusherType != nil {
				set(payload, "pusher_type", tt.pusherType)
			}
			ev := mustParse(t, "push", payload)
			if ev.PusherType != tt.want || ev.Pusher != "octocat" {
				t.Errorf("PusherType = %q, Pusher = %q; want %q, octocat", ev.PusherType, ev.Pusher, tt.want)
			}
		})
	}
}
