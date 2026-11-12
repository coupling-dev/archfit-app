package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// TestTheCheckIsSetOnlyWhenEveryD13ConditionHolds answers with merge later, then breaks one condition of M2 D13 and
// asks for the check again: none sets archfit/gate.
func TestTheCheckIsSetOnlyWhenEveryD13ConditionHolds(t *testing.T) {
	tests := []struct {
		name  string
		spoil func(w *world, pin *records.Pin)
	}{
		{"the branch is at another commit", func(w *world, _ *records.Pin) {
			w.hub.push(Branch, "notes.md", "a push by someone")
		}},
		{"the head changes another file", func(w *world, pin *records.Pin) {
			head := w.hub.commits[pin.HeadSHA]
			files := maps.Clone(head.files)
			files["notes.md"] = []byte("x")
			pin.HeadSHA = w.hub.put(head.parent, "two files", files)
			w.hub.branches[Branch] = pin.HeadSHA
		}},
		{"the head's policy has another digest", func(_ *world, pin *records.Pin) {
			pin.Digest = policy3.Digest([]byte("version: 3\n"))
		}},
		{"the pull request comes from another repository", func(w *world, pin *records.Pin) {
			w.hub.pulls[pin.PullRequest].HeadRepo = "fork/shop"
		}},
		{"the pull request is into another branch", func(w *world, pin *records.Pin) {
			w.hub.pulls[pin.PullRequest].BaseRef = "release"
		}},
		{"the pull request is closed", func(w *world, pin *records.Pin) {
			w.hub.pulls[pin.PullRequest].State = "closed"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			w.request(431, "graph", "pkg_tc", "pkg_util")
			out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
			p := w.proposalOf(out)
			pin := records.Pin{Proposal: "p", PullRequest: p.PullRequest, HeadSHA: p.HeadSHA, Digest: p.ExpectedDigest}
			checks := len(w.hub.checks)
			if checks != 1 {
				t.Fatalf("the answer set %d checks, want 1", checks)
			}
			tt.spoil(w, &pin)
			err := w.svc.check(context.Background(), testRepo, w.hub.branches[fakeBase], pin, nil)
			if !errors.Is(err, errRefused) {
				t.Errorf("check = %v, want errRefused", err)
			}
			if len(w.hub.checks) != checks {
				t.Errorf("a check was set: %+v", w.hub.checks[checks:])
			}
		})
	}
}

func TestAMergeAfterAFailedCheckIsNotTried(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.hub.errs["CreateCheckRun"] = errors.New("github: 502")
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	if out.State != requests.PullRefused || w.hub.count("MergePull") != 0 {
		t.Errorf("outcome %+v, merges %d", out, w.hub.count("MergePull"))
	}
	if got := w.requestState(431); got != records.StateAnswered {
		t.Errorf("the recorded answer must stand: request %q", got)
	}
}

func TestTheLease(t *testing.T) {
	ctx := context.Background()
	key, ten := records.ProposalLeaseKey(testRepo.FullName), tenant(testRepo)
	held := func(w *world, expires time.Time) {
		raw, _ := json.Marshal(lease{Schema: leaseSchema, Token: "other", Expires: expires})
		if err := w.store.Put(ctx, ten, key, raw); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a held lease makes the answer busy and writes nothing", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		held(w, testNow.Add(time.Minute))
		polls := 0
		w.svc.sleep = func(context.Context, time.Duration) error {
			if polls++; polls > 3 {
				return context.DeadlineExceeded
			}
			return nil
		}
		_, err := w.answer(olivia, 431, requests.Answer{Kind: policy3.Reject, Instead: "No."}, false)
		if !errors.Is(err, ErrBusy) || len(w.hub.writes()) != 0 {
			t.Errorf("err = %v, writes %v", err, w.hub.writes())
		}
		if raw, _ := w.store.Get(ctx, ten, key); !json.Valid(raw) || string(raw) == "" {
			t.Error("the other holder's lease is gone")
		}
	})

	t.Run("an expired lease is taken over and released after the answer", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		held(w, testNow.Add(-time.Second))
		if _, err := w.answer(olivia, 431, requests.Answer{Kind: policy3.Reject, Instead: "No."}, true); err != nil {
			t.Fatal(err)
		}
		if _, err := w.store.Get(ctx, ten, key); err == nil {
			t.Error("the lease is still held after the answer")
		}
	})

	t.Run("a release never deletes another holder's lease", func(t *testing.T) {
		w := newWorld(t)
		held(w, testNow.Add(time.Minute))
		w.svc.release(ctx, key, ten, "mine")
		if _, err := w.store.Get(ctx, ten, key); err != nil {
			t.Errorf("the other holder's lease was deleted: %v", err)
		}
	})
}
