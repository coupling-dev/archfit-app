package sweep

import (
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// share opens pull request number on the head of tg, into base.
func (w *world) share(tg inputs.Target, number int, base string) {
	w.pulls.open[tg.Repo.FullName] = append(w.pulls.open[tg.Repo.FullName],
		api.Pull{Number: number, State: "open", HeadSHA: tg.HeadSHA, HeadRepo: tg.Repo.FullName, BaseRef: base})
}

// Rule 5d: a success whose head is shared with an open pull request into another base becomes shared_head, with no
// re-run. Nothing else shares: another head, the default branch, a receipt that is not a success.
func TestSharedHeadRule(t *testing.T) {
	failure := receipt(201, verdict.ReasonBlockingFindings)
	failure.Conclusion = verdict.Failure
	tests := []struct {
		name   string
		rc     feedback.Receipt
		others func(w *world, tg inputs.Target)
		want   []verdict.ReasonCode
	}{
		{"into another base", receipt(201, verdict.ReasonHealthy), func(w *world, tg inputs.Target) { w.share(tg, 2, "loosened") },
			[]verdict.ReasonCode{verdict.ReasonSharedHead}},
		{"into the default branch", receipt(201, verdict.ReasonHealthy), func(w *world, tg inputs.Target) { w.share(tg, 2, "main") }, nil},
		{"another head into another base", receipt(201, verdict.ReasonHealthy), func(w *world, tg inputs.Target) {
			w.share(tg, 2, "loosened")
			w.pulls.open[tg.Repo.FullName][1].HeadSHA = "other"
		}, nil},
		{"no other pull request", receipt(201, verdict.ReasonHealthy), func(*world, inputs.Target) {}, nil},
		{"a failure is not a success", failure, func(w *world, tg inputs.Target) { w.share(tg, 2, "loosened") }, nil},
		{"a receipt that asks for action", receipt(201, verdict.ReasonMissingUpload), func(w *world, tg inputs.Target) { w.share(tg, 2, "loosened") }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tg := w.pull(r, 1, trusted())
			w.check(tg, tt.rc, t0.Add(-day))
			tt.others(w, tg)

			w.pass(t0)

			if got := w.reasons()[1]; !slices.Equal(got, tt.want) {
				t.Errorf("published %v, want %v", got, tt.want)
			}
			if len(w.actions.rerunCalls) != 0 {
				t.Errorf("re-runs %v, want none", w.actions.rerunCalls)
			}
		})
	}
}

func TestSharedHeadDetailIsBounded(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	tg := w.pull(r, 1, trusted())
	w.check(tg, receipt(201, verdict.ReasonHealthy), t0.Add(-day))
	for n := 2; n <= 8; n++ {
		w.share(tg, 10-n, "x`\n"+strings.Repeat("é", 100)) // the numbers come in descending order
	}

	w.pass(t0)

	pub := w.pub.published[0]
	r0 := pub.Outcome.Reasons[0]
	if pub.Outcome.Conclusion != verdict.ActionRequired || r0.Code != verdict.ReasonSharedHead {
		t.Fatalf("published %+v", pub.Outcome)
	}
	if !strings.HasPrefix(r0.Detail, "#2 into x"+strings.Repeat("é", 79)+"…, #3 into ") || !strings.HasSuffix(r0.Detail, ", and 2 more") {
		t.Errorf("Detail = %q", r0.Detail)
	}
	if strings.ContainsAny(r0.Detail+r0.Next, "\n") || strings.Count(r0.Next, "`") != 2 {
		t.Errorf("Detail = %q, Next = %q: want no newline and the base alone in backticks", r0.Detail, r0.Next)
	}
}

// The rule decides again inside the lease: a pull request that closed, a newer receipt, a listing that fails, a
// withdrawn approval or inputs that moved while the caller waited all leave the success as it is.
func TestSharedHeadDecidesAgainInsideTheLease(t *testing.T) {
	tests := []struct {
		name       string
		waited     func(w *world, tg inputs.Target)
		wantErrors int
	}{
		{"the other pull request closed", func(w *world, tg inputs.Target) { w.pulls.open[tg.Repo.FullName] = w.pulls.open[tg.Repo.FullName][:1] }, 0},
		{"a newer run replaced the receipt", func(w *world, tg inputs.Target) {
			w.pub.states[checkKey(tg)] = feedback.CheckState{Exists: true, Receipt: receipt(202, verdict.ReasonHealthy)}
		}, 0},
		{"the listing failed", func(w *world, tg inputs.Target) { w.pulls.errs[tg.Repo.FullName] = errBoom }, 1},
		{"the approval was withdrawn", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.Approval = inputs.ApprovalMissing
			w.trust(tg, tr)
		}, 0},
		{"the inputs moved", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.PolicyDigests = []string{"p2"}
			w.trust(tg, tr)
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tg := w.pull(r, 1, trusted())
			w.check(tg, receipt(201, verdict.ReasonHealthy), t0.Add(-day))
			w.share(tg, 2, "loosened")
			listed := false
			w.pub.waited = func(inputs.Target) {
				if !listed {
					listed = true
					tt.waited(w, tg)
				}
			}

			sum := w.pass(t0)

			for _, p := range w.pub.published {
				if p.Receipt.Reason == verdict.ReasonSharedHead {
					t.Errorf("published shared_head: %+v", p.Outcome)
				}
			}
			if sum.Errors != tt.wantErrors {
				t.Errorf("errors = %d, want %d", sum.Errors, tt.wantErrors)
			}
		})
	}
}
