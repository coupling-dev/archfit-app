package sweep

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

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
			in := RerunInput{Receipt: feedback.Receipt{RunID: 9, RunAttempt: 1}, Head: head, EventBase: base, Tip: base, ReadsTip: true}
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
