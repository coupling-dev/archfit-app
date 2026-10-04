package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const editedPolicy = basePolicy + "  b:\n    paths: [\"pkg/b/**\"]\n"

// editsPolicy makes the pull change the policy file; the engine then runs with the head version.
func (w *world) editsPolicy(p *pull, more ...string) (headDigest string) {
	w.changes(p, append([]string{"pkg/a/a.go", policy.PolicyPath}, more...)...)
	w.setFile(p.repo, p.head, policy.PolicyPath, []byte(editedPolicy))
	return w.digestOf(p.repo, p.head, policy.PolicyPath)
}

// currentlyGreen lists the pulls whose gate check is success right now.
func (w *world) currentlyGreen(pulls ...*pull) []int {
	var green []int
	for _, p := range pulls {
		if c, ok := w.check(p, p.head, gateCheckName); ok && c.Conclusion == "success" {
			green = append(green, p.number)
		}
	}
	return green
}

func TestLegitimateChangePassesAndViolationFails(t *testing.T) {
	w := newWorld(t)

	legit := w.openPull(w.shop, 1)
	w.handle(w.event(webhook.PullOpened, legit))
	w.wantAnswer(w.upload(legit, fixtureHealthy), 200, "success", "healthy")
	w.wantGate(legit, runCompleted, "success")
	if c, _ := w.check(legit, legit.head, stateCheckName); c.Conclusion != "success" {
		t.Errorf("%s of the healthy PR = %q", stateCheckName, c.Conclusion)
	}

	violation := w.openPull(w.shop, 2)
	w.handle(w.event(webhook.PullOpened, violation))
	w.wantAnswer(w.upload(violation, fixtureBlocked), 200, "failure", "blocking_findings")
	w.wantGate(violation, runCompleted, "failure")
	text := w.feedbackText(violation)
	for _, want := range []string{"no_direct_b_dependency", "pkg/a/a.go", "pkg/b", "archfit check -c .archfit.yaml", "customer-attested"} {
		if !strings.Contains(text, want) {
			t.Errorf("feedback of the blocked PR does not name %q", want)
		}
	}
	if strings.Contains(text, "verified") {
		t.Error("feedback claims something is verified")
	}

	attention := w.openPull(w.shop, 3)
	w.handle(w.event(webhook.PullOpened, attention))
	w.wantAnswer(w.upload(attention, fixtureAttention), 200, "success", "attention_only")
	w.wantGate(attention, runCompleted, "success")
	if c, _ := w.check(attention, attention.head, stateCheckName); c.Conclusion != "neutral" {
		t.Errorf("%s of the needs_attention PR = %q, want neutral", stateCheckName, c.Conclusion)
	}
}

func TestTamperingNeverYieldsSuccess(t *testing.T) {
	baseline, labels := []byte(`{"violations":[]}`), []byte("labels: {}\n")
	tampered := func(raw []byte) []byte { return append(slices.Clone(raw), " \n"...) }

	// honest is an upload whose claims all match the protected ref; each row tampers with one thing.
	honest := func(w *world, p *pull, muts ...func(map[string]any)) uploadSpec {
		muts = append([]func(map[string]any){gateReference("comparable")}, muts...)
		s := w.spec(p, w.report(w.shop, fixtureHealthy, muts...))
		s.env.BaselineDigest, s.env.LabelsDigest = policy.Digest(baseline), policy.Digest(labels)
		return s
	}

	tests := []struct {
		name   string
		reason string
		spec   func(w *world, p *pull) uploadSpec
	}{
		{"head-edited policy", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			return honest(w, p, configHash(w.editsPolicy(p)))
		}},
		{"policy rewritten during the run", "policy_mismatch", func(w *world, p *pull) uploadSpec {
			return honest(w, p, configHash(policy.Digest([]byte(editedPolicy))))
		}},
		{"head-edited baseline", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			w.changes(p, policy.BaselinePath)
			w.setFile(w.shop, p.head, policy.BaselinePath, tampered(baseline))
			s := honest(w, p)
			s.env.BaselineDigest = policy.Digest(tampered(baseline))
			return s
		}},
		{"baseline swapped during the run", "baseline_mismatch", func(w *world, p *pull) uploadSpec {
			s := honest(w, p)
			s.env.BaselineDigest = policy.Digest(tampered(baseline))
			return s
		}},
		{"envelope claims no baseline while one exists", "baseline_mismatch", func(w *world, p *pull) uploadSpec {
			s := honest(w, p)
			s.env.BaselineDigest = ""
			return s
		}},
		{"gate_reference missing while a baseline exists", "baseline_not_comparable", func(w *world, p *pull) uploadSpec {
			return honest(w, p, noGateReference)
		}},
		{"gate_reference non_comparable while a baseline exists", "baseline_not_comparable", func(w *world, p *pull) uploadSpec {
			return honest(w, p, gateReference("non_comparable", "no baseline file was loaded"))
		}},
		{"head-edited labels file", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			w.changes(p, policy.LabelsPath)
			w.setFile(w.shop, p.head, policy.LabelsPath, tampered(labels))
			s := honest(w, p)
			s.env.LabelsDigest = policy.Digest(tampered(labels))
			return s
		}},
		{"labels swapped during the run", "labels_mismatch", func(w *world, p *pull) uploadSpec {
			s := honest(w, p)
			s.env.LabelsDigest = policy.Digest(tampered(labels))
			return s
		}},
		{"head-edited settings: new owner, advisory mode, self-approved", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			w.changes(p, settings.Path)
			w.setFile(w.shop, p.head, settings.Path, settings.Render(settings.Settings{
				Version: 1, Owners: []string{"mallory"}, Gate: settings.GateAdvisory, Workflow: testWorkflow, DataMode: settings.DataCustomer,
			}))
			w.review(p, "mallory", "APPROVED", p.head)
			return honest(w, p)
		}},
		{"head-edited workflow", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			w.changes(p, testWorkflow)
			return honest(w, p)
		}},
		{"head-edited CODEOWNERS and policy, approved by the new code owner", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			digest := w.editsPolicy(p, ".github/CODEOWNERS")
			w.setFile(w.shop, p.head, ".github/CODEOWNERS", []byte("* @mallory\n"))
			w.review(p, "mallory", "APPROVED", p.head)
			return honest(w, p, configHash(digest))
		}},
		{"an ordinary non-owner approval", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			digest := w.editsPolicy(p)
			w.review(p, "bob", "APPROVED", p.head)
			return honest(w, p, configHash(digest))
		}},
		{"an owner approval followed by a new commit", "owner_approval_missing", func(w *world, p *pull) uploadSpec {
			w.editsPolicy(p)
			w.review(p, ownerLogin, "APPROVED", p.head)
			w.push(p)
			return honest(w, p, configHash(w.editsPolicy(p)))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.setFile(w.shop, testBranch, policy.BaselinePath, baseline)
			w.setFile(w.shop, testBranch, policy.LabelsPath, labels)
			p := w.openPull(w.shop, 7)
			s := tc.spec(w, p)
			w.handle(w.event(webhook.PullOpened, p))

			w.wantAnswer(w.send(s), 200, "action_required", tc.reason)
			w.wantGate(p, runCompleted, "action_required")
			w.noSuccess()
		})
	}

	t.Run("the honest upload of this table passes", func(t *testing.T) {
		w := newWorld(t)
		w.setFile(w.shop, testBranch, policy.BaselinePath, baseline)
		w.setFile(w.shop, testBranch, policy.LabelsPath, labels)
		w.wantAnswer(w.send(honest(w, w.openPull(w.shop, 7))), 200, "success", "healthy")
	})

	t.Run("after the owner approves the new head, the re-run passes", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.editsPolicy(p)
		w.review(p, ownerLogin, "APPROVED", p.head)
		w.push(p)
		digest := w.editsPolicy(p)
		w.handle(w.event(webhook.PullSynchronized, p))
		body := w.report(w.shop, fixtureHealthy, configHash(digest))
		w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")

		w.review(p, ownerLogin, "APPROVED", p.head)
		w.handle(w.event(webhook.ReviewChanged, p))
		if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
			t.Fatalf("reruns = %v", w.gh.reruns)
		}
		w.noSuccess()
		w.wantAnswer(w.send(w.spec(p, body)), 200, "success", "healthy")
		if got := w.receipt(p); got.PolicyDigest != digest || got.RunAttempt != 2 {
			t.Errorf("receipt = %+v, want the approved head digest and attempt 2", got)
		}
	})
}

func TestAtomicCodeAndPolicyPullRequest(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	digest := w.editsPolicy(p)
	w.handle(w.event(webhook.PullOpened, p))
	body := w.report(w.shop, fixtureHealthy, configHash(digest))

	w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")
	if text := w.feedbackText(p); !strings.Contains(text, "policy_mismatch") {
		t.Error("feedback does not also name policy_mismatch")
	}
	if got := w.receipt(p).PolicyDigest; got != w.digestOf(w.shop, testBranch, policy.PolicyPath) {
		t.Errorf("an unapproved head digest reached the receipt: %s", got)
	}

	w.review(p, ownerLogin, "APPROVED", p.head)
	w.handle(w.event(webhook.ReviewChanged, p))
	if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("reruns = %v", w.gh.reruns)
	}
	w.wantGate(p, runCompleted, "action_required") // an approval alone turns nothing green
	w.wantAnswer(w.send(w.spec(p, body)), 200, "success", "healthy")
	w.wantGate(p, runCompleted, "success")
	w.finishRun(p)

	approved := p.head
	w.push(p)
	w.setFile(w.shop, p.head, policy.PolicyPath, []byte(editedPolicy))
	w.handle(w.event(webhook.PullSynchronized, p))
	w.wantGate(p, runInProgress, "")
	w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")
	w.wantGate(p, runCompleted, "action_required")
	if c, _ := w.check(p, approved, gateCheckName); c.Conclusion != "success" {
		t.Errorf("the approved head's own check changed to %q", c.Conclusion)
	}
}

func TestNonComparableBaselineRecovery(t *testing.T) {
	oldBaseline, newBaseline := []byte(`{"profile":"v0"}`), []byte(`{"profile":"v1"}`)
	w := newWorld(t)
	w.setFile(w.shop, testBranch, policy.BaselinePath, oldBaseline)
	p := w.openPull(w.shop, 7)
	w.handle(w.event(webhook.PullOpened, p))

	s := w.spec(p, w.report(w.shop, fixtureHealthy, gateReference("non_comparable", "measurement profile differs")))
	s.env.BaselineDigest = policy.Digest(oldBaseline)
	w.wantAnswer(w.send(s), 200, "action_required", "baseline_not_comparable")
	if text := w.feedbackText(p); !strings.Contains(text, "baseline PR") {
		t.Errorf("feedback does not name the baseline-PR recovery:\n%s", text)
	}

	// A regenerated baseline that is not on the protected ref does not help.
	fresh := w.report(w.shop, fixtureHealthy, gateReference("comparable"))
	s = w.spec(p, fresh)
	s.env.BaselineDigest = policy.Digest(newBaseline)
	w.wantAnswer(w.send(s), 200, "action_required", "baseline_mismatch")
	w.noSuccess()

	// The owner-approved baseline PR merges; the protected push re-runs the workflow.
	w.setFile(w.shop, testBranch, policy.BaselinePath, newBaseline)
	w.handle(w.pushEvent(w.shop, policy.BaselinePath))
	if got := w.receipt(p).Reason; got != "stale_inputs" {
		t.Errorf("receipt reason after the merge = %s, want stale_inputs", got)
	}
	if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("reruns = %v", w.gh.reruns)
	}
	w.noSuccess()
	s = w.spec(p, fresh)
	s.env.BaselineDigest = policy.Digest(newBaseline)
	w.wantAnswer(w.send(s), 200, "success", "healthy")
	w.wantGate(p, runCompleted, "success")
}

func TestDuplicateAndOutOfOrderDelivery(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.event(webhook.PullOpened, p))

	first := w.spec(p, w.report(w.shop, fixtureHealthy))
	w.wantAnswer(w.send(first), 200, "success", "healthy")
	comment := w.comments(p)[0]
	w.wantAnswer(w.send(first), 200, "success", "healthy")
	if got := w.comments(p); len(got) != 1 || got[0] != comment {
		t.Errorf("the duplicate upload changed the comment: %d comments", len(got))
	}
	w.wantGate(p, runCompleted, "success") // wantGate also fails on a second check

	// Attempt 2 reports a violation; then attempt 1 arrives again, late.
	w.editRun(p, func(r *api.WorkflowRun) { r.Attempt = 2 })
	w.wantAnswer(w.upload(p, fixtureBlocked), 200, "failure", "blocking_findings")
	before := w.gh.writes()
	if a := w.send(first); a.status != 409 {
		t.Errorf("older attempt: %d %s, want 409", a.status, a.raw)
	}
	if got := w.receipt(p); got.RunAttempt != 2 || got.Conclusion != "failure" {
		t.Errorf("the newer result does not stand: %+v", got)
	}
	if got := w.comments(p); len(got) != 1 || got[0].ID != comment.ID {
		t.Errorf("%d comments, want the one comment updated in place", len(got))
	}

	// The head moves; an upload for the superseded head is stale.
	superseded := w.spec(p, w.report(w.shop, fixtureHealthy))
	w.push(p)
	if a := w.send(superseded); a.status != 409 {
		t.Errorf("superseded head: %d %s, want 409", a.status, a.raw)
	}
	if w.gh.writes() != before {
		t.Errorf("stale uploads wrote %d times", w.gh.writes()-before)
	}
}

func TestReviewTriggersRerun(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	digest := w.editsPolicy(p)
	w.handle(w.event(webhook.PullOpened, p))
	body := w.report(w.shop, fixtureHealthy, configHash(digest))
	w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")

	w.review(p, ownerLogin, "APPROVED", p.head)
	w.handle(w.event(webhook.ReviewChanged, p))
	if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("reruns = %v, want one", w.gh.reruns)
	}
	w.wantGate(p, runCompleted, "action_required")
	if got := w.receipt(p).Reason; got != "owner_approval_missing" {
		t.Errorf("receipt reason = %s: the check must wait for the new upload", got)
	}

	writes := w.gh.writes()
	w.handle(w.event(webhook.ReviewChanged, p)) // redelivered: the run is in progress
	if len(w.gh.reruns) != 1 || w.gh.writes() != writes {
		t.Errorf("second delivery: reruns = %v, %d new writes", w.gh.reruns, w.gh.writes()-writes)
	}

	w.wantAnswer(w.send(w.spec(p, body)), 200, "success", "healthy")
}

func TestSuspensionAndRestoration(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	vault := w.addRepo("acme/vault", 12, 201, true)
	w.entitle(12, Entitlement{SuspendAfter: w.now.Add(-24 * time.Hour), Reason: "Trial and grace period ended."})
	p := w.openPull(vault, 7)
	w.handle(w.event(webhook.PullOpened, p))

	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "action_required", "subscription_suspended")
	w.wantGate(p, runCompleted, "action_required")
	if sum, err := w.gate.Sweep(ctx); err != nil || sum.Suspended != 1 || sum.Rerun != 0 {
		t.Errorf("sweep while suspended = %+v, %v", sum, err)
	}

	// control/account replaces the record with a grant; only the sweep and a fresh report restore the check.
	w.entitle(12, Entitlement{})
	sum, err := w.gate.Sweep(ctx)
	if err != nil || sum.Rerun != 1 || sum.Suspended != 0 {
		t.Errorf("sweep after restoration = %+v, %v", sum, err)
	}
	w.wantGate(p, runCompleted, "action_required") // the old receipt is never promoted
	w.noSuccess()
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
	w.wantGate(p, runCompleted, "success")

	t.Run("an unreadable entitlement is not non-payment", func(t *testing.T) {
		w := newWorld(t)
		vault := w.addRepo("acme/vault", 12, 201, true)
		w.entitle(12, Entitlement{SuspendAfter: w.now.Add(-24 * time.Hour)})
		w.store.entitlementErr = errors.New("store: connection refused")
		w.wantAnswer(w.upload(w.openPull(vault, 7), fixtureHealthy), 200, "success", "healthy")
		if len(w.store.tenants) == 0 {
			t.Error("the Store was not even asked")
		}
	})
}

func TestGateSurvivesControlPlaneFailure(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t) // nothing of internal/control is wired: the import test proves it cannot be
	// The control plane's records are unreadable; the publication leases still work (see TestStoreOutageFailsClosed).
	w.store.entitlementErr = errors.New("store: down")
	controlDown := errors.New("control: billing provider unreachable")
	// dispatch is what app does with a delivery: the gate first, then the control plane.
	dispatch := func(e webhook.Event) error {
		if err := w.gate.HandleEvent(ctx, e); err != nil {
			return err
		}
		return controlDown
	}

	p := w.openPull(w.shop, 7)
	if err := dispatch(w.event(webhook.PullOpened, p)); !errors.Is(err, controlDown) {
		t.Fatalf("dispatch = %v", err)
	}
	w.wantGate(p, runInProgress, "")
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")

	lost := w.openPull(w.shop, 8)
	sum, err := w.gate.Sweep(ctx)
	if err != nil || sum.Pended != 1 || sum.Errors != 0 {
		t.Errorf("sweep = %+v, %v", sum, err)
	}
	w.wantGate(lost, runInProgress, "")
}

// Without the Store no publication lease can be taken, and an unserialized write could let an older run overwrite a
// newer one. So nothing is written: the upload is retryable, the event is redelivered and the check stays as it was.
func TestStoreOutageFailsClosed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.store.down = errors.New("store: unreachable")
	p := w.openPull(w.shop, 7)

	if err := w.gate.HandleEvent(ctx, w.event(webhook.PullOpened, p)); err == nil {
		t.Error("HandleEvent created the pending check without a lease")
	}
	if a := w.upload(p, fixtureHealthy); a.status != 503 || a.Error != "unavailable" || a.Conclusion != "" {
		t.Errorf("upload answered %d %s, want 503 unavailable", a.status, a.raw)
	}
	if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors == 0 {
		t.Errorf("sweep = %+v, %v, want errors counted", sum, err)
	}
	if n := w.gh.writes(); n != 0 {
		t.Errorf("%d check or comment writes without a lease, want 0", n)
	}
	w.noSuccess()

	w.store.down = nil
	w.handle(w.event(webhook.PullOpened, p))
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
	w.wantGate(p, runCompleted, "success")
}

func TestBasePolicyChangeReachesOpenPullsAtOnce(t *testing.T) {
	ctx := context.Background()
	triggers := map[string]func(w *world){
		"the DefaultBranchPush event arrives": func(w *world) { w.handle(w.pushEvent(w.shop, policy.PolicyPath)) },
		"the event is lost and the hourly sweep runs": func(w *world) {
			w.now = w.now.Add(time.Hour)
			if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 0 {
				w.t.Errorf("sweep = %+v, %v", sum, err)
			}
		},
	}
	for name, trigger := range triggers {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			pulls := []*pull{w.openPull(w.shop, 1), w.openPull(w.shop, 2), w.openPull(w.shop, 3)}
			for _, p := range pulls {
				w.handle(w.event(webhook.PullOpened, p))
				w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
			}
			w.setFile(w.shop, testBranch, policy.PolicyPath, []byte(editedPolicy)) // the policy PR merges

			trigger(w)
			for _, p := range pulls {
				w.wantGate(p, runCompleted, "action_required")
				got := w.receipt(p)
				if got.Reason != "stale_inputs" || got.PolicyDigest != policy.Digest([]byte(editedPolicy)) {
					t.Errorf("PR %d receipt = %+v", p.number, got)
				}
			}
			wantReruns := []int64{pulls[0].runID, pulls[1].runID, pulls[2].runID}
			if !slices.Equal(w.gh.reruns, wantReruns) {
				t.Errorf("reruns = %v, want %v", w.gh.reruns, wantReruns)
			}

			writes := w.gh.writes()
			trigger(w)
			if !slices.Equal(w.gh.reruns, wantReruns) || w.gh.writes() != writes {
				t.Errorf("a later pass added: reruns %v, %d writes", w.gh.reruns, w.gh.writes()-writes)
			}
			if green := w.currentlyGreen(pulls...); len(green) != 0 {
				t.Errorf("PRs %v are green without a fresh report", green)
			}

			for _, p := range pulls {
				w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
			}
		})
	}
}

func TestSweepThroughFacade(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	web := w.addRepo("acme/web", shopInstallation, 102, false)
	w.setFile(w.shop, testBranch, policy.PolicyPath, []byte(basePolicy+
		"waivers:\n  - rule: no_direct_b_dependency\n    reason: legacy import\n    approved_by: olivia\n    expires: 2026-09-25\n"))

	lostWebhook := w.openPull(w.shop, 1) // PullOpened never arrived
	noUpload := w.openPull(w.shop, 2)
	w.now = testStart.Add(-61 * time.Minute)
	w.handle(w.event(webhook.PullOpened, noUpload))
	w.now = testStart
	waived := w.openPull(w.shop, 4)
	w.wantAnswer(w.upload(waived, fixtureHealthy), 200, "success", "healthy")
	staleBase := w.openPull(web, 3)
	w.wantAnswer(w.upload(staleBase, fixtureHealthy), 200, "success", "healthy")
	w.setFile(web, testBranch, policy.PolicyPath, []byte(editedPolicy))
	all := []*pull{lostWebhook, noUpload, waived, staleBase}

	sum, err := w.gate.Sweep(ctx)
	want := Summary{Repos: 2, Pulls: 4, Pended: 1, TimedOut: 1, Rerun: 1, Warned: 1}
	if err != nil || sum != want {
		t.Errorf("Sweep = %+v, %v; want %+v", sum, err, want)
	}
	w.wantGate(lostWebhook, runInProgress, "")
	w.wantGate(noUpload, runCompleted, "action_required")
	if got := w.receipt(noUpload).Reason; got != "missing_upload" {
		t.Errorf("no-upload receipt reason = %s", got)
	}
	w.wantGate(staleBase, runCompleted, "action_required")
	if got := w.receipt(staleBase).Reason; got != "stale_inputs" {
		t.Errorf("stale-base receipt reason = %s", got)
	}
	if !slices.Equal(w.gh.reruns, []int64{staleBase.runID}) {
		t.Errorf("reruns = %v", w.gh.reruns)
	}
	if !strings.Contains(w.comments(waived)[0].Body, "2026-09-25") {
		t.Error("no waiver-expiry notice in the comment")
	}
	if green := w.currentlyGreen(all...); !slices.Equal(green, []int{waived.number}) {
		t.Errorf("green PRs = %v, want only the one with a fresh report", green)
	}

	// The second pass only adds the waiver notice to the PR the first pass timed out (its rule ended that
	// evaluation before the notice); from then on a pass changes nothing.
	checks, reruns := len(w.gh.checkWrites), len(w.gh.reruns)
	sum, err = w.gate.Sweep(ctx)
	if want := (Summary{Repos: 2, Pulls: 4, Warned: 2}); err != nil || sum != want {
		t.Errorf("second Sweep = %+v, %v; want %+v", sum, err, want)
	}
	writes := w.gh.writes()
	sum, err = w.gate.Sweep(ctx)
	if want := (Summary{Repos: 2, Pulls: 4, Warned: 2}); err != nil || sum != want || w.gh.writes() != writes {
		t.Errorf("third Sweep = %+v, %v, %d new writes; want %+v and none", sum, err, w.gh.writes()-writes, want)
	}
	if len(w.gh.checkWrites) != checks || len(w.gh.reruns) != reruns {
		t.Errorf("later passes wrote %d checks and requested %d re-runs", len(w.gh.checkWrites)-checks, len(w.gh.reruns)-reruns)
	}

	// The waiver expires: the green check asks for action and the workflow re-runs.
	w.now = time.Date(2026, 9, 26, 0, 0, 1, 0, time.UTC)
	if _, err := w.gate.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	w.wantGate(waived, runCompleted, "action_required")
	if got := w.receipt(waived).Reason; got != "waiver_expired" {
		t.Errorf("waived receipt reason = %s", got)
	}
	if !slices.Contains(w.gh.reruns, waived.runID) {
		t.Errorf("reruns = %v, want the waived PR's run", w.gh.reruns)
	}
	if green := w.currentlyGreen(all...); len(green) != 0 {
		t.Errorf("green PRs after the expiry = %v", green)
	}
}
