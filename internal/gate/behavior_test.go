package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
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

// An owner approval that a green receipt relied on can end without a new commit: the review is dismissed, or the owner
// requests changes. Settings and workflow edits move no digest, so only the approval itself can show the change.
// A report-derived action_required (missing required coverage) shows the report on the PR comment and the Pull page,
// so the owner approval that admitted the head's inputs matters to it as to a success: when it is gone the result
// becomes owner_approval_missing, on the event as on the hourly sweep, and the report is withheld.
func TestWithdrawnApprovalEndsReportDerivedActionRequired(t *testing.T) {
	ctx := context.Background()
	triggers := map[string]func(w *world, p *pull){
		"the ReviewChanged event": func(w *world, p *pull) { w.handle(w.event(webhook.ReviewChanged, p)) },
		"the event is lost and the hourly sweep runs": func(w *world, _ *pull) {
			w.now = w.now.Add(time.Hour)
			if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 0 {
				w.t.Errorf("sweep = %+v, %v", sum, err)
			}
		},
	}
	for name, trigger := range triggers {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.changes(p, "pkg/a/a.go", settings.Path) // a protected path with no digest of its own
			w.handle(w.event(webhook.PullOpened, p))
			w.review(p, ownerLogin, "APPROVED", p.head)
			w.wantAnswer(w.upload(p, fixtureCoverage), 200, "action_required", "missing_required_coverage")

			key := pullKey(p.repo.FullName, p.number)
			for i, rv := range w.gh.reviews[key] {
				if rv.Reviewer == ownerLogin && rv.State == "APPROVED" {
					w.gh.reviews[key][i].State = "DISMISSED"
				}
			}
			trigger(w, p)

			if rc := w.receipt(p); rc.Reason != verdict.ReasonOwnerApprovalMissing {
				t.Errorf("receipt reason = %q, want owner_approval_missing: the report was decided under an approval that is gone", rc.Reason)
			}
			w.wantGate(p, runCompleted, "action_required")
			writes := w.gh.writes()
			trigger(w, p)
			if w.gh.writes() != writes || len(w.gh.reruns) != 0 {
				t.Errorf("a repeated trigger added %d writes and reruns %v", w.gh.writes()-writes, w.gh.reruns)
			}
		})
	}
}

func TestWithdrawnApprovalEndsSuccess(t *testing.T) {
	ctx := context.Background()
	edits := map[string]func(w *world, p *pull) []func(map[string]any){
		"policy": func(w *world, p *pull) []func(map[string]any) {
			return []func(map[string]any){configHash(w.editsPolicy(p))}
		},
		"settings": func(w *world, p *pull) []func(map[string]any) {
			w.changes(p, "pkg/a/a.go", settings.Path)
			return nil
		},
		"workflow": func(w *world, p *pull) []func(map[string]any) {
			w.changes(p, "pkg/a/a.go", testWorkflow)
			return nil
		},
	}
	withdrawals := map[string]func(w *world, p *pull){
		"dismissed": func(w *world, p *pull) { // GitHub changes the state of the review itself
			key := pullKey(p.repo.FullName, p.number)
			for i, rv := range w.gh.reviews[key] {
				if rv.Reviewer == ownerLogin && rv.State == "APPROVED" {
					w.gh.reviews[key][i].State = "DISMISSED"
				}
			}
		},
		"changes requested": func(w *world, p *pull) { w.review(p, ownerLogin, "CHANGES_REQUESTED", p.head) },
	}
	triggers := map[string]func(w *world, p *pull){
		"the ReviewChanged event": func(w *world, p *pull) { w.handle(w.event(webhook.ReviewChanged, p)) },
		"the event is lost and the hourly sweep runs": func(w *world, _ *pull) {
			w.now = w.now.Add(time.Hour)
			if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 0 {
				w.t.Errorf("sweep = %+v, %v", sum, err)
			}
		},
	}
	// approved leaves a green receipt that holds only because the owner approved the head.
	approved := func(t *testing.T, edit func(*world, *pull) []func(map[string]any)) (*world, *pull, []func(map[string]any)) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		muts := edit(w, p)
		w.handle(w.event(webhook.PullOpened, p))
		w.review(p, ownerLogin, "APPROVED", p.head)
		w.wantAnswer(w.upload(p, fixtureHealthy, muts...), 200, "success", "healthy")
		return w, p, muts
	}

	for editName, edit := range edits {
		for withdrawName, withdraw := range withdrawals {
			for triggerName, trigger := range triggers {
				t.Run(editName+"/"+withdrawName+"/"+triggerName, func(t *testing.T) {
					w, p, muts := approved(t, edit)
					withdraw(w, p)
					trigger(w, p)

					w.wantGate(p, runCompleted, "action_required")
					if rc := w.receipt(p); rc.Reason != verdict.ReasonOwnerApprovalMissing || rc.RunID != p.runID {
						t.Errorf("receipt = %+v, want owner_approval_missing under run %d", rc, p.runID)
					}
					if c, _ := w.check(p, p.head, stateCheckName); c.Conclusion != "action_required" {
						t.Errorf("%s = %q, want action_required", stateCheckName, c.Conclusion)
					}
					if len(w.gh.reruns) != 0 {
						t.Errorf("reruns = %v: no run can be green until an owner approves again", w.gh.reruns)
					}

					writes := w.gh.writes()
					trigger(w, p)
					if w.gh.writes() != writes || len(w.gh.reruns) != 0 {
						t.Errorf("a repeated trigger added %d writes and reruns %v", w.gh.writes()-writes, w.gh.reruns)
					}

					w.review(p, ownerLogin, "APPROVED", p.head)
					w.handle(w.event(webhook.ReviewChanged, p))
					if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
						t.Fatalf("reruns after the new approval = %v, want [%d]", w.gh.reruns, p.runID)
					}
					w.wantGate(p, runCompleted, "action_required")
					w.wantAnswer(w.upload(p, fixtureHealthy, muts...), 200, "success", "healthy")
				})
			}
		}
	}

	t.Run("an approval that still holds keeps the check green", func(t *testing.T) {
		for editName, edit := range edits {
			w, p, _ := approved(t, edit)
			w.review(p, "bob", "CHANGES_REQUESTED", p.head) // not an owner
			w.review(p, ownerLogin, "COMMENTED", p.head)    // a comment withdraws nothing
			writes := w.gh.writes()
			for _, trigger := range triggers {
				trigger(w, p)
			}
			if c, _ := w.check(p, p.head, gateCheckName); c.Conclusion != "success" || w.gh.writes() != writes || len(w.gh.reruns) != 0 {
				t.Errorf("%s: gate = %q, %d writes, reruns %v", editName, c.Conclusion, w.gh.writes()-writes, w.gh.reruns)
			}
		}
	})
}

// approvedSettingsEdit is a pull that edits the settings file, approved by the owner, with a green gate check. The
// digests do not show the edit: only the owner approval keeps the check green.
func (w *world) approvedSettingsEdit() (*pull, uploadSpec) {
	w.t.Helper()
	p := w.openPull(w.shop, 7)
	w.changes(p, "pkg/a/a.go", settings.Path)
	w.handle(w.event(webhook.PullOpened, p))
	w.review(p, ownerLogin, "APPROVED", p.head)
	s := w.spec(p, w.report(w.shop, fixtureHealthy))
	w.wantAnswer(w.send(s), 200, "success", "healthy")
	return p, s
}

// successWrites counts the gate check writes that concluded success.
func (w *world) successWrites() int {
	n := 0
	for _, c := range w.gh.checkWrites {
		if c.Name == gateCheckName && c.Conclusion == "success" {
			n++
		}
	}
	return n
}

// The publication lease orders a withdrawal and an upload of the same head; whichever waits reads its inputs again
// inside the lease and writes nothing when they moved.
func TestWithdrawalRacesWithPublication(t *testing.T) {
	ctx := context.Background()

	t.Run("an upload decided under the approval waits while the withdrawal is published", func(t *testing.T) {
		w := newWorld(t)
		p, retry := w.approvedSettingsEdit()
		greens := w.successWrites()
		w.store.beforeLease = func() { // the upload has decided success; the owner dismisses before it holds the lease
			w.review(p, ownerLogin, "CHANGES_REQUESTED", p.head)
			w.handle(w.event(webhook.ReviewChanged, p))
		}

		a := w.send(retry)

		if a.status != 503 || a.Error != "unavailable" {
			t.Errorf("upload answered %d %s, want 503 unavailable: its decision is stale", a.status, a.raw)
		}
		w.wantGate(p, runCompleted, "action_required")
		if got := w.receipt(p).Reason; got != verdict.ReasonOwnerApprovalMissing {
			t.Errorf("receipt reason = %s, want owner_approval_missing", got)
		}
		if n := w.successWrites(); n != greens {
			t.Errorf("%d success writes after the withdrawal", n-greens)
		}
		w.wantAnswer(w.send(retry), 200, "action_required", "owner_approval_missing") // the Action's retry
	})

	triggers := map[string]func(t *testing.T, w *world, p *pull){
		"the ReviewChanged event": func(t *testing.T, w *world, p *pull) {
			if err := w.gate.HandleEvent(ctx, w.event(webhook.ReviewChanged, p)); err != nil {
				t.Errorf("HandleEvent = %v", err)
			}
		},
		"the sweep": func(t *testing.T, w *world, _ *pull) {
			w.now = w.now.Add(time.Hour)
			if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 0 {
				t.Errorf("sweep = %+v, %v", sum, err)
			}
		},
	}
	for name, trigger := range triggers {
		t.Run("a withdrawal that waits while the owner approves again writes nothing: "+name, func(t *testing.T) {
			w := newWorld(t)
			p, _ := w.approvedSettingsEdit()
			w.review(p, ownerLogin, "DISMISSED", p.head)
			w.store.beforeLease = func() { w.review(p, ownerLogin, "APPROVED", p.head) }
			writes := w.gh.writes()

			trigger(t, w, p)

			w.wantGate(p, runCompleted, "success") // the owner approved this head again: the green check holds
			if w.gh.writes() != writes {
				t.Errorf("%d writes from a withdrawal decided on replaced inputs", w.gh.writes()-writes)
			}
		})
	}

	t.Run("a newer attempt that turns green while the event's withdrawal waits is withdrawn too", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.approvedSettingsEdit()
		w.editRun(p, func(r *api.WorkflowRun) { r.Attempt = 2 }) // the workflow was re-run
		retry := w.spec(p, w.report(w.shop, fixtureHealthy))
		var withdrawal <-chan error
		w.gh.beforeCheckWrite = func() { // attempt 2 read the approval inside the lease; the owner then withdraws it
			w.review(p, ownerLogin, "CHANGES_REQUESTED", p.head)
			withdrawal = w.eventAtLease(w.event(webhook.ReviewChanged, p))
		}

		w.wantAnswer(w.send(retry), 200, "success", "healthy")

		if err := <-withdrawal; err != nil {
			t.Fatalf("HandleEvent = %v", err)
		}
		w.wantGate(p, runCompleted, "action_required")
		if rc := w.receipt(p); rc.Reason != verdict.ReasonOwnerApprovalMissing || rc.RunAttempt != 2 {
			t.Errorf("receipt = %+v, want owner_approval_missing over attempt 2", rc)
		}
	})

	t.Run("a newer attempt that turns green while the sweep's withdrawal waits is withdrawn too", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.approvedSettingsEdit()
		w.review(p, ownerLogin, "CHANGES_REQUESTED", p.head)
		w.editRun(p, func(r *api.WorkflowRun) { r.Attempt = 2 })
		retry := w.spec(p, w.report(w.shop, fixtureHealthy))
		w.store.beforeLease = func() { // the sweep found attempt 1 withdrawn; attempt 2 passes before it holds the lease
			w.review(p, ownerLogin, "APPROVED", p.head)
			w.wantAnswer(w.send(retry), 200, "success", "healthy")
			w.review(p, ownerLogin, "CHANGES_REQUESTED", p.head)
		}

		triggers["the sweep"](t, w, p)

		w.wantGate(p, runCompleted, "action_required")
		if rc := w.receipt(p); rc.Reason != verdict.ReasonOwnerApprovalMissing || rc.RunAttempt != 2 {
			t.Errorf("receipt = %+v, want owner_approval_missing over attempt 2", rc)
		}
	})

	t.Run("an approval that lands while the withdrawal writes re-runs the workflow", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.approvedSettingsEdit()
		w.review(p, ownerLogin, "CHANGES_REQUESTED", p.head)
		var approval <-chan error
		w.gh.beforeCheckWrite = func() { // the withdrawal read the inputs inside the lease; the owner approves again
			w.review(p, ownerLogin, "APPROVED", p.head)
			approval = w.eventAtLease(w.event(webhook.ReviewChanged, p))
		}

		w.handle(w.event(webhook.ReviewChanged, p))

		if err := <-approval; err != nil {
			t.Fatalf("HandleEvent of the approval = %v", err)
		}
		if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
			t.Fatalf("reruns = %v, want [%d]: the new approval was lost behind the withdrawal", w.gh.reruns, p.runID)
		}
		w.wantGate(p, runCompleted, "action_required") // until the re-run uploads
		w.wantAnswer(w.send(w.spec(p, w.report(w.shop, fixtureHealthy))), 200, "success", "healthy")
	})
}

// eventAtLease handles e in another goroutine and returns once that handler waits for the publication lease, or has
// finished without asking for it. The channel yields the HandleEvent error.
func (w *world) eventAtLease(e webhook.Event) <-chan error {
	atLease, done := make(chan struct{}), make(chan error, 1)
	w.store.mu.Lock()
	w.store.beforeLease = func() { close(atLease) }
	w.store.mu.Unlock()
	go func() { done <- w.gate.HandleEvent(context.Background(), e) }()
	select {
	case <-atLease:
	case err := <-done:
		w.store.mu.Lock()
		w.store.beforeLease = nil
		w.store.mu.Unlock()
		done <- err
	}
	return done
}

// A receipt that asked for an owner approval is re-run once no approval is needed any more, as after an approval: a
// base retarget or a repointed settings workflow can drop the protected path from the pull request.
func TestApprovalNoLongerNeededReruns(t *testing.T) {
	ctx := context.Background()
	triggers := map[string]func(w *world, p *pull){
		"the ReviewChanged event": func(w *world, p *pull) {
			w.review(p, "bob", "COMMENTED", p.head)
			w.handle(w.event(webhook.ReviewChanged, p))
		},
		"the sweep": func(w *world, _ *pull) {
			w.now = w.now.Add(time.Hour)
			if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 0 {
				w.t.Errorf("sweep = %+v, %v", sum, err)
			}
		},
	}
	for name, trigger := range triggers {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.changes(p, "pkg/a/a.go", testWorkflow)
			w.handle(w.event(webhook.PullOpened, p))
			body := w.report(w.shop, fixtureHealthy)
			w.wantAnswer(w.send(w.spec(p, body)), 200, "action_required", "owner_approval_missing")
			w.changes(p, "pkg/a/a.go") // the pull request no longer changes a protected path

			trigger(w, p)

			if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
				t.Fatalf("reruns = %v, want [%d]", w.gh.reruns, p.runID)
			}
			w.wantGate(p, runCompleted, "action_required")
			w.wantAnswer(w.send(w.spec(p, body)), 200, "success", "healthy")
		})
	}
}

// Without the Store the withdrawal cannot take the lease, so it cannot write: the green check stays as it is, no new
// success is published, and the next trigger after the Store is back downgrades it.
func TestWithdrawalDuringStoreOutage(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p, retry := w.approvedSettingsEdit()
	w.review(p, ownerLogin, "DISMISSED", p.head)
	w.store.down = errors.New("store: unreachable")
	writes, greens := w.gh.writes(), w.successWrites()

	if err := w.gate.HandleEvent(ctx, w.event(webhook.ReviewChanged, p)); err == nil {
		t.Error("HandleEvent reported success without a lease")
	}
	if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 1 {
		t.Errorf("sweep = %+v, %v, want the pull counted as an error", sum, err)
	}
	if a := w.send(retry); a.status != 503 || a.Error != "unavailable" {
		t.Errorf("upload answered %d %s, want 503 unavailable", a.status, a.raw)
	}
	if w.gh.writes() != writes || w.successWrites() != greens {
		t.Errorf("%d writes, %d of them success, without a lease", w.gh.writes()-writes, w.successWrites()-greens)
	}

	w.store.down = nil
	w.now = w.now.Add(time.Hour)
	if sum, err := w.gate.Sweep(ctx); err != nil || sum.Errors != 0 {
		t.Errorf("sweep after the outage = %+v, %v", sum, err)
	}
	w.wantGate(p, runCompleted, "action_required")
	if got := w.receipt(p).Reason; got != verdict.ReasonOwnerApprovalMissing {
		t.Errorf("receipt reason = %s, want owner_approval_missing", got)
	}
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

// An installation no operator approved gets nothing: no check on a new head, and its upload is refused.
func TestUnadmittedInstallationGetsNothing(t *testing.T) {
	w := newWorld(t)
	w.admit.refused[shopInstallation] = true
	p := w.openPull(w.shop, 1)
	w.handle(w.event(webhook.PullOpened, p))
	if _, found := w.check(p, p.head, gateCheckName); found {
		t.Error("a pending check was published for an unapproved installation")
	}
	if a := w.upload(p, fixtureHealthy); a.status != 403 || a.Error != "installation_not_approved" {
		t.Errorf("upload answered %d %s; want 403 installation_not_approved", a.status, a.raw)
	}
	if _, found := w.check(p, p.head, stateCheckName); found {
		t.Error("a state check was published for an unapproved installation")
	}

	delete(w.admit.refused, shopInstallation)
	w.handle(w.event(webhook.PullSynchronized, p))
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
}

func TestUnreadableApprovalIsRetriedNotDropped(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 1)
	w.admit.down = true
	if err := w.gate.HandleEvent(context.Background(), w.event(webhook.PullOpened, p)); err == nil {
		t.Fatal("HandleEvent = nil; want an error, so the delivery is retried")
	}
	if _, found := w.check(p, p.head, gateCheckName); found {
		t.Error("a check was published while the approval could not be read")
	}
	if a := w.upload(p, fixtureHealthy); a.status != 503 || a.Error != "admission_unavailable" {
		t.Errorf("upload answered %d %s; want 503 admission_unavailable", a.status, a.raw)
	}

	w.admit.down = false
	w.handle(w.event(webhook.PullOpened, p))
	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
}

// onboardedBase gives branch the settings and policy of the default branch, as an author who controls it could, so
// that a pull request into it would be decided if the App looked there.
func (w *world) onboardedBase(r api.Repo, branch string) {
	for _, path := range []string{settings.Path, policy.PolicyPath} {
		w.setFile(r, branch, path, w.gh.files[fileKey(r.FullName, testBranch, path)])
	}
}

// A pull request into a branch other than the default branch is not decided against (its upload ends
// unsupported_event), so the App reads and writes nothing for its events, and no success can come of them.
func TestEventsForAPullIntoAnotherBaseDoNothing(t *testing.T) {
	for _, kind := range []webhook.EventKind{webhook.PullOpened, webhook.PullSynchronized} {
		t.Run(string(kind), func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.onboardedBase(p.repo, "release")
			key := pullKey(p.repo.FullName, p.number)
			pl := w.gh.pulls[key]
			pl.BaseRef = "release"
			w.gh.pulls[key] = pl

			w.handle(w.event(kind, p))

			if writes := w.gh.writes(); writes != 0 {
				t.Errorf("%d writes for a pull into another base", writes)
			}
			if _, found := w.check(p, p.head, gateCheckName); found {
				t.Error("a gate check was created for a pull into another base")
			}
		})
	}

}

// The repository sweep that a push to a protected path on the default branch starts decides only the pull requests
// into that branch. A pull into another base would be resolved at a branch its author controls, found stale, and
// re-run forever; a pull into the default branch is still swept and re-run by the same push.
func TestPushSweepLeavesAPullIntoAnotherBaseAlone(t *testing.T) {
	w := newWorld(t)
	other, onMain := w.openPull(w.shop, 7), w.openPull(w.shop, 8)
	for _, p := range []*pull{other, onMain} {
		w.handle(w.event(webhook.PullOpened, p))
		w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
	}

	// The author retargets one pull at a branch whose policy differs from main's.
	w.onboardedBase(other.repo, "release")
	w.setFile(other.repo, "release", policy.PolicyPath, []byte(basePolicy+"# loosened on release\n"))
	key := pullKey(other.repo.FullName, other.number)
	pl := w.gh.pulls[key]
	pl.BaseRef = "release"
	w.gh.pulls[key] = pl
	// The push changes main's policy: every receipt computed from the old one is stale.
	w.setFile(onMain.repo, testBranch, policy.PolicyPath, []byte(basePolicy+"# changed on main\n"))

	w.handle(w.pushEvent(w.shop, policy.PolicyPath))

	if !slices.Equal(w.gh.reruns, []int64{onMain.runID}) {
		t.Errorf("re-runs = %v, want only the pull into the default branch [%d]", w.gh.reruns, onMain.runID)
	}
}

// identityOf states the engine's result identity (v3.3.0 and later) in the report.
func identityOf(tree, policyDigest, engine string) func(map[string]any) {
	return func(doc map[string]any) {
		doc["identity"] = map[string]any{
			"schema_version": "archfit.result-identity.v1", "policy_digest": policyDigest, "head_tree_digest": tree,
			"base_sha": strings.Repeat("b", 40), "engine_version": engine, "measurement_profile_hash": strings.Repeat("f", 64),
		}
	}
}

// A1: a re-run whose report states the same result identity, on the same head and with the same App outcome, writes
// no check run and edits no comment. A different identity (another tree, policy or engine) publishes as before, and a
// report without an identity is always published.
func TestRepeatedIdentityPublishesNothing(t *testing.T) {
	tree, policyDigest := strings.Repeat("e", 40), strings.Repeat("a", 64)
	same := identityOf(tree, policyDigest, "v3.3.0")
	rerun := func(w *world, p *pull) {
		run := w.gh.runs[p.runID]
		run.Attempt++
		w.gh.runs[p.runID] = run
	}
	tests := []struct {
		name   string
		second func(map[string]any)
		writes bool
	}{
		{"the same identity", same, false},
		{"another head tree", identityOf(strings.Repeat("9", 40), policyDigest, "v3.3.0"), true},
		{"another policy", identityOf(tree, strings.Repeat("7", 64), "v3.3.0"), true},
		{"another engine", identityOf(tree, policyDigest, "v3.4.0"), true},
		{"no identity", func(doc map[string]any) { delete(doc, "identity") }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, p))
			w.wantAnswer(w.upload(p, fixtureBlocked, same), 200, "failure", "blocking_findings")
			before, checks := w.gh.writes(), len(w.gh.checkWrites)

			rerun(w, p)
			w.wantAnswer(w.upload(p, fixtureBlocked, tt.second), 200, "failure", "blocking_findings")

			if wrote := w.gh.writes() != before; wrote != tt.writes {
				t.Errorf("wrote = %v (%d writes, %d check writes), want %v", wrote, w.gh.writes()-before, len(w.gh.checkWrites)-checks, tt.writes)
			}
			w.wantGate(p, runCompleted, "failure")
		})
	}

	// The App's own inputs can change under one engine identity: a baseline that is now trusted names another digest in
	// the receipt, so the check that shows it is rewritten.
	t.Run("a changed trusted input publishes although the identity is the same", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, p))
		w.wantAnswer(w.upload(p, fixtureBlocked, same), 200, "failure", "blocking_findings")
		before := w.gh.writes()

		w.setFile(w.shop, testBranch, policy.LabelsPath, []byte("labels: {}\n"))
		rerun(w, p)
		s := w.spec(p, w.report(w.shop, fixtureBlocked, same))
		s.env.LabelsDigest = w.digestOf(w.shop, testBranch, policy.LabelsPath)
		w.wantAnswer(w.send(s), 200, "failure", "blocking_findings")

		if w.gh.writes() == before {
			t.Error("a receipt with another labels digest wrote nothing")
		}
	})

	t.Run("a changed App outcome publishes although the identity is the same", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, p))
		w.wantAnswer(w.upload(p, fixtureBlocked, same), 200, "failure", "blocking_findings")
		before := w.gh.writes()

		rerun(w, p)
		w.wantAnswer(w.upload(p, fixtureHealthy, same), 200, "success", "healthy")

		if w.gh.writes() == before {
			t.Error("a new outcome under the same identity wrote nothing")
		}
		w.wantGate(p, runCompleted, "success")
	})
}
