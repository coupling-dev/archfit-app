package gate

import (
	"context"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// branchPush is the push webhook that moved p's branch to its current head, sent by login.
func (w *world) branchPush(p *pull, login string) webhook.Event {
	pl := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	return webhook.Event{
		Kind: webhook.BranchPush, DeliveryID: "delivery-branch-push", InstallationID: p.repo.InstallationID,
		Repo: p.repo.FullName, RepoPrivate: p.repo.Private, HeadSHA: p.head, HeadRepo: pl.HeadRepo,
		Branch: pl.HeadRef, Pusher: login,
	}
}

// setOwners writes the settings file of r at ref with the given owners and agents.
func (w *world) setOwners(r api.Repo, ref string, owners []string, agents ...string) {
	w.setFile(r, ref, settings.Path, settings.Render(settings.Settings{
		Version: 1, Owners: owners, Agents: agents, Gate: settings.GateRequired, Workflow: testWorkflow,
		DataMode: settings.DataCustomer,
	}))
}

// editsBaseline gives the default branch a baseline and makes the pull replace it; it returns the head digest.
func (w *world) editsBaseline(p *pull) string {
	w.setFile(p.repo, testBranch, policy.BaselinePath, []byte(`{"violations":[]}`))
	w.changes(p, "pkg/a/a.go", policy.BaselinePath)
	w.setFile(p.repo, p.head, policy.BaselinePath, []byte(`{"violations":["accepted by an agent"]}`))
	return w.digestOf(p.repo, p.head, policy.BaselinePath)
}

// baselineUpload is a healthy upload of p's current run that used the baseline with digest.
func (w *world) baselineUpload(p *pull, digest string) uploadSpec {
	s := w.spec(p, w.report(p.repo, fixtureHealthy, gateReference("comparable")))
	s.env.BaselineDigest = digest
	return s
}

// missingApproval opens a pull request that replaces the baseline and leaves an owner_approval_missing receipt on it.
func (w *world) missingApproval(number int) (p *pull, digest string) {
	p = w.openPull(w.shop, number)
	digest = w.editsBaseline(p)
	w.handle(w.event(webhook.PullOpened, p))
	w.wantAnswer(w.send(w.baselineUpload(p, digest)), 200, "action_required", "owner_approval_missing")
	return p, digest
}

// A local coding agent pushes with its owner's credentials, so no push is an approval: an owner-identity push that
// replaces the baseline still needs an owner to approve the exact commit.
func TestOwnerPushNeverApproves(t *testing.T) {
	t.Run("the push before the report", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		digest := w.editsBaseline(p)
		w.handle(w.branchPush(p, ownerLogin))
		w.handle(w.event(webhook.PullOpened, p))

		w.wantAnswer(w.send(w.baselineUpload(p, digest)), 200, "action_required", "owner_approval_missing")
		w.noSuccess()
	})

	t.Run("the report before the push", func(t *testing.T) {
		w := newWorld(t)
		p, digest := w.missingApproval(7)

		w.handle(w.branchPush(p, ownerLogin))
		if len(w.gh.reruns) != 0 {
			t.Errorf("reruns = %v, want none", w.gh.reruns)
		}
		w.wantAnswer(w.send(w.baselineUpload(p, digest)), 200, "action_required", "owner_approval_missing")
	})

	t.Run("a member of an owner team", func(t *testing.T) {
		w := newWorld(t)
		w.setOwners(w.shop, testBranch, []string{"acme/architects"})
		w.gh.teams["acme/architects|carol"] = true
		p, _ := w.missingApproval(7)

		w.handle(w.branchPush(p, "carol"))
		if len(w.gh.reruns) != 0 {
			t.Errorf("reruns = %v, want none", w.gh.reruns)
		}
	})
}

// The record of who pushed a head stays, for attribution; a deploy key's push records nothing.
func TestPusherTypeOtherThanUserRecordsNothing(t *testing.T) {
	tests := []struct {
		pusherType string
		recorded   bool
	}{
		{"", true}, // absent: sender is trusted
		{"user", true},
		{"deploy_key", false},
		{"something_new", false},
	}
	for _, tt := range tests {
		t.Run("pusher_type "+tt.pusherType, func(t *testing.T) {
			w := newWorld(t)
			p, _ := w.missingApproval(7)
			push := w.branchPush(p, ownerLogin)
			push.PusherType = tt.pusherType

			w.handle(push)
			_, err := w.mem.Get(context.Background(), shopInstallation, pusherKeyPrefix+shopName+"/"+p.head)
			if recorded := err == nil; recorded != tt.recorded {
				t.Errorf("recorded = %v, want %v", recorded, tt.recorded)
			}
			if len(w.gh.reruns) != 0 {
				t.Errorf("reruns = %v, want none", w.gh.reruns)
			}
		})
	}
}

func TestPusherRecordNeverAuthorizesAFork(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.editPull(p, func(pl *api.Pull) { pl.HeadRepo = "mallory/shop" })
	w.editsPolicy(p)
	w.handle(w.branchPush(p, ownerLogin)) // the fork's head is not a branch of this repository
	w.handle(w.event(webhook.PullOpened, p))

	w.wantGate(p, runCompleted, "action_required")
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "action_required", "fork_unsupported")
	if len(w.gh.reruns) != 0 {
		t.Errorf("reruns = %v, want none", w.gh.reruns)
	}
}

func TestSourceOnlyPushToTheDefaultBranchStillWritesNothing(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
	before := w.gh.writes()

	push := w.pushEvent(w.shop, "pkg/a/a.go")
	push.Branch, push.Pusher = testBranch, ownerLogin
	w.handle(push)
	if w.gh.writes() != before || !slices.Equal(w.gh.reruns, nil) {
		t.Errorf("writes %d → %d, reruns %v", before, w.gh.writes(), w.gh.reruns)
	}
}
