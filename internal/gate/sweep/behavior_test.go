package sweep

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestMissedWebhookAndMissingUpload(t *testing.T) {
	workflows := []struct{ name, status, conclusion string }{
		{"queued", "queued", ""},
		{"failed", "completed", "failure"},
		{"cancelled", "completed", "cancelled"},
		{"timed out", "completed", "timed_out"},
	}
	for _, wf := range workflows {
		t.Run(wf.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tg := w.pull(r, 1, trusted()) // the PR-opened webhook was lost: no check
			w.actions.runs[9] = &api.WorkflowRun{ID: 9, Attempt: 1, Status: wf.status, Conclusion: wf.conclusion}

			steps := []struct {
				after       time.Duration
				wantPending bool
			}{{0, true}, {59 * time.Minute, true}, {60 * time.Minute, false}, {2 * time.Hour, false}}
			for _, step := range steps {
				w.pass(t0.Add(step.after))
				st := w.state(tg)
				if !st.Exists || st.Pending != step.wantPending {
					t.Fatalf("after %v: state = %+v, want pending %v", step.after, st, step.wantPending)
				}
				if !st.Pending && (st.Receipt.Conclusion != verdict.ActionRequired || st.Receipt.Reason != verdict.ReasonMissingUpload) {
					t.Errorf("after %v: receipt = %+v, want action_required missing_upload", step.after, st.Receipt)
				}
			}
			if got := w.calls(); got != (calls{published: 1, pendings: 1}) {
				t.Errorf("calls = %+v, want one pending check and one publication", got)
			}
			if len(w.actions.runCalls) != 0 {
				t.Errorf("the workflow run was read %d times; the sweep does not depend on it here", len(w.actions.runCalls))
			}
		})
	}
}

func TestForksStayBlocked(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	fresh := w.pullWith(r, api.Pull{Number: 1, HeadRepo: "stranger/shop", BaseRef: "main"}, trusted())
	// The blocked fork's receipt predates the current policy: it must not be treated as stale.
	blocked := w.pullWith(r, api.Pull{Number: 2, HeadRepo: "stranger/shop", BaseRef: "main"}, trusted())
	rc := receipt(0, verdict.ReasonForkUnsupported)
	rc.PolicyDigest = "old"
	w.check(blocked, rc, t0.Add(-day))

	for i := range 2 {
		w.pass(t0.Add(time.Duration(i) * time.Hour))
	}

	if got := w.reasons(); !reflect.DeepEqual(got, map[int][]verdict.ReasonCode{1: {verdict.ReasonForkUnsupported}}) {
		t.Errorf("published = %v, want fork_unsupported once for PR 1", got)
	}
	if !w.state(blocked).StartedAt.Equal(t0.Add(-day)) || !w.state(fresh).StartedAt.Equal(t0) {
		t.Errorf("checks written at %v and %v", w.state(blocked).StartedAt, w.state(fresh).StartedAt)
	}
	if len(w.actions.runCalls)+len(w.actions.rerunCalls) != 0 {
		t.Errorf("fork pulls reached Actions: %v %v", w.actions.runCalls, w.actions.rerunCalls)
	}
}

func TestBasePolicyChangeRerunsOpenPulls(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	var targets []inputs.Target
	for n := 1; n <= 3; n++ {
		tg := w.pull(r, n, trusted())
		w.check(tg, receipt(int64(100+n), verdict.ReasonHealthy), t0.Add(-day))
		targets = append(targets, tg)
	}
	if got := w.pass(t0); got.Rerun != 0 || w.calls() != (calls{}) {
		t.Fatalf("pass before the change: summary %+v, calls %+v", got, w.calls())
	}

	changed := trusted()
	changed.PolicyDigests = []string{"p2"}
	for _, tg := range targets {
		w.trust(tg, changed)
	}
	sum := w.pass(t0.Add(time.Hour))

	if sum.Rerun != 3 {
		t.Errorf("Summary.Rerun = %d, want 3", sum.Rerun)
	}
	for _, tg := range targets {
		rc := w.state(tg).Receipt
		if rc.Reason != verdict.ReasonStaleInputs || rc.PolicyDigest != "p2" || rc.RunID != int64(100+tg.PullRequest) {
			t.Errorf("PR %d receipt = %+v, want stale_inputs at p2 with its run identity", tg.PullRequest, rc)
		}
	}
	if !slices.Equal(w.actions.rerunCalls, []int64{101, 102, 103}) {
		t.Errorf("re-runs = %v, want one per pull", w.actions.rerunCalls)
	}

	before := w.calls()
	if sum := w.pass(t0.Add(2 * time.Hour)); w.calls() != before || sum.Rerun != 0 {
		t.Errorf("next pass: summary %+v, calls %+v, want nothing more", sum, w.calls())
	}
}

func TestExpiryBlocksUnrelatedPullsWithoutNewCommit(t *testing.T) {
	e := t0.Add(30 * day)
	next := e.Add(60 * day) // another waiver, still valid after e
	start := func(t *testing.T) (*world, inputs.Target) {
		w := newWorld(t)
		r := w.repo(1, 11, "acme/shop")
		tr := trusted()
		tr.EarliestWaiverExpiry = e
		tg := w.pull(r, 1, tr)
		rc := receipt(9, verdict.ReasonHealthy)
		rc.EarliestWaiverExpiry = e
		w.check(tg, rc, t0)
		return w, tg
	}
	// expire moves the resolver past e: inputs reports the earliest expiry not before now.
	expire := func(w *world, tg inputs.Target) {
		tr := trusted()
		tr.EarliestWaiverExpiry = next
		w.trust(tg, tr)
	}
	assertExpired := func(t *testing.T, w *world, tg inputs.Target) {
		t.Helper()
		if got := w.reasons()[1]; !slices.Equal(got, []verdict.ReasonCode{verdict.ReasonWaiverExpired}) {
			t.Fatalf("published = %v, want waiver_expired once", got)
		}
		if rc := w.state(tg).Receipt; !rc.EarliestWaiverExpiry.Equal(next) || rc.RunID != 9 {
			t.Errorf("receipt = %+v, want the next expiry and run 9", rc)
		}
	}

	t.Run("on time", func(t *testing.T) {
		w, tg := start(t)
		if w.pass(e.Add(-8 * day)); w.calls() != (calls{}) {
			t.Errorf("8 days before: calls = %+v, want none", w.calls())
		}
		w.pass(e.Add(-3 * day))
		w.pass(e.Add(-3*day + time.Hour))
		if got := w.calls(); got != (calls{warnings: 2}) {
			t.Errorf("3 days before: calls = %+v, want the warning and its repeat only", got)
		}
		for _, warn := range w.pub.warnings {
			if warn.target != tg || !warn.expiry.Equal(e) {
				t.Errorf("warning = %+v, want the same date every time", warn)
			}
		}

		expire(w, tg)
		sum := w.pass(e.Add(time.Hour))
		assertExpired(t, w, tg)
		if !slices.Equal(w.actions.rerunCalls, []int64{9}) || sum.Rerun != 1 {
			t.Errorf("re-runs = %v, summary = %+v, want one re-run", w.actions.rerunCalls, sum)
		}

		before := w.calls()
		if w.pass(e.Add(2 * time.Hour)); w.calls() != before {
			t.Errorf("pass after the repair: calls = %+v, want %+v", w.calls(), before)
		}
	})

	t.Run("scheduler a day late, Rerun fails once", func(t *testing.T) {
		w, tg := start(t)
		expire(w, tg)
		w.actions.rerunErrs[9] = errBoom

		sum := w.pass(e.Add(25 * time.Hour))
		assertExpired(t, w, tg)
		if sum.Errors != 1 || sum.Rerun != 0 {
			t.Errorf("late pass summary = %+v, want the failed re-run in Errors", sum)
		}

		delete(w.actions.rerunErrs, 9)
		sum = w.pass(e.Add(26 * time.Hour))
		if got := w.calls(); got != (calls{published: 1, reruns: 2}) || sum.Rerun != 1 {
			t.Errorf("retry pass: calls = %+v, summary = %+v, want rule 7c to re-run without publishing", got, sum)
		}

		// The attempt advanced; even when it completes without an upload the sweep does not loop.
		for _, status := range []string{"queued", "in_progress", "completed"} {
			w.actions.runs[9].Status = status
			before := w.calls()
			if sum := w.pass(e.Add(27 * time.Hour)); w.calls() != before || sum.Rerun != 0 {
				t.Errorf("run %s at attempt 2: calls = %+v, want %+v", status, w.calls(), before)
			}
		}
		if got := w.state(tg).Receipt.Reason; got != verdict.ReasonWaiverExpired {
			t.Errorf("reason = %q, want waiver_expired to stand", got)
		}
	})
}

func TestRestoredEntitlementNeedsFreshEvidence(t *testing.T) {
	t.Run("suspended after an upload", func(t *testing.T) {
		w := newWorld(t)
		r := w.repo(1, 11, "acme/shop")
		suspended := trusted()
		suspended.Suspended = true
		tg := w.pull(r, 1, suspended)
		w.check(tg, receipt(9, verdict.ReasonHealthy), t0.Add(-day))

		if sum := w.pass(t0); sum.Suspended != 1 || w.state(tg).Receipt.Reason != verdict.ReasonSuspended {
			t.Fatalf("summary = %+v, receipt = %+v, want subscription_suspended", sum, w.state(tg).Receipt)
		}
		if sum := w.pass(t0.Add(time.Hour)); sum.Suspended != 1 || len(w.pub.published) != 1 {
			t.Errorf("still suspended: summary = %+v, published %d, want counted and not republished", sum, len(w.pub.published))
		}

		w.trust(tg, trusted()) // entitlement restored
		sum := w.pass(t0.Add(2 * time.Hour))
		if sum.Rerun != 1 || sum.Suspended != 0 || !slices.Equal(w.actions.rerunCalls, []int64{9}) {
			t.Errorf("summary = %+v, re-runs = %v, want one re-run of run 9", sum, w.actions.rerunCalls)
		}
		if rc := w.state(tg).Receipt; rc.Conclusion != verdict.ActionRequired || len(w.pub.published) != 1 {
			t.Errorf("receipt = %+v, want action_required until a new upload", rc)
		}

		before := w.calls()
		if w.pass(t0.Add(3 * time.Hour)); w.calls() != before {
			t.Errorf("re-run in flight: calls = %+v, want %+v", w.calls(), before)
		}

		w.actions.runs[9].Status = "completed" // the new attempt uploaded nothing
		w.pass(t0.Add(4 * time.Hour))
		if rc := w.state(tg).Receipt; rc.Reason != verdict.ReasonMissingUpload || len(w.actions.rerunCalls) != 1 {
			t.Errorf("receipt = %+v, re-runs = %v, want missing_upload and no second re-run", rc, w.actions.rerunCalls)
		}
		before = w.calls()
		if w.pass(t0.Add(5 * time.Hour)); w.calls() != before {
			t.Errorf("after missing_upload: calls = %+v, want %+v", w.calls(), before)
		}
	})

	t.Run("suspended before any upload", func(t *testing.T) {
		w := newWorld(t)
		r := w.repo(1, 11, "acme/shop")
		tg := w.pull(r, 1, trusted())
		w.check(tg, receipt(0, verdict.ReasonSuspended), t0.Add(-day))

		sum := w.pass(t0)

		if rc := w.state(tg).Receipt; rc.Reason != verdict.ReasonMissingUpload || rc.Conclusion != verdict.ActionRequired {
			t.Errorf("receipt = %+v, want missing_upload", rc)
		}
		if sum.Rerun != 0 || len(w.actions.runCalls) != 0 {
			t.Errorf("summary = %+v, run calls = %v, want no re-run", sum, w.actions.runCalls)
		}
		before := w.calls()
		if w.pass(t0.Add(time.Hour)); w.calls() != before {
			t.Errorf("second pass: calls = %+v, want %+v", w.calls(), before)
		}
	})
}

func TestLateOwnerApproval(t *testing.T) {
	tests := []struct {
		name     string
		approval inputs.Approval
		want     []int64
	}{
		{"approval on the head SHA", inputs.ApprovalGranted, []int64{9}},
		{"approval on an older SHA", inputs.ApprovalMissing, nil},
		{"approval no longer needed", inputs.ApprovalNotRequired, []int64{9}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tr := trusted()
			tr.Approval = tc.approval
			tr.ProtectedPaths = []string{".archfit.yaml"}
			tg := w.pull(r, 1, tr)
			w.check(tg, receipt(9, verdict.ReasonOwnerApprovalMissing), t0.Add(-day))

			for i := range 2 {
				w.pass(t0.Add(time.Duration(i) * time.Hour))
			}

			if !slices.Equal(w.actions.rerunCalls, tc.want) || len(w.pub.published) != 0 {
				t.Errorf("re-runs = %v, published = %v, want re-runs %v and no publication",
					w.actions.rerunCalls, w.reasons(), tc.want)
			}
		})
	}
}

func TestWithdrawnApprovalEndsSuccess(t *testing.T) {
	withdrawn := func(tr inputs.Trusted) inputs.Trusted {
		tr.Approval, tr.ProtectedPaths = inputs.ApprovalMissing, []string{".archfit-app.yaml", ".github/workflows/archfit.yaml"}
		return tr
	}
	granted := trusted()
	granted.Approval, granted.ProtectedPaths = inputs.ApprovalGranted, []string{".archfit-app.yaml"}
	advisory := withdrawn(trusted())
	advisory.Settings.Gate = settings.GateAdvisory
	failed := receipt(9, verdict.ReasonBlockingFindings)
	failed.Conclusion = verdict.Failure
	stalePolicy := receipt(9, verdict.ReasonHealthy)
	stalePolicy.PolicyDigest = "p0"

	tests := []struct {
		name string
		rc   feedback.Receipt
		tr   inputs.Trusted
		want []verdict.ReasonCode
	}{
		{"success", receipt(9, verdict.ReasonHealthy), withdrawn(trusted()), []verdict.ReasonCode{verdict.ReasonOwnerApprovalMissing}},
		{"failure", failed, withdrawn(trusted()), []verdict.ReasonCode{verdict.ReasonOwnerApprovalMissing}},
		{"withdrawn while the policy also changed", stalePolicy, withdrawn(trusted()), []verdict.ReasonCode{verdict.ReasonOwnerApprovalMissing}},
		{"approval still granted", receipt(9, verdict.ReasonHealthy), granted, nil},
		{"the check already asks for action", receipt(9, verdict.ReasonPolicyMismatch), withdrawn(trusted()), nil},
		{"advisory mode", receipt(9, verdict.ReasonHealthy), advisory, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tg := w.pull(w.repo(1, 11, "acme/shop"), 1, tc.tr)
			w.check(tg, tc.rc, t0.Add(-day))

			for i := range 2 {
				w.pass(t0.Add(time.Duration(i) * time.Hour))
			}

			if got := w.reasons()[1]; !slices.Equal(got, tc.want) {
				t.Errorf("published %v, want %v", got, tc.want)
			}
			if len(w.actions.rerunCalls) != 0 {
				t.Errorf("re-runs = %v: no run can pass before an owner approves again", w.actions.rerunCalls)
			}
			if len(tc.want) == 0 {
				return
			}
			pub := w.pub.published[0]
			if r := pub.Outcome.Reasons[0]; r.Detail != ".archfit-app.yaml, .github/workflows/archfit.yaml" {
				t.Errorf("reason = %+v, want the protected paths in Detail", r)
			}
			if rc := pub.Receipt; rc.RunID != 9 || rc.RunAttempt != 1 || rc.PolicyDigest != "p1" {
				t.Errorf("receipt = %+v, want run 9.1 at the current policy p1", rc)
			}
			// The first pass resolves twice: once to find the rule, once inside the lease to decide it.
			if !reflect.DeepEqual(pub.Trusted, tc.tr) || w.resolver.calls[1] != tg || !w.resolver.nows[1].Equal(t0) {
				t.Errorf("published from %+v; want the inputs read again inside the lease at the pass time", pub.Trusted)
			}
		})
	}
}

// Rule 5a decides again inside the publication lease, from the gate check and the inputs as they are there.
func TestWithdrawalDecidesInsideTheLease(t *testing.T) {
	withdrawn := trusted()
	withdrawn.Approval, withdrawn.ProtectedPaths = inputs.ApprovalMissing, []string{".archfit-app.yaml"}
	granted := withdrawn
	granted.Approval = inputs.ApprovalGranted
	newer := receipt(9, verdict.ReasonHealthy)
	newer.RunAttempt = 2

	tests := []struct {
		name        string
		waited      func(w *world, tg inputs.Target) // what lands while the pass waits for the lease
		want        []verdict.ReasonCode
		wantAttempt int
		wantErrors  int
	}{
		{"nothing landed", nil, []verdict.ReasonCode{verdict.ReasonOwnerApprovalMissing}, 1, 0},
		{"a newer attempt turned green", func(w *world, tg inputs.Target) {
			w.pub.states[checkKey(tg)] = feedback.CheckState{Exists: true, StartedAt: t0, Receipt: newer}
		}, []verdict.ReasonCode{verdict.ReasonOwnerApprovalMissing}, 2, 0},
		{"the owner approved again", func(w *world, tg inputs.Target) { w.trust(tg, granted) }, nil, 0, 0},
		{"the inputs cannot be read", func(w *world, tg inputs.Target) { w.resolver.errs[pullKey(tg)] = errBoom }, nil, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tg := w.pull(w.repo(1, 11, "acme/shop"), 1, withdrawn)
			w.check(tg, receipt(9, verdict.ReasonHealthy), t0.Add(-day))
			if tc.waited != nil {
				w.pub.waited = func(inputs.Target) { tc.waited(w, tg) }
			}

			sum := w.pass(t0)

			if got := w.reasons()[1]; !slices.Equal(got, tc.want) || sum.Errors != tc.wantErrors {
				t.Errorf("published %v with %d errors, want %v with %d", got, sum.Errors, tc.want, tc.wantErrors)
			}
			if len(tc.want) > 0 {
				if rc := w.pub.published[0].Receipt; rc.RunID != 9 || rc.RunAttempt != tc.wantAttempt {
					t.Errorf("receipt = %+v, want run 9.%d", rc, tc.wantAttempt)
				}
			}
		})
	}

	t.Run("no lease", func(t *testing.T) {
		w := newWorld(t)
		tg := w.pull(w.repo(1, 11, "acme/shop"), 1, withdrawn)
		w.check(tg, receipt(9, verdict.ReasonHealthy), t0.Add(-day))
		w.pub.errs["Reconcile:acme/shop"] = errBoom

		if sum := w.pass(t0); sum.Errors != 1 || len(w.pub.published) != 0 {
			t.Errorf("summary %+v, published %v; want the pull skipped and nothing written", sum, w.reasons())
		}
	})
}

func TestStoreOutageDoesNotSuspend(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	tg := w.pull(r, 1, trusted()) // the Store failed inside Resolve: Suspended is false
	want := receipt(9, verdict.ReasonHealthy)
	w.check(tg, want, t0.Add(-day))

	sum := w.pass(t0)

	if w.calls() != (calls{}) || sum.Suspended != 0 || w.state(tg).Receipt != want {
		t.Errorf("calls = %+v, summary = %+v, receipt = %+v, want the receipt to stand", w.calls(), sum, w.state(tg).Receipt)
	}
}
