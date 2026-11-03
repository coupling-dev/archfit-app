package gate

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"

	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestMirrorFailureCannotKeepPullOrQueueApprovalGreen(t *testing.T) {
	for _, trigger := range []string{"App refresh", "review webhook"} {
		t.Run(trigger, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 1)
			groups := w.enqueue(p)
			w.changePolicyIn(p, loosenedPolicy, groups...)
			w.approve(p, ownerLogin)
			digest := policy.Digest([]byte(loosenedPolicy))
			w.wantAnswer(w.upload(p, fixtureHealthy, configHash(digest)), http.StatusOK, "success", "healthy")
			w.wantAnswer(w.uploadGroup(groups[0], fixtureHealthy, configHash(digest)), http.StatusOK, "success", "healthy")
			shown := w.pullApproval(p, ownerLogin)
			if err := w.gate.Decide(t.Context(), w.target(p), ownerLogin, shown.Digests, DecisionRequestChanges, "Reconsider this policy.", nil); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("GitHub comment service unavailable")
			w.gh.fail["CreateComment"] = failure
			var err error
			if trigger == "App refresh" {
				err = w.gate.Reevaluate(t.Context(), w.target(p))
			} else {
				err = w.gate.HandleEvent(t.Context(), w.event(webhook.ReviewChanged, p))
			}
			if !errors.Is(err, failure) {
				t.Fatalf("refresh error = %v, want mirror failure", err)
			}
			w.wantGate(p, runCompleted, "action_required")
			w.wantGroupGate(groups[0], "action_required")
		})
	}
}

type countedReviewerRequests struct {
	inputs.Resolver
	requests int
}

func (r *countedReviewerRequests) RequestEligibleReviewers(ctx context.Context, target inputs.Target, author string, now time.Time) bool {
	r.requests++
	return r.Resolver.RequestEligibleReviewers(ctx, target, author, now)
}

func TestReadingApprovalDoesNotSendReviewerRequests(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 1)
	w.editsPolicy(p)
	r := &countedReviewerRequests{Resolver: w.gate.resolver}
	w.gate.resolver = r
	w.pullApproval(p, ownerLogin)
	if r.requests != 0 {
		t.Fatal("reading approval requested reviewers")
	}
}

func TestForkApprovalReportsARefusal(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 1)
	w.editsPolicy(p)
	target := w.target(p)
	target.Fork = true
	got, err := w.gate.PullApproval(t.Context(), target, "not-an-owner", "contributor")
	if err != nil || !errors.Is(got.Refusal, ErrForkPull) {
		t.Fatalf("fork eligibility=%+v,%v", got, err)
	}
}
