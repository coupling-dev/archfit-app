package sweep

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// eachRuleWorld is one repository with one pull per rule row, plus a healthy one. The run of PR n is 100+n.
func eachRuleWorld(t *testing.T, now time.Time, fullName, baseRef string) (*world, api.Repo) {
	t.Helper()
	w := newWorld(t)
	r := w.repo(1, 11, fullName)
	open := func(n int, tr inputs.Trusted) inputs.Target {
		return w.pullWith(r, api.Pull{Number: n, HeadRepo: fullName, BaseRef: baseRef}, tr)
	}
	healthy := func(n int) feedback.Receipt { return receipt(int64(100+n), verdict.ReasonHealthy) }

	w.pullWith(r, api.Pull{Number: 1, HeadRepo: "stranger/fork", BaseRef: baseRef}, trusted())

	suspended := trusted()
	suspended.Suspended = true
	w.check(open(2, suspended), healthy(2), now.Add(-day))

	open(3, trusted())
	w.pendingCheck(open(4, trusted()), now.Add(-59*time.Minute))
	w.pendingCheck(open(5, trusted()), now.Add(-60*time.Minute))

	stalePolicy := healthy(6)
	stalePolicy.PolicyDigest = "old"
	w.check(open(6, trusted()), stalePolicy, now.Add(-day))

	withBaseline := trusted()
	withBaseline.BaselineDigests = []string{"b1"}
	staleBaseline := healthy(7)
	staleBaseline.BaselineDigest = "b0"
	w.check(open(7, withBaseline), staleBaseline, now.Add(-day))

	withLabels := trusted()
	withLabels.LabelsDigests = []string{"l1"}
	w.check(open(8, withLabels), healthy(8), now.Add(-day)) // the receipt predates the labels file

	expiring := trusted()
	expiring.EarliestWaiverExpiry = now.Add(3 * day)
	soon := healthy(9)
	soon.EarliestWaiverExpiry = now.Add(3 * day)
	w.check(open(9, expiring), soon, now.Add(-day))

	expired := healthy(10)
	expired.EarliestWaiverExpiry = now.Add(-time.Hour)
	w.check(open(10, trusted()), expired, now.Add(-2*time.Hour))

	w.check(open(11, trusted()), receipt(111, verdict.ReasonSuspended), now.Add(-day))

	approved := trusted()
	approved.Approval = inputs.ApprovalGranted
	approved.PolicyDigests = []string{"p1", "p1-head"}
	w.check(open(12, approved), receipt(112, verdict.ReasonOwnerApprovalMissing), now.Add(-day))

	w.check(open(13, trusted()), healthy(13), now.Add(-day))
	return w, r
}

func TestRunRepairsEachRule(t *testing.T) {
	w, r := eachRuleWorld(t, t0, "acme/shop", "main")

	sum := w.pass(t0)

	want := Summary{Repos: 1, Pulls: 13, Pended: 1, TimedOut: 1, Rerun: 6, Warned: 1, Suspended: 1}
	if sum != want {
		t.Errorf("summary = %+v, want %+v", sum, want)
	}
	wantReasons := map[int][]verdict.ReasonCode{
		1:  {verdict.ReasonForkUnsupported},
		2:  {verdict.ReasonSuspended},
		5:  {verdict.ReasonMissingUpload},
		6:  {verdict.ReasonStaleInputs},
		7:  {verdict.ReasonStaleInputs},
		8:  {verdict.ReasonStaleInputs},
		10: {verdict.ReasonWaiverExpired},
	}
	if got := w.reasons(); !reflect.DeepEqual(got, wantReasons) {
		t.Errorf("published = %v, want %v", got, wantReasons)
	}
	if len(w.pub.pendings) != 1 || w.pub.pendings[0].PullRequest != 3 {
		t.Errorf("pendings = %+v, want PR 3 only", w.pub.pendings)
	}
	if len(w.pub.warnings) != 1 || w.pub.warnings[0].target.PullRequest != 9 || !w.pub.warnings[0].expiry.Equal(t0.Add(3*day)) {
		t.Errorf("warnings = %+v, want PR 9 at the expiry", w.pub.warnings)
	}
	reruns := slices.Sorted(slices.Values(w.actions.rerunCalls))
	if !slices.Equal(reruns, []int64{106, 107, 108, 110, 111, 112}) {
		t.Errorf("re-runs = %v", reruns)
	}

	fork := w.resolver.calls[0]
	wantFork := inputs.Target{Repo: r, PullRequest: 1, HeadSHA: "sha11-1", BaseRef: "main", Fork: true}
	if fork != wantFork {
		t.Errorf("fork target = %+v, want %+v", fork, wantFork)
	}
	for i, tg := range w.resolver.calls[1:] {
		if tg.Fork || !w.resolver.nows[i+1].Equal(t0) {
			t.Errorf("target %+v resolved at %v: want no fork, resolved at now", tg, w.resolver.nows[i+1])
		}
	}
}

func TestSecondPassIsQuiet(t *testing.T) {
	w, _ := eachRuleWorld(t, t0, "acme/shop", "main")
	w.pass(t0)
	before := w.calls()
	firstWarnings := slices.Clone(w.pub.warnings)

	sum := w.pass(t0)

	after := w.calls()
	if after.published != before.published || after.pendings != before.pendings || after.reruns != before.reruns {
		t.Errorf("second pass calls = %+v, first pass = %+v", after, before)
	}
	for _, warn := range w.pub.warnings[before.warnings:] {
		if !slices.ContainsFunc(firstWarnings, func(f warning) bool {
			return f.target == warn.target && f.expiry.Equal(warn.expiry)
		}) {
			t.Errorf("second pass warned PR %d with a new date %v", warn.target.PullRequest, warn.expiry)
		}
	}
	want := Summary{Repos: 1, Pulls: 13, Warned: 1, Suspended: 1}
	if sum != want {
		t.Errorf("summary = %+v, want %+v", sum, want)
	}
}

func TestFailedRerunIsRetriedOnce(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	tg := w.pull(r, 1, trusted())
	rc := receipt(9, verdict.ReasonHealthy)
	rc.PolicyDigest = "old"
	w.check(tg, rc, t0.Add(-day))
	w.actions.rerunErrs[9] = errBoom

	sum := w.pass(t0)
	if sum.Errors != 1 || sum.Rerun != 0 {
		t.Errorf("first pass summary = %+v, want Errors 1 and Rerun 0", sum)
	}
	if got := w.state(tg).Receipt.Reason; got != verdict.ReasonStaleInputs {
		t.Fatalf("reason = %q, want the stale_inputs publication kept", got)
	}

	delete(w.actions.rerunErrs, 9)
	sum = w.pass(t0.Add(time.Hour))
	if got := w.calls(); got != (calls{published: 1, reruns: 2}) {
		t.Errorf("after the second pass calls = %+v, want 1 publication and 2 re-run requests", got)
	}
	if sum.Rerun != 1 || sum.Errors != 0 {
		t.Errorf("second pass summary = %+v, want Rerun 1", sum)
	}

	before := w.calls()
	sum = w.pass(t0.Add(2 * time.Hour))
	if got := w.calls(); got != before || sum.Rerun != 0 {
		t.Errorf("third pass calls = %+v (before %+v), summary %+v", got, before, sum)
	}
}

func TestAdvisoryRepositories(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	advisory := trusted()
	advisory.Settings.Gate = settings.GateAdvisory
	advisory.EarliestWaiverExpiry = t0.Add(2 * day)
	tg := w.pullWith(r, api.Pull{Number: 1, HeadRepo: "stranger/fork", BaseRef: "main"}, advisory)

	sum := w.pass(t0)

	if !reflect.DeepEqual(w.pub.warnings, []warning{{tg, t0.Add(2 * day)}}) {
		t.Errorf("warnings = %+v, want one", w.pub.warnings)
	}
	if w.pub.stateCalls != 0 || len(w.pub.pendings) != 0 || len(w.pub.published) != 0 || len(w.actions.runCalls) != 0 {
		t.Errorf("state calls %d, pendings %d, published %d, run calls %d: want none",
			w.pub.stateCalls, len(w.pub.pendings), len(w.pub.published), len(w.actions.runCalls))
	}
	if want := (Summary{Repos: 1, Pulls: 1, Warned: 1}); sum != want {
		t.Errorf("summary = %+v, want %+v", sum, want)
	}

	t.Run("default gate mode is advisory", func(t *testing.T) {
		w := newWorld(t)
		unset := trusted()
		unset.Settings.Gate = ""
		w.pull(w.repo(1, 11, "acme/shop"), 1, unset)
		w.pass(t0)
		if got := w.calls(); got != (calls{}) || w.pub.stateCalls != 0 {
			t.Errorf("calls = %+v, state calls = %d, want none", got, w.pub.stateCalls)
		}
	})
}

func TestNotOnboardedIsSkipped(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	w.pull(r, 1, inputs.Trusted{})
	w.pull(r, 2, inputs.Trusted{})
	w.pullWith(r, api.Pull{Number: 3, HeadRepo: r.FullName, BaseRef: "release"}, inputs.Trusted{})
	other := w.repo(1, 12, "acme/blog") // same base name, another repository
	w.pull(other, 4, inputs.Trusted{})

	sum := w.pass(t0)

	var resolved []int
	for _, tg := range w.resolver.calls {
		resolved = append(resolved, tg.PullRequest)
	}
	if !slices.Equal(resolved, []int{1, 3, 4}) {
		t.Errorf("resolved pulls %v, want one per (repository, base): [1 3 4]", resolved)
	}
	if got := w.calls(); got != (calls{}) || w.pub.stateCalls != 0 {
		t.Errorf("publisher calls = %+v, state calls = %d, want none", got, w.pub.stateCalls)
	}
	if want := (Summary{Repos: 2}); sum != want {
		t.Errorf("summary = %+v, want %+v", sum, want)
	}
}

func TestRunRepoIsTheSamePassForOneRepository(t *testing.T) {
	w := newWorld(t)
	first, second := w.repo(1, 11, "acme/shop"), w.repo(1, 12, "acme/blog")
	for i, r := range []api.Repo{first, second} {
		rc := receipt(int64(201+i), verdict.ReasonHealthy)
		rc.PolicyDigest = "old"
		w.check(w.pull(r, i+1, trusted()), rc, t0.Add(-day))
	}

	// The caller builds the repository from a webhook: only RepoRef and Private are set.
	sum := w.passRepo(api.Repo{RepoRef: first.RepoRef}, t0)

	if want := (Summary{Repos: 1, Pulls: 1, Rerun: 1}); sum != want {
		t.Errorf("RunRepo summary = %+v, want %+v", sum, want)
	}
	if w.repos.listInstCalls != 0 || len(w.repos.listRepos) != 0 {
		t.Errorf("RunRepo listed installations %d times and repositories %v", w.repos.listInstCalls, w.repos.listRepos)
	}
	if got := w.reasons(); !reflect.DeepEqual(got, map[int][]verdict.ReasonCode{1: {verdict.ReasonStaleInputs}}) {
		t.Errorf("RunRepo published %v, want PR 1 only", got)
	}

	sum = w.pass(t0.Add(time.Minute))

	if want := (Summary{Repos: 2, Pulls: 2, Rerun: 1}); sum != want {
		t.Errorf("Run summary = %+v, want %+v", sum, want)
	}
	wantReasons := map[int][]verdict.ReasonCode{1: {verdict.ReasonStaleInputs}, 2: {verdict.ReasonStaleInputs}}
	if got := w.reasons(); !reflect.DeepEqual(got, wantReasons) {
		t.Errorf("published %v, want %v", got, wantReasons)
	}
	if !slices.Equal(w.actions.rerunCalls, []int64{201, 202}) {
		t.Errorf("re-runs = %v, want [201 202]", w.actions.rerunCalls)
	}
}

func TestDraftsAreIncluded(t *testing.T) {
	w := newWorld(t)
	r := w.repo(1, 11, "acme/shop")
	tg := w.pullWith(r, api.Pull{Number: 1, Draft: true, HeadRepo: r.FullName, BaseRef: "main"}, trusted())

	sum := w.pass(t0)

	if !w.state(tg).Pending || sum.Pended != 1 {
		t.Errorf("state = %+v, summary = %+v, want a pending check", w.state(tg), sum)
	}
}

// threeRepos has one stale pull in each of three installations: PR n in repository n with run 200+n.
func threeRepos(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	for i, name := range []string{"a/a", "b/b", "c/c"} {
		n := i + 1
		r := w.repo(int64(n), int64(10+n), name)
		rc := receipt(int64(200+n), verdict.ReasonHealthy)
		rc.PolicyDigest = "old"
		w.check(w.pull(r, n, trusted()), rc, t0.Add(-day))
	}
	return w
}

func TestRateLimitStopsThePass(t *testing.T) {
	limited := fmt.Errorf("list: %w", api.ErrRateLimited)
	tests := []struct {
		name          string
		inject        func(w *world)
		wantPublished bool // the second repository got its stale_inputs before the limit hit
	}{
		{"ListRepos", func(w *world) { w.repos.listReposErr[2] = limited }, false},
		{"OpenPulls", func(w *world) { w.pulls.errs["b/b"] = limited }, false},
		{"Resolve", func(w *world) { w.resolver.errs["b/b#2"] = limited }, false},
		{"State", func(w *world) { w.pub.errs["State:b/b"] = limited }, false},
		{"Publish", func(w *world) { w.pub.errs["Publish:b/b"] = limited }, false},
		{"Run", func(w *world) { w.actions.runErrs[202] = limited }, true},
		{"Rerun", func(w *world) { w.actions.rerunErrs[202] = limited }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := threeRepos(t)
			tc.inject(w)

			w.pub.now = t0
			sum, err := w.sweeper.Run(context.Background(), t0)

			if err != nil || !sum.RateLimited || sum.Errors != 0 || sum.Rerun != 1 {
				t.Errorf("summary = %+v, err = %v, want RateLimited, nil error, one re-run", sum, err)
			}
			wantReasons := map[int][]verdict.ReasonCode{1: {verdict.ReasonStaleInputs}}
			if tc.wantPublished {
				wantReasons[2] = []verdict.ReasonCode{verdict.ReasonStaleInputs}
			}
			if got := w.reasons(); !reflect.DeepEqual(got, wantReasons) {
				t.Errorf("published = %v, want %v", got, wantReasons)
			}
			if !slices.Contains(w.actions.rerunCalls, 201) || slices.Contains(w.actions.rerunCalls, 203) {
				t.Errorf("re-runs = %v, want the first repository's and never the third's", w.actions.rerunCalls)
			}
			for _, tg := range w.resolver.calls {
				if tg.Repo.FullName == "c/c" {
					t.Errorf("the pass went on to %s after the rate limit", tg.Repo.FullName)
				}
			}
			if got := w.pub.states["a/a@sha11-1"].Receipt.Reason; got != verdict.ReasonStaleInputs {
				t.Errorf("first repository's check says %q, want its repair kept", got)
			}
			if got := w.pub.states["c/c@sha13-3"].Receipt.Reason; got != verdict.ReasonHealthy {
				t.Errorf("third repository's check says %q, want it untouched", got)
			}
		})
	}

	t.Run("ListInstallations", func(t *testing.T) {
		w := threeRepos(t)
		w.repos.listInstErr = limited
		sum, err := w.sweeper.Run(context.Background(), t0)
		if want := (Summary{RateLimited: true}); err != nil || sum != want {
			t.Errorf("summary = %+v, err = %v, want %+v and nil", sum, err, want)
		}
	})
}

func TestUnitErrorsAreSkipped(t *testing.T) {
	logs := captureLogs(t)
	w := newWorld(t)
	w.pull(w.repo(1, 11, "a/a"), 1, trusted())
	w.repo(2, 12, "b/b")
	w.repos.listReposErr[2] = errBoom // one installation
	w.repo(3, 13, "c/c")
	w.pulls.errs["c/c"] = errBoom // one repository
	d := w.repo(3, 14, "d/d")
	w.pull(d, 2, trusted())
	w.resolver.errs["d/d#2"] = errBoom // one pull
	w.pull(d, 3, trusted())

	sum := w.pass(t0)

	if want := (Summary{Repos: 3, Pulls: 2, Pended: 2, Errors: 3}); sum != want {
		t.Errorf("summary = %+v, want %+v", sum, want)
	}
	var skipped []map[string]any
	sc := bufio.NewScanner(logs)
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		if line["level"] == "WARN" {
			delete(line, "time")
			delete(line, "level")
			delete(line, "msg")
			skipped = append(skipped, line)
		}
	}
	want := []map[string]any{
		{"unit": "installation", "installation_id": 2.0},
		{"unit": "repository", "installation_id": 3.0, "repo_id": 13.0},
		{"unit": "pull", "installation_id": 3.0, "repo_id": 14.0, "pr": 2.0, "head_sha": "sha14-2"},
	}
	if !reflect.DeepEqual(skipped, want) {
		t.Errorf("skip log lines = %v, want %v", skipped, want)
	}

	t.Run("RunRepo", func(t *testing.T) {
		sum, err := w.sweeper.RunRepo(context.Background(), d, t0)
		if want := (Summary{Repos: 1, Pulls: 1, Errors: 1}); err != nil || sum != want {
			t.Errorf("summary = %+v, err = %v, want %+v and nil", sum, err, want)
		}
	})
}

func TestListInstallationsFailure(t *testing.T) {
	t.Run("ListInstallations fails", func(t *testing.T) {
		w := threeRepos(t)
		w.repos.listInstErr = errBoom
		sum, err := w.sweeper.Run(context.Background(), t0)
		if !errors.Is(err, errBoom) || sum != (Summary{}) {
			t.Errorf("summary = %+v, err = %v, want the error and nothing done", sum, err)
		}
		if len(w.repos.listRepos) != 0 {
			t.Errorf("ListRepos called after the failure: %v", w.repos.listRepos)
		}
	})

	t.Run("ctx cancelled mid-pass", func(t *testing.T) {
		w := threeRepos(t)
		ctx, cancel := context.WithCancel(context.Background())
		w.resolver.hook = func(inputs.Target) { cancel() } // the first pull still completes
		w.pub.now = t0
		sum, err := w.sweeper.Run(ctx, t0)
		if want := (Summary{Repos: 1, Pulls: 1, Rerun: 1}); !errors.Is(err, context.Canceled) || sum != want {
			t.Errorf("summary = %+v, err = %v, want %+v and context.Canceled", sum, err, want)
		}
	})

	t.Run("a call fails because ctx was cancelled", func(t *testing.T) {
		w := threeRepos(t)
		ctx, cancel := context.WithCancel(context.Background())
		w.resolver.hook = func(inputs.Target) { cancel() }
		w.pub.errs["State:a/a"] = context.Canceled
		sum, err := w.sweeper.RunRepo(ctx, w.repos.repos[1][0], t0)
		if want := (Summary{Repos: 1, Pulls: 1}); !errors.Is(err, context.Canceled) || sum != want {
			t.Errorf("summary = %+v, err = %v, want %+v and context.Canceled, not a skipped unit", sum, err, want)
		}
	})
}

func TestSweepNeverPublishesSuccess(t *testing.T) {
	scenarios := map[string]func(t *testing.T) *world{
		"each rule, two passes": func(t *testing.T) *world {
			w, _ := eachRuleWorld(t, t0, "acme/shop", "main")
			w.pass(t0)
			w.pass(t0.Add(2 * time.Hour))
			return w
		},
		"re-runs complete without an upload": func(t *testing.T) *world {
			w, _ := eachRuleWorld(t, t0, "acme/shop", "main")
			w.pass(t0)
			for _, run := range w.actions.runs {
				run.Status = "completed"
			}
			w.pass(t0.Add(time.Hour))
			return w
		},
		"rate limited": func(t *testing.T) *world {
			w := threeRepos(t)
			w.pub.errs["State:b/b"] = api.ErrRateLimited
			w.pass(t0)
			return w
		},
		"unit errors": func(t *testing.T) *world {
			w := threeRepos(t)
			w.actions.rerunErrs[201] = errBoom
			w.actions.runErrs[202] = errBoom
			w.pass(t0)
			w.pass(t0.Add(time.Hour))
			return w
		},
		"missing upload": func(t *testing.T) *world {
			w := newWorld(t)
			w.pull(w.repo(1, 11, "acme/shop"), 1, trusted())
			w.pass(t0)
			w.pass(t0.Add(time.Hour))
			return w
		},
	}
	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) {
			w := run(t)
			if len(w.pub.published) == 0 {
				t.Fatal("the scenario published nothing")
			}
			w.assertNeverSuccess()
			for _, p := range w.pub.published {
				if st := w.state(p.Target); st.Receipt.Conclusion != verdict.ActionRequired {
					t.Errorf("PR %d ended %q", p.Target.PullRequest, st.Receipt.Conclusion)
				}
			}
		})
	}
}

func TestLogsCarryOnlyIdentifiers(t *testing.T) {
	logs := captureLogs(t)
	w, r := eachRuleWorld(t, t0, marker+"-org/"+marker+"-repo", marker+"-branch")
	w.repos.installations[0].Account = marker + "-login"
	for i := range w.pulls.open[r.FullName] {
		p := &w.pulls.open[r.FullName][i]
		p.HeadRef, p.Body, p.URL = marker+"-head", marker+" body", "https://example.test/"+marker
	}
	for key, tr := range w.resolver.trusted {
		tr.Settings.Owners = []string{marker + "-owner"}
		tr.Settings.Workflow = ".github/workflows/" + marker + ".yaml"
		tr.ProtectedPaths = []string{marker + "/policy.yaml"}
		tr.ApprovedBy = marker + "-reviewer"
		w.resolver.trusted[key] = tr
	}
	w.actions.rerunErrs[106] = errors.New(marker + ": GET /repos/" + r.FullName) // errors may quote names

	sum := w.pass(t0)

	if sum.Errors != 1 {
		t.Errorf("summary = %+v, want the failed re-run counted", sum)
	}
	out := logs.String()
	if strings.Contains(out, marker) {
		t.Errorf("logs carry a marker string:\n%s", out)
	}
	for _, want := range []string{`"head_sha":"sha11-6"`, `"repo_id":11`, `"installation_id":1`, `"pr":10`, `"rule":"6b"`, `"policy_digest":"p1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("logs lack %s:\n%s", want, out)
		}
	}
}
