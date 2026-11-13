package sweep

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestStaleReceipt(t *testing.T) {
	healthy := func(policy, baseline, labels string) feedback.Receipt {
		rc := receipt(9, verdict.ReasonHealthy)
		rc.PolicyDigest, rc.BaselineDigest, rc.LabelsDigest = policy, baseline, labels
		return rc
	}
	policyMissing := receipt(9, verdict.ReasonPolicyMissing)
	policyMissing.PolicyDigest = ""
	tests := []struct {
		name                     string
		policy, baseline, labels []string
		rc                       feedback.Receipt
		want                     bool
	}{
		{"policy equal to base", []string{"p1"}, nil, nil, healthy("p1", "", ""), false},
		{"policy equal to approved head", []string{"p1", "p2"}, nil, nil, healthy("p2", "", ""), false},
		{"policy different", []string{"p1"}, nil, nil, healthy("p9", "", ""), true},
		{"no trusted policy, receipt without digest", nil, nil, nil, policyMissing, false},
		{"no trusted policy, receipt with digest", nil, nil, nil, healthy("p1", "", ""), true},
		{"policy empty with a trusted policy", []string{"p1"}, nil, nil, healthy("", "", ""), true},
		{"baseline empty, none trusted", []string{"p1"}, nil, nil, healthy("p1", "", ""), false},
		{"baseline empty with a trusted baseline", []string{"p1"}, []string{"b1"}, nil, healthy("p1", "", ""), true},
		{"baseline set, none trusted", []string{"p1"}, nil, nil, healthy("p1", "b1", ""), true},
		{"zero receipt, empty trusted lists", nil, nil, nil, feedback.Receipt{}, true},
		{"baseline equal to approved head", []string{"p1"}, []string{"b1", "b2"}, nil, healthy("p1", "b2", ""), false},
		{"labels equal", []string{"p1"}, nil, []string{"l1"}, healthy("p1", "", "l1"), false},
		{"labels different", []string{"p1"}, nil, []string{"l1"}, healthy("p1", "", "l9"), true},
		{"labels empty with trusted labels", []string{"p1"}, nil, []string{"l1"}, healthy("p1", "", ""), true},
		{"labels set, none trusted", []string{"p1"}, nil, nil, healthy("p1", "", "l1"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := inputs.Trusted{PolicyDigests: tc.policy, BaselineDigests: tc.baseline, LabelsDigests: tc.labels}
			if got := stale(tc.rc, tr); got != tc.want {
				t.Errorf("stale = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("agrees with inputs.Accepts", func(t *testing.T) {
		for _, list := range [][]string{nil, {"d1"}, {"d1", "d2"}} {
			for _, digest := range []string{"", "d1", "d2", "d9"} {
				want := !inputs.Accepts(list, digest)
				fields := map[string]struct {
					tr inputs.Trusted
					rc feedback.Receipt
				}{
					"policy":   {inputs.Trusted{PolicyDigests: list}, feedback.Receipt{PolicyDigest: digest}},
					"baseline": {inputs.Trusted{BaselineDigests: list}, feedback.Receipt{BaselineDigest: digest}},
					"labels":   {inputs.Trusted{LabelsDigests: list}, feedback.Receipt{LabelsDigest: digest}},
				}
				for field, f := range fields {
					f.rc.Reason = verdict.ReasonHealthy
					if got := stale(f.rc, f.tr); got != want {
						t.Errorf("%s: stale(%q against %v) = %v, want %v", field, digest, list, got, want)
					}
				}
			}
		}
	})
}

func TestWithdrawnReceipt(t *testing.T) {
	conclusions := map[verdict.Conclusion]bool{
		verdict.Success: true, verdict.Failure: true, verdict.ActionRequired: false, "": false, "neutral": false,
	}
	approvals := map[inputs.Approval]bool{
		inputs.ApprovalMissing: true, "": true, "skipped": true, inputs.ApprovalGranted: false, inputs.ApprovalNotRequired: false,
	}
	for conclusion, decided := range conclusions {
		for approval, unapproved := range approvals {
			rc := feedback.Receipt{RunID: 9, RunAttempt: 1, Conclusion: conclusion, Reason: verdict.ReasonHealthy}
			if got, want := Withdrawn(rc, inputs.Trusted{Approval: approval}), decided && unapproved; got != want {
				t.Errorf("Withdrawn(%q receipt, approval %q) = %v, want %v", conclusion, approval, got, want)
			}
		}
	}
}

// TestDecidedReceipt: success, failure, and an action_required whose reason comes from the report and itself asks
// for action are decisions on the report. Every other receipt says what to do and is not decided.
func TestDecidedReceipt(t *testing.T) {
	tests := []struct {
		name string
		rc   feedback.Receipt
		want bool
	}{
		{"success", feedback.Receipt{Conclusion: verdict.Success, Reason: verdict.ReasonHealthy}, true},
		{"failure", feedback.Receipt{Conclusion: verdict.Failure, Reason: verdict.ReasonBlockingFindings}, true},
		{"missing required coverage", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonMissingCoverage}, true},
		{"baseline not comparable", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonBaselineNotComparable}, true},
		{"policy defect", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonPolicyDefect}, true},
		{"unknown engine identity", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonUnknownIdentity}, false},
		{"owner approval missing", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing}, false},
		{"stale inputs", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonStaleInputs}, false},
		{"incoherent: action required with healthy", feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonHealthy}, false},
		{"incoherent: success with a coverage reason", feedback.Receipt{Conclusion: "neutral", Reason: verdict.ReasonMissingCoverage}, false},
		{"unreadable", feedback.Receipt{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decided(tt.rc); got != tt.want {
				t.Errorf("Decided(%+v) = %v, want %v", tt.rc, got, tt.want)
			}
			if got, want := Withdrawn(tt.rc, inputs.Trusted{Approval: inputs.ApprovalMissing}), tt.want; got != want {
				t.Errorf("Withdrawn without an approval = %v, want %v", got, want)
			}
			if Withdrawn(tt.rc, inputs.Trusted{Approval: inputs.ApprovalGranted}) {
				t.Error("Withdrawn while the approval holds")
			}
		})
	}
}

func TestNoPolicyWindowIsQuiet(t *testing.T) {
	// start is the no-policy window: settings merged, the receipt says policy_missing.
	start := func(t *testing.T) (*world, api.Repo, inputs.Target) {
		w := newWorld(t)
		r := w.repo(1, 11, "acme/shop")
		noPolicy := trusted()
		noPolicy.PolicyDigests = nil
		tg := w.pull(r, 1, noPolicy)
		rc := receipt(9, verdict.ReasonPolicyMissing)
		rc.PolicyDigest = ""
		w.check(tg, rc, t0.Add(-day))
		return w, r, tg
	}
	assertQuiet := func(t *testing.T, w *world, before calls, sum Summary) {
		t.Helper()
		if got := w.calls(); got != before {
			t.Errorf("calls = %+v, want unchanged %+v", got, before)
		}
		if sum.Rerun != 0 || sum.Errors != 0 {
			t.Errorf("summary = %+v, want no re-run and no error", sum)
		}
	}
	assertRepaired := func(t *testing.T, w *world, tg inputs.Target, sum Summary) {
		t.Helper()
		if got := w.reasons()[1]; !slices.Equal(got, []verdict.ReasonCode{verdict.ReasonStaleInputs}) {
			t.Errorf("published = %v, want one stale_inputs", got)
		}
		if !slices.Equal(w.actions.rerunCalls, []int64{9}) {
			t.Errorf("re-runs = %v, want exactly run 9", w.actions.rerunCalls)
		}
		if sum.Rerun != 1 {
			t.Errorf("Summary.Rerun = %d, want 1", sum.Rerun)
		}
		if got := w.state(tg).Receipt.PolicyDigest; got != "p1" {
			t.Errorf("new receipt policy digest = %q, want p1", got)
		}
	}

	for _, name := range []string{"policy never merged", "policy deleted from the base"} {
		t.Run(name, func(t *testing.T) {
			w, _, tg := start(t)
			for i := range 2 {
				before := w.calls()
				sum := w.pass(t0.Add(time.Duration(i) * time.Hour))
				assertQuiet(t, w, before, sum)
			}
			if got := w.state(tg).Receipt.Reason; got != verdict.ReasonPolicyMissing {
				t.Fatalf("reason = %q, want the policy_missing feedback to stay", got)
			}
			if len(w.actions.runCalls) != 0 {
				t.Errorf("Run called %d times in the quiet window", len(w.actions.runCalls))
			}

			w.trust(tg, trusted()) // the policy merges
			assertRepaired(t, w, tg, w.pass(t0.Add(2*time.Hour)))

			before := w.calls()
			assertQuiet(t, w, before, w.pass(t0.Add(3*time.Hour)))
		})
	}

	t.Run("RunRepo from the push trigger", func(t *testing.T) {
		w, r, tg := start(t)
		before := w.calls()
		assertQuiet(t, w, before, w.passRepo(r, t0))

		w.trust(tg, trusted())
		assertRepaired(t, w, tg, w.passRepo(r, t0.Add(time.Minute)))

		before = w.calls()
		assertQuiet(t, w, before, w.pass(t0.Add(time.Hour)))
	})
}

func TestUnreadableReceiptIsBounded(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	tg := w.pull(r, 1, trusted())
	w.check(tg, feedback.Receipt{}, t0.Add(-day)) // completed check, no receipt marker

	w.pass(t0)
	if len(w.pub.published) != 1 {
		t.Fatalf("published %d times, want 1", len(w.pub.published))
	}
	rc := w.pub.published[0].Receipt
	if rc.Reason != verdict.ReasonStaleInputs || rc.RunID != 0 || rc.RunAttempt != 0 || rc.ReportDigest != "" {
		t.Errorf("receipt = %+v, want stale_inputs with a zero run identity", rc)
	}
	if len(w.actions.runCalls) != 0 || len(w.actions.rerunCalls) != 0 {
		t.Errorf("Run calls %v, Rerun calls %v, want none", w.actions.runCalls, w.actions.rerunCalls)
	}

	before := w.calls()
	w.pass(t0.Add(time.Hour))
	if got := w.calls(); got != before {
		t.Errorf("second pass calls = %+v, want unchanged %+v", got, before)
	}
}

func TestExpiryRule(t *testing.T) {
	e := t0.Add(10 * day) // the run (created a day before t0) is still younger than 30 days then
	tests := []struct {
		name          string
		now           time.Time
		startedAt     time.Time
		receiptExpiry time.Time
		trustedExpiry time.Time
		wantWarn      bool
		wantExpired   bool
	}{
		{"8 days before", e.Add(-8 * day), t0, e, e, false, false},
		{"7 days before", e.Add(-7 * day), t0, e, e, true, false},
		{"1 second before", e.Add(-time.Second), t0, e, e, true, false},
		{"equal, published before the expiry", e, t0, e, time.Time{}, false, true},
		{"equal, published at the expiry", e, e, e, time.Time{}, false, false},
		{"after, published before the expiry", e.Add(day), t0, e, time.Time{}, false, true},
		{"after, published after the expiry", e.Add(day), e.Add(time.Hour), e, time.Time{}, false, false},
		{"no receipt expiry, trusted expiry near", e.Add(-day), t0, time.Time{}, e, true, false},
		{"no receipt expiry, trusted expiry reached", e, t0, time.Time{}, e, false, true},
		{"no expiry at all", e, t0, time.Time{}, time.Time{}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tr := trusted()
			tr.EarliestWaiverExpiry = tc.trustedExpiry
			tg := w.pull(r, 1, tr)
			rc := receipt(9, verdict.ReasonHealthy)
			rc.EarliestWaiverExpiry = tc.receiptExpiry
			w.check(tg, rc, tc.startedAt)

			sum := w.pass(tc.now)

			var wantWarnings []warning
			if tc.wantWarn {
				wantWarnings = []warning{{tg, e}}
			}
			if !reflect.DeepEqual(w.pub.warnings, wantWarnings) {
				t.Errorf("warnings = %+v, want %+v", w.pub.warnings, wantWarnings)
			}
			var wantReasons []verdict.ReasonCode
			var wantReruns []int64
			if tc.wantExpired {
				wantReasons, wantReruns = []verdict.ReasonCode{verdict.ReasonWaiverExpired}, []int64{9}
			}
			if got := w.reasons()[1]; !slices.Equal(got, wantReasons) {
				t.Errorf("published = %v, want %v", got, wantReasons)
			}
			if !slices.Equal(w.actions.rerunCalls, wantReruns) {
				t.Errorf("re-runs = %v, want %v", w.actions.rerunCalls, wantReruns)
			}
			want := Summary{Repos: 1, Pulls: 1}
			if tc.wantWarn {
				want.Warned = 1
			}
			if tc.wantExpired {
				want.Rerun = 1
			}
			if sum != want {
				t.Errorf("summary = %+v, want %+v", sum, want)
			}
		})
	}
}

func TestRerunGuard(t *testing.T) {
	tests := []struct {
		name      string
		runID     int64
		status    string // "" removes the run
		attempt   int
		wantRun   bool
		wantRerun bool
	}{
		{"run ID 0", 0, "", 0, false, false},
		{"run not found", 9, "", 0, true, false},
		{"run queued", 9, "queued", 1, true, false},
		{"run in progress", 9, "in_progress", 1, true, false},
		{"completed at the receipt's attempt", 9, "completed", 1, true, true},
		{"completed at a later attempt", 9, "completed", 2, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tg := w.pull(r, 1, trusted())
			// A fresh stale_inputs receipt reaches only rule 7c, which is the guard alone.
			w.check(tg, receipt(tc.runID, verdict.ReasonStaleInputs), t0.Add(-time.Hour))
			delete(w.actions.runs, 9)
			if tc.status != "" {
				w.run(9, tc.attempt, tc.status)
				w.actions.runs[9].HeadSHA = tg.HeadSHA
			}

			sum := w.pass(t0)

			if got := len(w.actions.runCalls) == 1; got != tc.wantRun {
				t.Errorf("Run calls = %v, want called: %v", w.actions.runCalls, tc.wantRun)
			}
			if got := len(w.actions.rerunCalls) == 1; got != tc.wantRerun {
				t.Errorf("Rerun calls = %v, want called: %v", w.actions.rerunCalls, tc.wantRerun)
			}
			if (sum.Rerun == 1) != tc.wantRerun || sum.Errors != 0 {
				t.Errorf("summary = %+v", sum)
			}
			if len(w.pub.published) != 0 {
				t.Errorf("rule 7c published %v", w.reasons())
			}
		})
	}
}

// An Archfit 3 rule that would re-run a run GitHub can no longer re-run (30 days old) requests none: rule 7c leaves its
// reason, rule 7b publishes missing_upload, which names the recovery.
func TestArchfit3RulesDoNotRerunAnOldRun(t *testing.T) {
	tests := []struct {
		name   string
		reason verdict.ReasonCode
		want   []verdict.ReasonCode
	}{
		{"7c", verdict.ReasonStaleInputs, nil},
		{"7b", verdict.ReasonOwnerApprovalMissing, []verdict.ReasonCode{verdict.ReasonMissingUpload}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tg := w.pull(r, 1, trusted())
			w.check(tg, receipt(9, tc.reason), t0.Add(-time.Hour))
			w.actions.runs[9].CreatedAt = t0.Add(-rerunMaxAge)

			sum := w.pass(t0)

			if len(w.actions.rerunCalls) != 0 || sum.Rerun != 0 || !slices.Equal(w.reasons()[1], tc.want) {
				t.Errorf("re-runs %v, published %v; want none and %v", w.actions.rerunCalls, w.reasons()[1], tc.want)
			}
		})
	}
}

// TestRerunConditions is the exported guard: the run checks it always made, then each condition of design 10.5 false
// on its own.
func TestRerunConditions(t *testing.T) {
	const head, base, tip = "head1", "base1", "tip1"
	failed := errors.New("boom")
	now := t0
	run := func(id int64, attempt int, status string, created time.Time) *api.WorkflowRun {
		return &api.WorkflowRun{ID: id, Attempt: attempt, Status: status, Event: "pull_request", HeadSHA: head,
			WorkflowPath: workflowPath, PullRequests: []int{1}, CreatedAt: created}
	}
	policyOnly := api.Comparison{Status: "ahead", Paths: []string{".archfit.yaml"}}
	tests := []struct {
		name     string
		change   func(a *fakeActions, in *RerunInput)
		want     RerunResult
		unmet    Unmet
		wantErr  error
		wantReqs int
	}{
		{name: "all four hold", want: RerunRequested, wantReqs: 1},
		{name: "run ID 0", change: func(_ *fakeActions, in *RerunInput) { in.Receipt.RunID = 0 }, want: RerunImpossible, unmet: UnmetNoRun},
		{name: "run not found", change: func(a *fakeActions, _ *RerunInput) { delete(a.runs, 9) }, want: RerunImpossible, unmet: UnmetNoRun},
		{name: "run queued", change: func(a *fakeActions, _ *RerunInput) { a.runs[9].Status = "queued" }, want: RerunInFlight},
		{name: "run in progress", change: func(a *fakeActions, _ *RerunInput) { a.runs[9].Status = "in_progress" }, want: RerunInFlight},
		{name: "completed at a later attempt", change: func(a *fakeActions, _ *RerunInput) { a.runs[9].Attempt = 2 }, want: RerunImpossible, unmet: UnmetNotLatest},
		{name: "Run fails", change: func(a *fakeActions, _ *RerunInput) { a.runErrs[9] = failed }, want: RerunInFlight, wantErr: failed},
		{name: "Rerun fails", change: func(a *fakeActions, _ *RerunInput) { a.rerunErrs[9] = failed }, want: RerunInFlight, wantErr: failed, wantReqs: 1},

		{name: "the workflow does not read the policy from the tip", change: func(_ *fakeActions, in *RerunInput) { in.ReadsTip = false },
			want: RerunImpossible, unmet: UnmetPolicyNotFromTip},

		{name: "the run is of an older head", change: func(_ *fakeActions, in *RerunInput) { in.Head = "head2" }, want: RerunImpossible, unmet: UnmetNotLatest},
		{name: "a newer run of the head completed", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "completed", now.Add(-time.Hour))
		}, want: RerunImpossible, unmet: UnmetNotLatest},
		{name: "a newer run of the head is in progress", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "in_progress", now.Add(-time.Hour))
		}, want: RerunInFlight},
		{name: "a newer run in progress, and a newest one completed", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "in_progress", now.Add(-2*time.Hour))
			a.runs[11] = run(11, 1, "completed", now.Add(-time.Hour))
		}, want: RerunImpossible, unmet: UnmetNotLatest},
		{name: "a newer run completed, and a newest one in progress", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "completed", now.Add(-2*time.Hour))
			a.runs[11] = run(11, 1, "queued", now.Add(-time.Hour))
		}, want: RerunInFlight},
		{name: "a run created at the same time with a higher id is newer", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "completed", now.Add(-day))
		}, want: RerunImpossible, unmet: UnmetNotLatest},
		{name: "an older run of the head", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[8] = run(8, 1, "completed", now.Add(-2*day))
		}, want: RerunRequested, wantReqs: 1},
		{name: "a newer run of another pull request", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "completed", now.Add(-time.Hour))
			a.runs[10].PullRequests = []int{2}
		}, want: RerunRequested, wantReqs: 1},
		{name: "a newer run of another event", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[10] = run(10, 1, "completed", now.Add(-time.Hour))
			a.runs[10].Event = "push"
		}, want: RerunRequested, wantReqs: 1},
		{name: "the head's runs cannot be read", change: func(a *fakeActions, _ *RerunInput) { a.headRunsErr = failed }, want: RerunInFlight, wantErr: failed},

		{name: "a second short of 30 days", change: func(a *fakeActions, _ *RerunInput) {
			a.runs[9].CreatedAt = now.Add(-rerunMaxAge + time.Second)
		}, want: RerunRequested, wantReqs: 1},
		{name: "exactly 30 days", change: func(a *fakeActions, _ *RerunInput) { a.runs[9].CreatedAt = now.Add(-rerunMaxAge) },
			want: RerunImpossible, unmet: UnmetTooOld},
		{name: "31 days", change: func(a *fakeActions, _ *RerunInput) { a.runs[9].CreatedAt = now.Add(-31 * day) },
			want: RerunImpossible, unmet: UnmetTooOld},

		{name: "the target moved in the policy only", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compares[base+".."+tip] = policyOnly
		}, want: RerunRequested, wantReqs: 1},
		{name: "the target moved in a code file too", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compares[base+".."+tip] = api.Comparison{Status: "ahead", Paths: []string{".archfit.yaml", "pkg/a.go"}}
		}, want: RerunImpossible, unmet: UnmetBaseChanged},
		{name: "the target moved in the workflow", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compares[base+".."+tip] = api.Comparison{Status: "ahead", Paths: []string{workflowPath}}
		}, want: RerunImpossible, unmet: UnmetBaseChanged},
		{name: "a cut file list", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compares[base+".."+tip] = api.Comparison{Status: "ahead", Paths: []string{".archfit.yaml"}, Truncated: true}
		}, want: RerunImpossible, unmet: UnmetBaseChanged},
		{name: "a base that is not behind the tip", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compares[base+".."+tip] = api.Comparison{Status: "diverged", Paths: []string{".archfit.yaml"}}
		}, want: RerunImpossible, unmet: UnmetBaseChanged},
		{name: "a base GitHub does not know", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compareErrs[base+".."+tip] = api.ErrNotFound
		}, want: RerunImpossible, unmet: UnmetBaseChanged},
		{name: "the compare fails", change: func(a *fakeActions, in *RerunInput) {
			in.Tip = tip
			a.compareErrs[base+".."+tip] = failed
		}, want: RerunInFlight, wantErr: failed},
		{name: "unknown event base", change: func(_ *fakeActions, in *RerunInput) { in.EventBase = "" }, want: RerunImpossible, unmet: UnmetBaseChanged},
		{name: "unknown tip", change: func(_ *fakeActions, in *RerunInput) { in.Tip = "" }, want: RerunImpossible, unmet: UnmetBaseChanged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeActions{runs: map[int64]*api.WorkflowRun{9: run(9, 1, "completed", now.Add(-day))},
				runErrs: map[int64]error{}, rerunErrs: map[int64]error{},
				compares: map[string]api.Comparison{}, compareErrs: map[string]error{}}
			in := RerunInput{Receipt: receipt(9, verdict.ReasonStaleInputs), Head: head, EventBase: base, Tip: base, ReadsTip: true}
			if tc.change != nil {
				tc.change(a, &in)
			}
			got, unmet, err := Rerun(context.Background(), a, api.RepoRef{InstallationID: 1, FullName: "acme/shop"}, in, now)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) || got != tc.want || unmet != tc.unmet {
				t.Errorf("Rerun = %v, %q, %v; want %v, %q, %v", got, unmet, err, tc.want, tc.unmet, tc.wantErr)
			}
			if len(a.rerunCalls) != tc.wantReqs {
				t.Errorf("Rerun requests %v, want %d", a.rerunCalls, tc.wantReqs)
			}
		})
	}
}

func TestWithoutReceipt(t *testing.T) {
	codes := []verdict.ReasonCode{
		verdict.ReasonMissingUpload, verdict.ReasonStaleInputs, verdict.ReasonWaiverExpired,
		verdict.ReasonForkUnsupported, verdict.ReasonSuspended, verdict.ReasonTargetMoved,
	}
	expiry := t0.Add(3 * day)
	full := trusted()
	full.PolicyDigests = []string{"p1", "p2"}
	full.BaselineDigests = []string{"b1"}
	full.LabelsDigests = []string{"l1", "l2"}
	full.EarliestWaiverExpiry = expiry
	previous := feedback.Receipt{
		RunID: 9, RunAttempt: 2, ReportDigest: "rd", BaseSHA: "base", PolicyDigest: "old", BaselineDigest: "old", LabelsDigest: "old",
		EarliestWaiverExpiry: t0.Add(-day), Conclusion: verdict.Success, Reason: verdict.ReasonHealthy,
	}
	tg := inputs.Target{Repo: api.Repo{ID: 11}, PullRequest: 1, HeadSHA: "abc", BaseRef: "main"}

	for _, code := range codes {
		tests := []struct {
			name    string
			trusted inputs.Trusted
			rc      feedback.Receipt
			want    feedback.Receipt
		}{
			{"previous receipt", full, previous, feedback.Receipt{
				RunID: 9, RunAttempt: 2, ReportDigest: "rd", BaseSHA: "base", PolicyDigest: "p1", BaselineDigest: "b1", LabelsDigest: "l1",
				EarliestWaiverExpiry: expiry, Conclusion: verdict.ActionRequired, Reason: code,
			}},
			{"no previous receipt, nothing trusted", inputs.Trusted{SettingsFound: true}, feedback.Receipt{},
				feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: code}},
		}
		for _, tc := range tests {
			t.Run(string(code)+"/"+tc.name, func(t *testing.T) {
				p := Without(code, tg, tc.trusted, tc.rc)
				if p.Receipt != tc.want {
					t.Errorf("receipt = %+v, want %+v", p.Receipt, tc.want)
				}
				if stale(p.Receipt, tc.trusted) {
					t.Errorf("the receipt %+v is stale for its own trusted inputs", p.Receipt)
				}
				if !reflect.DeepEqual(p.Outcome, verdict.Without(code)) || p.Outcome.Conclusion != verdict.ActionRequired {
					t.Errorf("outcome = %+v", p.Outcome)
				}
				if p.Report != nil || !reflect.ValueOf(p.Identity).IsZero() {
					t.Errorf("report = %v, identity = %+v, want nil and zero", p.Report, p.Identity)
				}
				if p.Target != tg || !reflect.DeepEqual(p.Trusted, tc.trusted) {
					t.Errorf("target = %+v, trusted = %+v", p.Target, p.Trusted)
				}
			})
		}
	}
}

func TestWithoutShowsTheEntitlementReason(t *testing.T) {
	tr := trusted()
	tr.Suspended, tr.Entitlement = true, inputs.Entitlement{Reason: "Trial ended. Subscribe in the Archfit App settings."}
	p := Without(verdict.ReasonSuspended, inputs.Target{}, tr, feedback.Receipt{})
	if !reflect.DeepEqual(p.Outcome, verdict.Suspended(tr.Entitlement)) || p.Outcome.Reasons[0].Next != tr.Entitlement.Reason {
		t.Errorf("outcome = %+v", p.Outcome)
	}
	if p.Receipt.Reason != verdict.ReasonSuspended || p.Receipt.Conclusion != verdict.ActionRequired {
		t.Errorf("receipt = %+v", p.Receipt)
	}
	// The reason of another code is never replaced.
	if p := Without(verdict.ReasonMissingUpload, inputs.Target{}, tr, feedback.Receipt{}); !reflect.DeepEqual(p.Outcome, verdict.Without(verdict.ReasonMissingUpload)) {
		t.Errorf("missing_upload outcome = %+v", p.Outcome)
	}
}

func TestTraversalRotation(t *testing.T) {
	w := newWorld(t)
	for _, id := range []int64{30, 10, 20} { // GitHub's order is not the ID order
		w.repo(id, id, "acme/shop")
	}
	want := [][]int64{{10, 20, 30}, {20, 30, 10}, {30, 10, 20}}
	for hour, order := range want {
		w.repos.listRepos = nil
		sum := w.pass(t0.Add(time.Duration(hour)*time.Hour + 59*time.Minute))
		if !slices.Equal(w.repos.listRepos, order) {
			t.Errorf("hour %d: visited %v, want %v", hour, w.repos.listRepos, order)
		}
		if sum.Repos != 3 {
			t.Errorf("hour %d: Summary.Repos = %d, want 3", hour, sum.Repos)
		}
	}
}
