package sweep

import (
	"errors"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// retargetRules are the rules that ask for a re-run: each with a receipt it acts on, the inputs it acts under, and
// what it does when the receipt was measured on the tip.
var retargetRules = []struct {
	rule     string
	receipt  func() feedback.Receipt
	trusted  func() inputs.Trusted
	started  time.Time // when the check began
	wantPubs []verdict.ReasonCode
}{
	{"5b", func() feedback.Receipt { return receipt(101, verdict.ReasonHealthy) },
		func() inputs.Trusted { tr := trusted(); tr.PolicyDigests = []string{"p2"}; return tr },
		t0.Add(-day), []verdict.ReasonCode{verdict.ReasonStaleInputs}},
	{"6b", func() feedback.Receipt {
		rc := receipt(101, verdict.ReasonMissingUpload) // not a success: rule 5c, which fails on a tip it cannot read, stays out
		rc.EarliestWaiverExpiry = t0.Add(-day)
		return rc
	}, trusted, t0.Add(-day - time.Hour), []verdict.ReasonCode{verdict.ReasonWaiverExpired}},
	{"7a", func() feedback.Receipt { return receipt(101, verdict.ReasonSuspended) }, trusted, t0.Add(-day), nil},
	{"7b", func() feedback.Receipt { return receipt(101, verdict.ReasonOwnerApprovalMissing) }, trusted, t0.Add(-day), nil},
	{"7c stale_inputs", func() feedback.Receipt { return receipt(101, verdict.ReasonStaleInputs) }, trusted, t0.Add(-day), nil},
	{"7c waiver_expired", func() feedback.Receipt { return receipt(101, verdict.ReasonWaiverExpired) }, trusted, t0.Add(-day), nil},
}

func TestARerunNeverFollowsAMovedTarget(t *testing.T) {
	bases := []struct {
		name  string
		base  string
		moved bool
	}{
		{"base behind the tip", oldBase, true},
		{"base is the tip", tip0, false},
		{"receipt without a base", "", false},
	}
	for _, r := range retargetRules {
		for _, b := range bases {
			t.Run(r.rule+"/"+b.name, func(t *testing.T) {
				w := newWorld(t)
				repo := w.repo(1, 11, "acme/shop")
				tg := w.pull(repo, 1, r.trusted())
				rc := r.receipt()
				rc.BaseSHA = b.base
				w.check(tg, rc, r.started)

				w.pass(t0)

				if b.moved {
					got := w.pub.published
					if len(got) != 1 || got[0].Receipt.Reason != verdict.ReasonTargetMoved || len(w.actions.rerunCalls) != 0 {
						t.Fatalf("published %v, reruns %v, want one target_moved and no re-run", w.reasons(), w.actions.rerunCalls)
					}
					if d := got[0].Outcome.Reasons[0]; d.Detail != oldBase+".."+tip0 || got[0].Receipt.BaseSHA != oldBase {
						t.Errorf("detail = %q, base = %q, want the range only over the same base", d.Detail, got[0].Receipt.BaseSHA)
					}
					before := w.calls()
					w.pass(t0)
					if w.calls() != before {
						t.Errorf("second pass changed %+v to %+v, want quiet", before, w.calls())
					}
					return
				}
				if got := w.reasons()[1]; !equalReasons(got, r.wantPubs) || len(w.actions.rerunCalls) != 1 {
					t.Errorf("published %v, reruns %v, want %v and one re-run", got, w.actions.rerunCalls, r.wantPubs)
				}
			})
		}
	}
}

func TestARerunRuleReadsTheTipOncePerPass(t *testing.T) {
	w := newWorld(t)
	repo := w.repo(1, 11, "acme/shop")
	for n := 1; n <= 3; n++ {
		tg := w.pull(repo, n, trusted())
		rc := receipt(int64(100+n), verdict.ReasonStaleInputs)
		rc.BaseSHA = oldBase
		w.check(tg, rc, t0.Add(-day))
	}

	w.pass(t0)

	if len(w.pub.published) != 3 || len(w.repos.tipCalls) != 1 {
		t.Errorf("published %v, tip reads %v, want three target_moved and one read", w.reasons(), w.repos.tipCalls)
	}
}

func TestARerunRuleWithATipThatCannotBeRead(t *testing.T) {
	for _, r := range retargetRules {
		t.Run(r.rule+"/an error does what the rule did before", func(t *testing.T) {
			w := newWorld(t)
			repo := w.repo(1, 11, "acme/shop")
			tg := w.pull(repo, 1, r.trusted())
			rc := r.receipt()
			rc.BaseSHA = oldBase
			w.check(tg, rc, r.started)
			w.repos.tipErrs["acme/shop"] = errors.New("boom")

			w.pass(t0)

			if got := w.reasons()[1]; !equalReasons(got, r.wantPubs) || len(w.actions.rerunCalls) != 1 {
				t.Errorf("published %v, reruns %v, want %v and one re-run", got, w.actions.rerunCalls, r.wantPubs)
			}
		})
		t.Run(r.rule+"/a rate limit stops the pass", func(t *testing.T) {
			w := newWorld(t)
			repo := w.repo(1, 11, "acme/shop")
			tg := w.pull(repo, 1, r.trusted())
			rc := r.receipt()
			rc.BaseSHA = oldBase
			w.check(tg, rc, r.started)
			w.repos.tipErrs["acme/shop"] = api.ErrRateLimited

			if sum := w.pass(t0); !sum.RateLimited || w.calls() != (calls{}) {
				t.Errorf("summary = %+v, calls = %+v, want a stop with no write", sum, w.calls())
			}
		})
	}
}

func TestARerunRuleLeavesTheChangeToTheOwningRule(t *testing.T) {
	tests := []struct {
		name  string
		rule  string
		land  func(w *world, tg inputs.Target)
		wants []verdict.ReasonCode
	}{
		{"7c: the inputs went stale meanwhile", "7c stale_inputs", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.PolicyDigests = []string{"p2"}
			w.trust(tg, tr)
		}, nil},
		{"5b: the approval was withdrawn meanwhile", "5b", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.PolicyDigests = []string{"p2"}
			tr.Approval = inputs.ApprovalMissing
			w.trust(tg, tr)
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, r := range retargetRules {
				if r.rule != tt.rule {
					continue
				}
				w := newWorld(t)
				repo := w.repo(1, 11, "acme/shop")
				tg := w.pull(repo, 1, r.trusted())
				rc := r.receipt()
				rc.BaseSHA = oldBase
				w.check(tg, rc, r.started)
				w.pub.waited = func(inputs.Target) { tt.land(w, tg) }

				w.pass(t0)

				if len(w.pub.published) != 0 || len(w.actions.rerunCalls) != 0 {
					t.Errorf("published %v, reruns %v, want nothing: the owning rule decides", w.reasons(), w.actions.rerunCalls)
				}
			}
		})
	}
}

func equalReasons(a, b []verdict.ReasonCode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
