package gate

import (
	"context"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const outsiderLogin = "bob"

// branchPush is the push webhook that moved p's branch to its current head, sent by login.
func (w *world) branchPush(p *pull, login string) webhook.Event {
	pl := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	return webhook.Event{
		Kind: webhook.BranchPush, DeliveryID: "delivery-branch-push", InstallationID: p.repo.InstallationID,
		Repo: p.repo.FullName, RepoPrivate: p.repo.Private, HeadSHA: p.head, HeadRepo: pl.HeadRepo,
		Branch: pl.HeadRef, Pusher: login,
	}
}

// setOwners writes the settings file of r at ref with the given owners.
func (w *world) setOwners(r api.Repo, ref string, owners ...string) {
	w.setFile(r, ref, settings.Path, settings.Render(settings.Settings{
		Version: 1, Owners: owners, Gate: settings.GateRequired, Workflow: testWorkflow, DataMode: settings.DataCustomer,
	}))
}

// missingApproval opens a pull request that edits the policy and leaves an owner_approval_missing receipt on it.
func (w *world) missingApproval(number int) (*pull, []byte) {
	p := w.openPull(w.shop, number)
	digest := w.editsPolicy(p)
	w.handle(w.event(webhook.PullOpened, p))
	body := w.report(w.shop, fixtureHealthy, configHash(digest))
	w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")
	return p, body
}

func TestOwnerPushAuthorizesTheHeadItPushed(t *testing.T) {
	t.Run("webhook before report: push, open, then the report passes", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		digest := w.editsPolicy(p)
		w.handle(w.branchPush(p, ownerLogin))
		w.handle(w.event(webhook.PullOpened, p))

		w.wantAnswer(w.upload(p, fixtureHealthy, configHash(digest)), 200, "success", "healthy")
		if got := w.receipt(p).PolicyDigest; got != digest {
			t.Errorf("receipt policy digest = %s, want the head digest the owner's push authorized", got)
		}
	})

	t.Run("report before webhook: the push re-runs the workflow", func(t *testing.T) {
		w := newWorld(t)
		p, body := w.missingApproval(7)

		w.handle(w.branchPush(p, ownerLogin))
		if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
			t.Fatalf("reruns = %v, want [%d]", w.gh.reruns, p.runID)
		}
		w.wantGate(p, runCompleted, "action_required") // the push alone turns nothing green
		w.wantAnswer(w.send(w.spec(p, body)), 200, "success", "healthy")
	})

	t.Run("a team member counts as an owner", func(t *testing.T) {
		w := newWorld(t)
		w.setOwners(w.shop, testBranch, "acme/architects")
		w.gh.teams["acme/architects|carol"] = true
		p, _ := w.missingApproval(7)

		w.handle(w.branchPush(p, "carol"))
		if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
			t.Errorf("reruns = %v, want [%d]", w.gh.reruns, p.runID)
		}
	})
}

func TestPushDoesNotAuthorizeUnlessAnOwnerPushedTheExactHead(t *testing.T) {
	t.Run("a non-owner push grants nothing", func(t *testing.T) {
		w := newWorld(t)
		p, body := w.missingApproval(7)

		w.handle(w.branchPush(p, outsiderLogin))
		if len(w.gh.reruns) != 0 {
			t.Errorf("reruns = %v, want none", w.gh.reruns)
		}
		w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")
	})

	t.Run("a non-owner push after an owner push does not carry the authorization over", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		digest := w.editsPolicy(p)
		w.handle(w.branchPush(p, ownerLogin))
		w.handle(w.event(webhook.PullOpened, p))
		w.wantAnswer(w.upload(p, fixtureHealthy, configHash(digest)), 200, "success", "healthy")
		w.finishRun(p)

		w.push(p)
		digest = w.editsPolicy(p)
		w.handle(w.branchPush(p, outsiderLogin))
		w.handle(w.event(webhook.PullSynchronized, p))
		w.wantAnswer(w.upload(p, fixtureHealthy, configHash(digest)), 200, "action_required", "owner_approval_missing")
	})

	t.Run("an owner push of an older head grants nothing to the new head", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.editsPolicy(p)
		w.handle(w.branchPush(p, ownerLogin))
		w.push(p)
		digest := w.editsPolicy(p)
		w.handle(w.event(webhook.PullSynchronized, p))

		w.wantAnswer(w.upload(p, fixtureHealthy, configHash(digest)), 200, "action_required", "owner_approval_missing")
	})

	t.Run("a later push of the same commit by a non-owner takes nothing away", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		digest := w.editsPolicy(p)
		w.handle(w.branchPush(p, ownerLogin))
		cut := w.branchPush(p, outsiderLogin) // a branch cut from the owner's commit
		cut.Branch = "cut-from-feature"
		w.handle(cut)
		w.handle(w.event(webhook.PullOpened, p))

		w.wantAnswer(w.upload(p, fixtureHealthy, configHash(digest)), 200, "success", "healthy")
	})

	t.Run("a head-edited settings file cannot make its pusher an owner", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.missingApproval(7)
		w.setOwners(w.shop, p.head, "mallory")
		w.setFile(w.shop, p.head, "CODEOWNERS", []byte("* @mallory\n"))

		w.handle(w.branchPush(p, "mallory"))
		if len(w.gh.reruns) != 0 {
			t.Errorf("reruns = %v, want none", w.gh.reruns)
		}
	})

	t.Run("a push event for a head the pull request no longer has changes nothing", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.missingApproval(7)
		stale := w.branchPush(p, ownerLogin)
		stale.HeadSHA = w.sha()

		w.handle(stale)
		if len(w.gh.reruns) != 0 {
			t.Errorf("reruns = %v, want none", w.gh.reruns)
		}
	})

	t.Run("a push to a branch without a pull request records and does nothing else", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.missingApproval(7)
		other := w.branchPush(p, ownerLogin)
		other.Branch = "no-pull-here"
		before := w.gh.writes()

		w.handle(other)
		if len(w.gh.reruns) != 0 || w.gh.writes() != before {
			t.Errorf("reruns = %v, writes %d → %d", w.gh.reruns, before, w.gh.writes())
		}
	})
}

func TestPusherTypeOtherThanUserRecordsNothing(t *testing.T) {
	tests := []struct {
		pusherType string
		authorized bool
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
			recorded := err == nil
			reran := slices.Equal(w.gh.reruns, []int64{p.runID})
			if recorded != tt.authorized || reran != tt.authorized {
				t.Errorf("recorded = %v, re-run = %v, want both %v", recorded, reran, tt.authorized)
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
	if w.gh.writes() != before || len(w.gh.reruns) != 0 {
		t.Errorf("writes %d → %d, reruns %v", before, w.gh.writes(), w.gh.reruns)
	}
}
