package sweep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	oldBase  = "base0" // the commit a pull request was measured on, before the target moved to tip0
	tipRunID = 900
)

// reportPayload is a decodable report whose seams are exactly the module edges given as "from>to".
func reportPayload(t *testing.T, edges ...string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../wire/testdata/report/healthy.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	template := doc["seams"].([]any)[0].(map[string]any)
	seams := []any{}
	for _, e := range edges {
		from, to, _ := strings.Cut(e, ">")
		s := maps.Clone(template)
		sum := sha256.Sum256([]byte(e))
		s["id"], s["from_module"], s["to_module"] = hex.EncodeToString(sum[:]), from, to
		seams = append(seams, s)
	}
	doc["seams"] = seams
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := report.DecodeArtifact(out, envelope.PayloadDigest(out)); err != nil {
		t.Fatalf("the test report does not decode: %v", err)
	}
	return out
}

// tipReport gives the default-branch tip an archfit/state check, started at startedAt, whose report has the edges.
func (w *world) tipReport(r api.Repo, startedAt time.Time, edges ...string) {
	w.t.Helper()
	payload := reportPayload(w.t, edges...)
	rc := receipt(tipRunID, verdict.ReasonHealthy)
	rc.ReportDigest = envelope.PayloadDigest(payload)
	w.actions.artifacts[tipRunID] = payload
	w.tipCheck(r, startedAt, rc)
}

// tipRejected gives the tip a state check of an upload the App refused (unknown engine identity), whose report has no
// edges at all.
func (w *world) tipRejected(r api.Repo, startedAt time.Time) {
	w.t.Helper()
	payload := reportPayload(w.t)
	rc := receipt(tipRunID, verdict.ReasonUnknownIdentity)
	rc.ReportDigest = envelope.PayloadDigest(payload)
	w.actions.artifacts[tipRunID] = payload
	w.tipCheck(r, startedAt, rc)
}

func (w *world) tipCheck(r api.Repo, startedAt time.Time, rc feedback.Receipt) {
	tip := inputs.Target{Repo: r, HeadSHA: tip0, BaseRef: "main"}
	w.pub.states[checkKey(tip)] = feedback.CheckState{Exists: true, StartedAt: startedAt, Receipt: rc}
}

// passed gives the pull a success check measured on base, whose report has the edges.
func (w *world) passed(tg inputs.Target, runID int64, base string, edges ...string) feedback.Receipt {
	w.t.Helper()
	payload := reportPayload(w.t, edges...)
	rc := receipt(runID, verdict.ReasonHealthy)
	rc.ReportDigest, rc.BaseSHA = envelope.PayloadDigest(payload), base
	w.actions.artifacts[runID] = payload
	w.check(tg, rc, t0.Add(-day))
	return rc
}

// movedWorld is one repository with one pull request that passed on oldBase, and a tip that holds tipEdges.
func movedWorld(t *testing.T, tipEdges, prEdges []string) (*world, inputs.Target, feedback.Receipt) {
	t.Helper()
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	tg := w.pull(r, 1, trusted())
	rc := w.passed(tg, 101, oldBase, prEdges...)
	w.tipReport(r, t0.Add(-time.Minute), tipEdges...)
	return w, tg, rc
}

func TestOverlapStaleness(t *testing.T) {
	tests := []struct {
		name         string
		tip, pr      []string
		wantModules  string // "" = the receipt is kept
		wantInDetail []string
	}{
		{"a pull request closes a cycle with the one merged before it", []string{"a>b"}, []string{"b>a"}, "a, b", nil},
		{"a three-module cycle spread over both reports", []string{"a>b", "b>c"}, []string{"c>a"}, "a, b, c", nil},
		{"a hub with no edge back", []string{"a>h", "c>h"}, []string{"c>h"}, "", nil},
		{"a cycle the target already holds, nothing added", []string{"a>b", "b>a"}, nil, "", nil},
		{"a cycle the target already holds, the same edges again", []string{"a>b", "b>a"}, []string{"a>b"}, "", nil},
		{"an existing cycle that grows", []string{"a>b", "b>a"}, []string{"b>c", "c>a"}, "a, b, c", nil},
		{"a cycle only the pull request's own report holds", []string{"x>y"}, []string{"a>b", "b>a"}, "", nil},
		{"a self edge is no cycle", []string{"a>a"}, []string{"a>a"}, "", nil},
		{"no edges", nil, nil, "", nil},
		{"a second, unrelated cycle beside an existing one", []string{"a>b", "b>a", "c>d"}, []string{"d>c"}, "c, d", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, tg, before := movedWorld(t, tt.tip, tt.pr)

			sum := w.pass(t0)

			if tt.wantModules == "" {
				if w.calls() != (calls{}) || sum.Rerun != 0 {
					t.Fatalf("calls = %+v, summary = %+v, want the receipt kept", w.calls(), sum)
				}
				return
			}
			if got := w.reasons()[1]; !slices.Equal(got, []verdict.ReasonCode{verdict.ReasonTargetMoved}) {
				t.Fatalf("published = %v, want target_moved once", got)
			}
			pub := w.pub.published[0]
			r := pub.Outcome.Reasons[0]
			if pub.Outcome.Conclusion != verdict.ActionRequired || r.Category != verdict.CategoryEvidence {
				t.Errorf("outcome = %+v, want action_required in evidence", pub.Outcome)
			}
			if want := tt.wantModules + "; " + oldBase + ".." + tip0; r.Detail != want {
				t.Errorf("Detail = %q, want %q", r.Detail, want)
			}
			if !strings.Contains(r.Next, "closes a module cycle: "+tt.wantModules+".") || !strings.Contains(r.Next, "Update the branch") {
				t.Errorf("Next = %q, want the cycle and the recovery", r.Next)
			}
			rc := w.state(tg).Receipt
			if rc.RunID != before.RunID || rc.ReportDigest != before.ReportDigest || rc.BaseSHA != oldBase ||
				rc.Conclusion != verdict.ActionRequired || rc.Reason != verdict.ReasonTargetMoved {
				t.Errorf("receipt = %+v, want the run identity and base of %+v with target_moved", rc, before)
			}
			if len(w.actions.rerunCalls) != 0 || sum.Rerun != 0 {
				t.Errorf("re-runs = %v: a re-run reuses the old commit and cannot measure the merge", w.actions.rerunCalls)
			}
			if w.pass(t0.Add(time.Hour)); len(w.pub.published) != 1 {
				t.Errorf("the next pass published again: %d publications", len(w.pub.published))
			}
		})
	}
}

func TestOverlapIsOnlyForPassedRequiredPulls(t *testing.T) {
	cycle := func(w *world, tg inputs.Target, mutate func(*feedback.Receipt, *inputs.Trusted)) {
		rc := w.state(tg).Receipt
		tr := trusted()
		mutate(&rc, &tr)
		w.check(tg, rc, t0.Add(-day))
		w.trust(tg, tr)
	}
	tests := []struct {
		name         string
		mutate       func(*feedback.Receipt, *inputs.Trusted)
		wantTipReads int
		wantArtifact bool
	}{
		{"recheck off", func(_ *feedback.Receipt, tr *inputs.Trusted) {
			tr.Settings.Integration = &settings.Integration{Recheck: settings.RecheckOff}
		}, 0, false},
		{"advisory", func(_ *feedback.Receipt, tr *inputs.Trusted) { tr.Settings.Gate = settings.GateAdvisory }, 0, false},
		{"failure", func(rc *feedback.Receipt, _ *inputs.Trusted) {
			rc.Conclusion, rc.Reason = verdict.Failure, verdict.ReasonBlockingFindings
		}, 0, false},
		{"action_required from the report", func(rc *feedback.Receipt, _ *inputs.Trusted) {
			rc.Conclusion, rc.Reason = verdict.ActionRequired, verdict.ReasonMissingCoverage
		}, 0, false},
		{"base is the tip", func(rc *feedback.Receipt, _ *inputs.Trusted) { rc.BaseSHA = tip0 }, 1, false},
		{"recheck overlap, the default", func(*feedback.Receipt, *inputs.Trusted) {}, 1, true},
		{"recheck overlap, written out", func(_ *feedback.Receipt, tr *inputs.Trusted) {
			tr.Settings.Integration = &settings.Integration{Recheck: settings.RecheckOverlap}
		}, 1, true},
		{"a receipt without a base is checked", func(rc *feedback.Receipt, _ *inputs.Trusted) { rc.BaseSHA = "" }, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, tg, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})
			cycle(w, tg, tt.mutate)

			w.pass(t0)

			if got := len(w.repos.tipCalls); got != tt.wantTipReads {
				t.Errorf("default branch read %d times, want %d", got, tt.wantTipReads)
			}
			if got := len(w.actions.artifactCalls) > 0; got != tt.wantArtifact {
				t.Errorf("artifact calls = %v, want any: %v", w.actions.artifactCalls, tt.wantArtifact)
			}
			if moved := slices.Contains(w.reasons()[1], verdict.ReasonTargetMoved); moved != tt.wantArtifact {
				t.Errorf("published = %v, want target_moved: %v", w.reasons()[1], tt.wantArtifact)
			}
		})
	}
}

func TestOverlapWithoutATipReport(t *testing.T) {
	tests := []struct {
		name      string
		set       func(*world, api.Repo)
		now       time.Time
		wantMoved bool
		wantErrs  int
	}{
		{"no state check on the tip", func(*world, api.Repo) {}, t0, false, 0},
		{"no state check on the tip, a day later", func(*world, api.Repo) {}, t0.Add(day), false, 0},
		{"artifact expired, 59 minutes", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-59*time.Minute))
			delete(w.actions.artifacts, tipRunID)
		}, t0, false, 0},
		{"artifact expired, 60 minutes", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-60*time.Minute))
			delete(w.actions.artifacts, tipRunID)
		}, t0, true, 0},
		{"artifact expired, a day", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-day))
			delete(w.actions.artifacts, tipRunID)
		}, t0, true, 0},
		{"a check that starts in the future reads as run out", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(time.Hour))
			delete(w.actions.artifacts, tipRunID)
		}, t0, true, 0},
		{"artifact is not the report the receipt names, young", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-time.Minute), "a>b")
			w.actions.artifacts[tipRunID] = reportPayload(w.t, "x>y")
		}, t0, false, 0},
		{"artifact is not the report the receipt names, old", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-day), "a>b")
			w.actions.artifacts[tipRunID] = reportPayload(w.t, "x>y")
		}, t0, true, 0},
		{"receipt without a run, old", func(w *world, r api.Repo) {
			w.tipCheck(r, t0.Add(-day), receipt(0, verdict.ReasonMissingUpload))
		}, t0, true, 0},
		{"receipt without a run, young", func(w *world, r api.Repo) {
			w.tipCheck(r, t0.Add(-time.Minute), receipt(0, verdict.ReasonMissingUpload))
		}, t0, false, 0},
		{"artifact read fails", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-day), "a>b")
			w.actions.artifactErrs[tipRunID] = errBoom
		}, t0, false, 1},
		{"artifact is not one file, young", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-time.Minute), "a>b")
			w.actions.artifactErrs[tipRunID] = fmt.Errorf("download: %w", api.ErrArtifactInvalid)
		}, t0, false, 0},
		{"artifact is not one file, old", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-day), "a>b")
			w.actions.artifactErrs[tipRunID] = fmt.Errorf("download: %w", api.ErrArtifactInvalid)
		}, t0, true, 0},
		// The report decodes and its digest matches, but the App rejected the upload: it measures nothing.
		{"report of a rejected upload, young", func(w *world, r api.Repo) {
			w.tipRejected(r, t0.Add(-time.Minute))
		}, t0, false, 0},
		{"report of a rejected upload, old", func(w *world, r api.Repo) {
			w.tipRejected(r, t0.Add(-day))
		}, t0, true, 0},
		{"state check read fails", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-day), "a>b")
			w.pub.errs["AdvisoryState:"+r.FullName] = errBoom
		}, t0, false, 1},
		{"default branch read fails", func(w *world, r api.Repo) {
			w.tipReport(r, t0.Add(-day), "a>b")
			w.repos.tipErrs[r.FullName] = errBoom
		}, t0, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tg := w.pull(r, 1, trusted())
			w.passed(tg, 101, oldBase, "b>a")
			tt.set(w, r)

			sum := w.pass(tt.now)

			if got := slices.Contains(w.reasons()[1], verdict.ReasonTargetMoved); got != tt.wantMoved {
				t.Errorf("published = %v, want target_moved: %v", w.reasons()[1], tt.wantMoved)
			}
			if sum.Errors != tt.wantErrs {
				t.Errorf("Errors = %d, want %d", sum.Errors, tt.wantErrs)
			}
			if tt.wantMoved {
				r := w.pub.published[0].Outcome.Reasons[0]
				if r.Detail != oldBase+".."+tip0 || strings.Contains(r.Next, "module cycle") {
					t.Errorf("reason = %+v, want the range only and no claim of a cycle", r)
				}
			}
		})
	}
}

func TestOverlapWithoutAVerifiablePullReport(t *testing.T) {
	tests := []struct {
		name string
		set  func(w *world, tg inputs.Target, rc feedback.Receipt)
	}{
		{"artifact expired", func(w *world, _ inputs.Target, rc feedback.Receipt) { delete(w.actions.artifacts, rc.RunID) }},
		{"digest mismatch", func(w *world, _ inputs.Target, rc feedback.Receipt) {
			w.actions.artifacts[rc.RunID] = reportPayload(w.t, "x>y")
		}},
		{"bytes that are no report, though the digest matches", func(w *world, tg inputs.Target, rc feedback.Receipt) {
			w.actions.artifacts[rc.RunID] = []byte("{}")
			rc.ReportDigest = envelope.PayloadDigest([]byte("{}"))
			w.check(tg, rc, t0.Add(-day))
		}},
		{"receipt without a digest", func(w *world, tg inputs.Target, rc feedback.Receipt) {
			rc.ReportDigest = ""
			w.check(tg, rc, t0.Add(-day))
		}},
		{"artifact is not one file", func(w *world, _ inputs.Target, rc feedback.Receipt) {
			w.actions.artifactErrs[rc.RunID] = fmt.Errorf("download: %w", api.ErrArtifactInvalid)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Its own edges would close no cycle: only the missing proof makes the receipt stale.
			w, tg, rc := movedWorld(t, []string{"a>b"}, []string{"c>d"})
			tt.set(w, tg, rc)

			w.pass(t0)

			if got := w.reasons()[1]; !slices.Equal(got, []verdict.ReasonCode{verdict.ReasonTargetMoved}) {
				t.Fatalf("published = %v, want target_moved", got)
			}
			if r := w.pub.published[0].Outcome.Reasons[0]; r.Detail != oldBase+".."+tip0 || strings.Contains(r.Next, "module cycle") {
				t.Errorf("reason = %+v, want the range only and no claim of a cycle", r)
			}
		})
	}

	t.Run("a rate limit stops the pass and decides nothing", func(t *testing.T) {
		w, _, rc := movedWorld(t, []string{"a>b"}, []string{"c>d"})
		w.actions.artifactErrs[rc.RunID] = api.ErrRateLimited

		sum := w.pass(t0)

		if !sum.RateLimited || w.calls() != (calls{}) {
			t.Errorf("summary = %+v, calls = %+v, want a stopped pass and no write", sum, w.calls())
		}
	})

	// A GitHub or blob-store failure says nothing about the report: the receipt stays and the next pass reads again.
	t.Run("a failed read decides nothing and is counted", func(t *testing.T) {
		w, tg, rc := movedWorld(t, []string{"a>b"}, []string{"c>d"})
		w.actions.artifactErrs[rc.RunID] = errBoom

		sum := w.pass(t0)

		if sum.Errors != 1 || w.calls() != (calls{}) || w.state(tg).Receipt != rc {
			t.Errorf("summary = %+v, calls = %+v, want one error and the receipt kept", sum, w.calls())
		}
	})
}

func TestOverlapReadsTheTargetOncePerPass(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	for n := 1; n <= 3; n++ {
		tg := w.pull(r, n, trusted())
		w.passed(tg, int64(100+n), oldBase, string(rune('b'+n))+">h")
	}
	w.tipReport(r, t0.Add(-time.Minute), "a>h")

	w.pass(t0)

	if len(w.repos.tipCalls) != 1 || w.pub.advisoryCalls != 1 {
		t.Errorf("default branch read %d times, tip check %d times, want once each", len(w.repos.tipCalls), w.pub.advisoryCalls)
	}
	if want := []int64{tipRunID, 101, 102, 103}; !slices.Equal(w.actions.artifactCalls, want) {
		t.Fatalf("artifacts read = %v, want %v", w.actions.artifactCalls, want)
	}
	if w.pass(t0.Add(time.Hour)); len(w.actions.artifactCalls) != 4 {
		t.Errorf("the next pass read artifacts again: %v; a verified report never changes", w.actions.artifactCalls)
	}
	if len(w.pub.published) != 0 {
		t.Errorf("published %d, want the receipts kept", len(w.pub.published))
	}
}

func TestRecheckAfterAPush(t *testing.T) {
	recheck := func(w *world, r api.Repo, tip string) (Summary, error) {
		w.pub.now = t0
		return w.sweeper.Recheck(context.Background(), r, tip, t0)
	}

	t.Run("runs rule 5c for the pushed tip", func(t *testing.T) {
		w, tg, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})

		sum, err := recheck(w, w.repos.repos[1][0], tip0)

		if err != nil || sum.Repos != 1 || sum.Pulls != 1 {
			t.Fatalf("summary = %+v, err = %v", sum, err)
		}
		if got := w.state(tg).Receipt.Reason; got != verdict.ReasonTargetMoved {
			t.Errorf("reason = %q, want target_moved", got)
		}
	})

	t.Run("a branch that moved on is left to the push that moved it", func(t *testing.T) {
		w, _, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})

		sum, err := recheck(w, w.repos.repos[1][0], "an older push")

		if err != nil || sum.Repos != 0 || w.calls() != (calls{}) || len(w.resolver.calls) != 0 || len(w.actions.artifactCalls) != 0 {
			t.Errorf("summary = %+v, err = %v, calls = %+v, resolved %d, artifacts %v; want nothing done",
				sum, err, w.calls(), len(w.resolver.calls), w.actions.artifactCalls)
		}
	})

	t.Run("a branch that is gone decides nothing", func(t *testing.T) {
		w, _, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})
		w.repos.tipErrs["acme/shop"] = api.ErrNotFound

		if _, err := recheck(w, w.repos.repos[1][0], tip0); err != nil || w.calls() != (calls{}) {
			t.Errorf("err = %v, calls = %+v", err, w.calls())
		}
	})

	t.Run("a rate limit is returned so the push is retried", func(t *testing.T) {
		w, _, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})
		w.repos.tipErrs["acme/shop"] = api.ErrRateLimited

		sum, err := recheck(w, w.repos.repos[1][0], tip0)

		if !errors.Is(err, api.ErrRateLimited) || !sum.RateLimited {
			t.Errorf("summary = %+v, err = %v, want the rate limit", sum, err)
		}
	})

	// Another rule owns each of these; target_moved over them would hide the reason that rule publishes.
	others := []struct {
		name string
		set  func(w *world, tg inputs.Target, rc feedback.Receipt)
	}{
		{"stale inputs", func(w *world, tg inputs.Target, rc feedback.Receipt) {
			rc.PolicyDigest = "old"
			w.check(tg, rc, t0.Add(-day))
		}},
		{"a withdrawn approval", func(w *world, tg inputs.Target, _ feedback.Receipt) {
			tr := trusted()
			tr.Approval = inputs.ApprovalMissing
			w.trust(tg, tr)
		}},
		{"a suspension", func(w *world, tg inputs.Target, _ feedback.Receipt) {
			tr := trusted()
			tr.Suspended = true
			w.trust(tg, tr)
		}},
		{"a pending check", func(w *world, tg inputs.Target, _ feedback.Receipt) { w.pendingCheck(tg, t0.Add(-time.Minute)) }},
		{"no check", func(w *world, tg inputs.Target, _ feedback.Receipt) { delete(w.pub.states, checkKey(tg)) }},
		{"advisory mode", func(w *world, tg inputs.Target, _ feedback.Receipt) {
			tr := trusted()
			tr.Settings.Gate = settings.GateAdvisory
			w.trust(tg, tr)
		}},
	}
	for _, tt := range others {
		t.Run("leaves "+tt.name+" to the full pass", func(t *testing.T) {
			w, tg, rc := movedWorld(t, []string{"a>b"}, []string{"b>a"})
			tt.set(w, tg, rc)

			if _, err := recheck(w, w.repos.repos[1][0], tip0); err != nil {
				t.Fatal(err)
			}

			if w.calls() != (calls{}) {
				t.Errorf("calls = %+v, want none: the recheck only runs rule 5c", w.calls())
			}
			if w.pub.advisoryCalls != 0 || len(w.actions.artifactCalls) != 0 {
				t.Errorf("the tip check was read %d times and artifacts %v: nothing is left for rule 5c to decide", w.pub.advisoryCalls, w.actions.artifactCalls)
			}
		})
	}

	t.Run("a fork is left alone", func(t *testing.T) {
		w := newWorld(t)
		r := w.repo(1, 11, "acme/shop")
		tg := w.pullWith(r, api.Pull{Number: 1, HeadRepo: "stranger/shop", BaseRef: "main"}, trusted())
		w.passed(tg, 101, oldBase, "b>a")
		w.tipReport(r, t0.Add(-time.Minute), "a>b")

		if _, err := recheck(w, r, tip0); err != nil || w.calls() != (calls{}) {
			t.Errorf("err = %v, calls = %+v, want none", err, w.calls())
		}
	})
}

func TestTargetMovedIsNotRepairedByOtherRules(t *testing.T) {
	e := t0.Add(-day)
	tests := []struct {
		name   string
		expiry time.Time
	}{
		{"a waiver expired after the check was written", e},
		{"no waiver", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			r := w.repo(1, 11, "acme/shop")
			tr := trusted()
			tr.EarliestWaiverExpiry = t0.Add(30 * day) // the next waiver, still valid
			tg := w.pull(r, 1, tr)
			rc := receipt(101, verdict.ReasonTargetMoved)
			rc.BaseSHA, rc.EarliestWaiverExpiry = oldBase, tt.expiry
			w.check(tg, rc, tt.expiry.Add(-time.Hour))

			w.pass(t0)

			if w.calls() != (calls{}) {
				t.Errorf("calls = %+v, want none: only a new head measures the merge", w.calls())
			}
		})
	}
}

func TestTargetMovedDecidesInsideTheLease(t *testing.T) {
	tests := []struct {
		name string
		land func(w *world, tg inputs.Target)
		err  bool
	}{
		{"a newer run replaced the receipt", func(w *world, tg inputs.Target) {
			rc := receipt(102, verdict.ReasonHealthy)
			rc.BaseSHA = tip0
			w.check(tg, rc, t0)
		}, false},
		{"the policy changed", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.PolicyDigests = []string{"p2"}
			w.trust(tg, tr)
		}, false},
		{"the approval was withdrawn", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.Approval = inputs.ApprovalMissing
			w.trust(tg, tr)
		}, false},
		{"recheck was switched off", func(w *world, tg inputs.Target) {
			tr := trusted()
			tr.Settings.Integration = &settings.Integration{Recheck: settings.RecheckOff}
			w.trust(tg, tr)
		}, false},
		{"the inputs cannot be read", func(w *world, tg inputs.Target) { w.resolver.errs[pullKey(tg)] = errBoom }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, tg, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})
			// The change lands while the sweep waits for the lease, after its own read of the pull.
			w.pub.waited = func(inputs.Target) { tt.land(w, tg) }

			sum := w.pass(t0)

			if len(w.pub.published) != 0 {
				t.Errorf("published %v, want nothing: the decision is made again inside the lease", w.reasons())
			}
			if (sum.Errors == 1) != tt.err {
				t.Errorf("Errors = %d, want an error: %v", sum.Errors, tt.err)
			}
		})
	}
}

func TestTargetMovedFailsClosedOnReadErrors(t *testing.T) {
	t.Run("a rate limit on the default branch stops the pass", func(t *testing.T) {
		w, _, _ := movedWorld(t, []string{"a>b"}, []string{"b>a"})
		w.repos.tipErrs["acme/shop"] = api.ErrRateLimited

		if sum := w.pass(t0); !sum.RateLimited || w.calls() != (calls{}) {
			t.Errorf("summary = %+v, calls = %+v", sum, w.calls())
		}
	})
	t.Run("a failure to publish is counted and the receipt stays", func(t *testing.T) {
		w, tg, rc := movedWorld(t, []string{"a>b"}, []string{"b>a"})
		w.pub.errs["Reconcile:acme/shop"] = errBoom

		if sum := w.pass(t0); sum.Errors != 1 || w.state(tg).Receipt != rc {
			t.Errorf("summary = %+v, receipt = %+v", sum, w.state(tg).Receipt)
		}
	})
}

func TestEdges(t *testing.T) {
	got := edgesOf([]report.Seam{
		{FromModule: "a", ToModule: "b"}, {FromModule: "b", ToModule: "b"}, {FromModule: "", ToModule: "b"}, {FromModule: "a", ToModule: ""},
	})
	if want := []edge{{"a", "b"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("edgesOf = %v, want %v: a seam within one module or without a module is no edge", got, want)
	}
}

func TestClosedCycle(t *testing.T) {
	e := func(pairs ...string) []edge {
		var out []edge
		for _, p := range pairs {
			from, to, _ := strings.Cut(p, ">")
			out = append(out, edge{from, to})
		}
		return out
	}
	tests := []struct {
		name    string
		tip, pr []edge
		want    []string
	}{
		{"two-cycle across the reports", e("a>b"), e("b>a"), []string{"a", "b"}},
		{"three-cycle across the reports", e("a>b", "b>c"), e("c>a"), []string{"a", "b", "c"}},
		{"three-cycle, two edges in the pull request", e("a>b"), e("b>c", "c>a"), []string{"a", "b", "c"}},
		{"hub with no edge back", e("a>h", "c>h"), e("c>h"), nil},
		{"hub, the pull request depends on a module that depends on the hub", e("a>h", "h>x"), e("c>a"), nil},
		{"existing cycle, unchanged", e("a>b", "b>a"), nil, nil},
		{"existing cycle, repeated in the pull request", e("a>b", "b>a"), e("a>b", "b>a"), nil},
		{"existing cycle, grown", e("a>b", "b>a"), e("b>c", "c>a"), []string{"a", "b", "c"}},
		{"existing cycle, grown by a module that only the target reaches", e("a>b", "b>a", "b>c"), e("c>a"), []string{"a", "b", "c"}},
		{"cycle only in the pull request", e("x>y"), e("a>b", "b>a"), nil},
		{"cycle only in the target", e("a>b", "b>a"), e("x>y"), nil},
		{"two new cycles are both named", e("a>b", "c>d"), e("b>a", "d>c"), []string{"a", "b", "c", "d"}},
		{"a chain is no cycle", e("a>b"), e("b>c"), nil},
		{"no edges", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := closedCycle(tt.tip, tt.pr); !slices.Equal(got, tt.want) {
				t.Errorf("closedCycle = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCycles(t *testing.T) {
	got := cycles([]edge{{"c", "a"}, {"a", "b"}, {"b", "a"}, {"c", "d"}, {"d", "e"}, {"e", "d"}, {"x", "x"}})
	if want := [][]string{{"a", "b"}, {"d", "e"}}; !reflect.DeepEqual(sortedComponents(got), want) {
		t.Errorf("cycles = %v, want %v", got, want)
	}
	if got := cycles(nil); len(got) != 0 {
		t.Errorf("cycles(nil) = %v", got)
	}
}

func sortedComponents(cs [][]string) [][]string {
	out := slices.Clone(cs)
	slices.SortFunc(out, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	return out
}

func TestEdgeCache(t *testing.T) {
	var c edgeCache
	repo := api.RepoRef{InstallationID: 1, FullName: "acme/shop"}
	c.put(edgeKey{repo, "d0"}, []edge{{"a", "b"}})
	if got, ok := c.get(edgeKey{repo, "d0"}); !ok || !reflect.DeepEqual(got, []edge{{"a", "b"}}) {
		t.Fatalf("get = %v, %v", got, ok)
	}
	if _, ok := c.get(edgeKey{api.RepoRef{InstallationID: 1, FullName: "acme/other"}, "d0"}); ok {
		t.Error("another repository read an entry it never verified")
	}
	for i := range edgeCacheSize {
		c.put(edgeKey{repo, "n" + strconv.Itoa(i)}, nil)
	}
	if _, ok := c.get(edgeKey{repo, "d0"}); ok {
		t.Error("the oldest entry survived a full cache")
	}
	if len(c.byKey) != edgeCacheSize {
		t.Errorf("cache holds %d entries, want at most %d", len(c.byKey), edgeCacheSize)
	}
}
