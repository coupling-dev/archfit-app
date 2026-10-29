package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

// shareHead opens a pull request that has p's head commit and the given base: the pull request an agent opens from
// the same feature branch into a base it controls.
func (w *world) shareHead(p *pull, number int, base string) *pull {
	q := w.openPull(w.shop, number)
	w.editPull(q, func(pl *api.Pull) { pl.HeadSHA, pl.BaseRef = p.head, base })
	return q
}

// A check is written per head commit, so a pull request into another base on the same head shows the result of the
// pull request into the default branch. No success is written while such a pull request is open.
func TestSharedHeadNeverSucceeds(t *testing.T) {
	t.Run("the other pull request is open at the upload", func(t *testing.T) {
		w := newWorld(t)
		b := w.openPull(w.shop, 7)
		w.changes(b, "pkg/a/a.go", ".archfit-app.yaml")
		w.handle(w.event(webhook.PullOpened, b))
		w.review(b, ownerLogin, "APPROVED", b.head)
		w.shareHead(b, 8, "loosened")

		w.wantAnswer(w.upload(b, fixtureHealthy), 200, "action_required", "shared_head")

		w.wantGate(b, runCompleted, "action_required")
		if rc := w.receipt(b); rc.Reason != "shared_head" {
			t.Errorf("receipt reason = %q, want shared_head", rc.Reason)
		}
		if text := w.feedbackText(b); !strings.Contains(text, "#8 into loosened") {
			t.Errorf("feedback does not name the other pull request:\n%s", text)
		}
		w.noSuccess()
	})

	t.Run("a failed listing is retried and never a success", func(t *testing.T) {
		for name, tc := range map[string]struct {
			err        error
			wantStatus int
			wantError  string
		}{
			"unavailable":  {errors.New("github down"), 503, "unavailable"},
			"rate limited": {api.ErrRateLimited, 503, "rate_limited"},
		} {
			t.Run(name, func(t *testing.T) {
				w := newWorld(t)
				b := w.openPull(w.shop, 7)
				w.handle(w.event(webhook.PullOpened, b))
				w.gh.fail["OpenPulls"] = tc.err

				a := w.upload(b, fixtureHealthy)

				if a.status != tc.wantStatus || a.Error != tc.wantError {
					t.Errorf("answer = %d %s, want %d %s", a.status, a.raw, tc.wantStatus, tc.wantError)
				}
				w.wantGate(b, "in_progress", "")
				w.noSuccess()

				delete(w.gh.fail, "OpenPulls")
				w.wantAnswer(w.upload(b, fixtureHealthy), 200, "success", "healthy")
			})
		}
	})
}

// Pull requests that do not share the head, or share it into the default branch, change nothing.
func TestSharedHeadIgnoresOtherPulls(t *testing.T) {
	tests := []struct {
		name  string
		other func(w *world, b *pull)
	}{
		{"another head into another base", func(w *world, _ *pull) {
			q := w.openPull(w.shop, 8)
			w.editPull(q, func(pl *api.Pull) { pl.BaseRef = "loosened" })
		}},
		{"the same head into the default branch", func(w *world, b *pull) { w.shareHead(b, 8, testBranch) }},
		{"the same head into another base, closed", func(w *world, b *pull) {
			q := w.shareHead(b, 8, "loosened")
			w.editPull(q, func(pl *api.Pull) { pl.State = "closed" })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			b := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, b))
			tt.other(w, b)

			w.wantAnswer(w.upload(b, fixtureHealthy), 200, "success", "healthy")
			if sum, err := w.gate.Sweep(context.Background()); err != nil || sum.Errors != 0 {
				t.Fatalf("Sweep = %+v, %v", sum, err)
			}
			w.wantGate(b, runCompleted, "success")
		})
	}
}

// A success that exists when a pull request into another base appears on its head is withdrawn at once by the event,
// or by the sweep when the event is lost. Nothing re-runs the workflow; closing the other pull request does not
// bring the success back, the author's re-run does.
func TestSharedHeadWithdrawsAnExistingSuccess(t *testing.T) {
	triggers := map[string]func(w *world, other *pull){
		"the other pull request opened": func(w *world, other *pull) { w.handle(w.event(webhook.PullOpened, other)) },
		"the other pull request pushed": func(w *world, other *pull) { w.handle(w.event(webhook.PullSynchronized, other)) },
		"the event is lost and the hourly sweep runs": func(w *world, _ *pull) {
			if sum, err := w.gate.Sweep(context.Background()); err != nil || sum.Errors != 0 {
				w.t.Fatalf("Sweep = %+v, %v", sum, err)
			}
		},
	}
	for name, trigger := range triggers {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			b, s := w.approvedSettingsEdit()
			other := w.shareHead(b, 8, "loosened")

			trigger(w, other)

			w.wantGate(b, runCompleted, "action_required")
			if rc := w.receipt(b); rc.Reason != "shared_head" {
				t.Fatalf("receipt reason = %q, want shared_head", rc.Reason)
			}
			if len(w.gh.reruns) != 0 {
				t.Errorf("re-runs = %v, want none", w.gh.reruns)
			}

			w.editPull(other, func(pl *api.Pull) { pl.State = "closed" })
			if sum, err := w.gate.Sweep(context.Background()); err != nil || sum.Errors != 0 || sum.Rerun != 0 {
				t.Fatalf("Sweep = %+v, %v", sum, err)
			}
			w.wantGate(b, runCompleted, "action_required")

			w.wantAnswer(w.send(s), 200, "success", "healthy")
			w.wantGate(b, runCompleted, "success")
		})
	}
}

// A closed pull request into another base shares nothing: its event leaves the success alone.
func TestSharedHeadClosedPullLeavesTheSuccess(t *testing.T) {
	w := newWorld(t)
	b, _ := w.approvedSettingsEdit()
	other := w.shareHead(b, 8, "loosened")
	w.editPull(other, func(pl *api.Pull) { pl.State = "closed" })

	if err := w.gate.HandleEvent(context.Background(), w.event(webhook.PullOpened, other)); err != nil {
		t.Fatal(err)
	}

	w.wantGate(b, runCompleted, "success")
	if !slices.Equal(w.gh.reruns, nil) {
		t.Errorf("re-runs = %v", w.gh.reruns)
	}
}

// The listing of the upload is repeated inside the publication lease. A pull request into another base that opens
// after the first listing is invisible to the decision, and its event finds the check pending or already withdrawn: the
// lease is the last place that can keep the upload from writing a success.
func TestSharedHeadOpenedWhileTheUploadWaitsForTheLease(t *testing.T) {
	t.Run("a pull request opens before the lease", func(t *testing.T) {
		w := newWorld(t)
		b := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, b))
		w.store.beforeLease = func() { w.shareHead(b, 8, "loosened") }

		a := w.upload(b, fixtureHealthy)

		if a.status != 503 || a.Error != "unavailable" {
			t.Fatalf("answer = %d %s, want 503 unavailable", a.status, a.raw)
		}
		w.wantGate(b, "in_progress", "")
		w.noSuccess()
		w.wantAnswer(w.upload(b, fixtureHealthy), 200, "action_required", "shared_head")
		w.noSuccess()
	})

	t.Run("a withdrawal lands before the lease of a stale upload of the same run", func(t *testing.T) {
		w := newWorld(t)
		b, s := w.approvedSettingsEdit()
		writes := w.successWrites()
		w.store.beforeLease = func() {
			other := w.shareHead(b, 8, "loosened")
			w.handle(w.event(webhook.PullOpened, other))
		}

		a := w.send(s)

		if a.status != 503 {
			t.Fatalf("answer = %d %s, want 503", a.status, a.raw)
		}
		w.wantGate(b, runCompleted, "action_required")
		if rc := w.receipt(b); rc.Reason != "shared_head" || w.successWrites() != writes {
			t.Errorf("receipt reason = %q, success writes %d -> %d; want shared_head and no new success", rc.Reason, writes, w.successWrites())
		}
	})

	t.Run("the listing inside the lease fails", func(t *testing.T) {
		w := newWorld(t)
		b := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, b))
		w.store.beforeLease = func() { w.gh.fail["OpenPulls"] = errors.New("github down") }

		a := w.upload(b, fixtureHealthy)

		if a.status != 503 {
			t.Fatalf("answer = %d %s, want 503", a.status, a.raw)
		}
		w.wantGate(b, "in_progress", "")
		w.noSuccess()
	})
}

// RunRepo counts a unit it could not read and returns nil: the event must still fail, so the queue retries it and the
// success is withdrawn once GitHub answers.
func TestSharedHeadEventRetriesAFailedSweep(t *testing.T) {
	w := newWorld(t)
	b, _ := w.approvedSettingsEdit()
	other := w.shareHead(b, 8, "loosened")
	w.gh.fail["OpenPulls"] = errors.New("github down")

	if err := w.gate.HandleEvent(context.Background(), w.event(webhook.PullOpened, other)); err == nil {
		t.Fatal("HandleEvent = nil, want an error so the delivery is retried")
	}

	delete(w.gh.fail, "OpenPulls")
	w.handle(w.event(webhook.PullOpened, other))
	w.wantGate(b, runCompleted, "action_required")
}
