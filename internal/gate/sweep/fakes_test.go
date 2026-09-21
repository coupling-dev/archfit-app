package sweep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// t0 has an hour index divisible by 3, so a three-installation pass starts at index 0.
var t0 = time.Unix(1782000000, 0).UTC()

const (
	day    = 24 * time.Hour
	marker = "MARKER"
)

var (
	errBoom  = errors.New("boom")
	errOlder = errors.New("fake publisher: older receipt refused")
)

type fakeRepos struct {
	api.Repos     // unused methods panic: the sweep must not call them
	installations []api.Installation
	repos         map[int64][]api.Repo
	listInstErr   error
	listReposErr  map[int64]error
	listInstCalls int
	listRepos     []int64 // installation IDs in call order
}

func (f *fakeRepos) ListInstallations(context.Context) ([]api.Installation, error) {
	f.listInstCalls++
	return f.installations, f.listInstErr
}

func (f *fakeRepos) ListRepos(_ context.Context, installationID int64) ([]api.Repo, error) {
	f.listRepos = append(f.listRepos, installationID)
	if err := f.listReposErr[installationID]; err != nil {
		return nil, err
	}
	return f.repos[installationID], nil
}

type fakePulls struct {
	api.Pulls
	open map[string][]api.Pull // by repository full name
	errs map[string]error
}

func (f *fakePulls) OpenPulls(_ context.Context, r api.RepoRef) ([]api.Pull, error) {
	if err := f.errs[r.FullName]; err != nil {
		return nil, err
	}
	return f.open[r.FullName], nil
}

type fakeActions struct {
	api.Actions
	runs       map[int64]*api.WorkflowRun
	runErrs    map[int64]error
	rerunErrs  map[int64]error
	runCalls   []int64
	rerunCalls []int64 // every request, failed ones included
}

func (f *fakeActions) Run(_ context.Context, _ api.RepoRef, runID int64) (api.WorkflowRun, error) {
	f.runCalls = append(f.runCalls, runID)
	if err := f.runErrs[runID]; err != nil {
		return api.WorkflowRun{}, err
	}
	run, ok := f.runs[runID]
	if !ok {
		return api.WorkflowRun{}, fmt.Errorf("run %d: %w", runID, api.ErrNotFound)
	}
	return *run, nil
}

// Rerun behaves like GitHub: a successful request queues the next attempt.
func (f *fakeActions) Rerun(_ context.Context, _ api.RepoRef, runID int64) error {
	f.rerunCalls = append(f.rerunCalls, runID)
	if err := f.rerunErrs[runID]; err != nil {
		return err
	}
	run := f.runs[runID]
	run.Attempt++
	run.Status = "queued"
	return nil
}

type fakeResolver struct {
	trusted map[string]inputs.Trusted // by pullKey; a missing entry is "no settings file"
	errs    map[string]error
	calls   []inputs.Target
	nows    []time.Time
	hook    func(inputs.Target)
}

func (f *fakeResolver) Resolve(_ context.Context, t inputs.Target, now time.Time) (inputs.Trusted, error) {
	f.calls = append(f.calls, t)
	f.nows = append(f.nows, now)
	if f.hook != nil {
		f.hook(t)
	}
	if err := f.errs[pullKey(t)]; err != nil {
		return inputs.Trusted{}, err
	}
	return f.trusted[pullKey(t)], nil
}

type warning struct {
	target inputs.Target
	expiry time.Time
}

// fakePublisher serves State from what was published, as feedback does from GitHub.
type fakePublisher struct {
	now        time.Time // the publication clock; world.pass sets it
	states     map[string]feedback.CheckState
	errs       map[string]error // by "<Method>:<full name>"
	published  []feedback.Publication
	pendings   []inputs.Target
	warnings   []warning
	stateCalls int
}

func (f *fakePublisher) Publish(_ context.Context, p feedback.Publication) error {
	if err := f.errs["Publish:"+p.Target.Repo.FullName]; err != nil {
		return err
	}
	prev, next := f.states[checkKey(p.Target)].Receipt, p.Receipt
	if next.RunID < prev.RunID || (next.RunID == prev.RunID && next.RunAttempt < prev.RunAttempt) {
		return errOlder
	}
	f.published = append(f.published, p)
	f.states[checkKey(p.Target)] = feedback.CheckState{Exists: true, StartedAt: f.now, Receipt: p.Receipt}
	return nil
}

func (f *fakePublisher) Pending(_ context.Context, t inputs.Target) error {
	if err := f.errs["Pending:"+t.Repo.FullName]; err != nil {
		return err
	}
	f.pendings = append(f.pendings, t)
	if !f.states[checkKey(t)].Exists {
		f.states[checkKey(t)] = feedback.CheckState{Exists: true, Pending: true, StartedAt: f.now}
	}
	return nil
}

func (f *fakePublisher) State(_ context.Context, t inputs.Target) (feedback.CheckState, error) {
	f.stateCalls++
	if err := f.errs["State:"+t.Repo.FullName]; err != nil {
		return feedback.CheckState{}, err
	}
	return f.states[checkKey(t)], nil
}

func (f *fakePublisher) Warn(_ context.Context, t inputs.Target, expiry time.Time) error {
	if err := f.errs["Warn:"+t.Repo.FullName]; err != nil {
		return err
	}
	f.warnings = append(f.warnings, warning{t, expiry})
	return nil
}

func pullKey(t inputs.Target) string  { return fmt.Sprintf("%s#%d", t.Repo.FullName, t.PullRequest) }
func checkKey(t inputs.Target) string { return t.Repo.FullName + "@" + t.HeadSHA }

// world is one GitHub account as the fakes see it, plus the sweeper under test.
type world struct {
	t        *testing.T
	repos    *fakeRepos
	pulls    *fakePulls
	actions  *fakeActions
	resolver *fakeResolver
	pub      *fakePublisher
	sweeper  Sweeper
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		t:        t,
		repos:    &fakeRepos{repos: map[int64][]api.Repo{}, listReposErr: map[int64]error{}},
		pulls:    &fakePulls{open: map[string][]api.Pull{}, errs: map[string]error{}},
		actions:  &fakeActions{runs: map[int64]*api.WorkflowRun{}, runErrs: map[int64]error{}, rerunErrs: map[int64]error{}},
		resolver: &fakeResolver{trusted: map[string]inputs.Trusted{}, errs: map[string]error{}},
		pub:      &fakePublisher{states: map[string]feedback.CheckState{}, errs: map[string]error{}},
	}
	w.sweeper = New(w.repos, w.pulls, w.actions, w.resolver, w.pub, Config{})
	t.Cleanup(w.assertNeverSuccess)
	return w
}

// assertNeverSuccess holds in every scenario: the sweep publishes action_required only.
func (w *world) assertNeverSuccess() {
	w.t.Helper()
	for _, p := range w.pub.published {
		if p.Outcome.Conclusion != verdict.ActionRequired || p.Receipt.Conclusion != verdict.ActionRequired {
			w.t.Errorf("PR %d: published %q/%q, want action_required",
				p.Target.PullRequest, p.Outcome.Conclusion, p.Receipt.Conclusion)
		}
		if p.Report != nil {
			w.t.Errorf("PR %d: sweep publication carries a report", p.Target.PullRequest)
		}
	}
}

func (w *world) repo(installationID, id int64, fullName string) api.Repo {
	if _, ok := w.repos.repos[installationID]; !ok {
		w.repos.installations = append(w.repos.installations, api.Installation{ID: installationID})
	}
	r := api.Repo{RepoRef: api.RepoRef{InstallationID: installationID, FullName: fullName}, ID: id, DefaultBranch: "main"}
	w.repos.repos[installationID] = append(w.repos.repos[installationID], r)
	return r
}

// pull opens a same-repository pull against main and scripts its trusted inputs.
func (w *world) pull(r api.Repo, number int, tr inputs.Trusted) inputs.Target {
	return w.pullWith(r, api.Pull{Number: number, HeadRepo: r.FullName, BaseRef: "main"}, tr)
}

func (w *world) pullWith(r api.Repo, p api.Pull, tr inputs.Trusted) inputs.Target {
	p.State = "open"
	p.HeadSHA = fmt.Sprintf("sha%d-%d", r.ID, p.Number)
	w.pulls.open[r.FullName] = append(w.pulls.open[r.FullName], p)
	tg := inputs.Target{Repo: r, PullRequest: p.Number, HeadSHA: p.HeadSHA, BaseRef: p.BaseRef, Fork: p.HeadRepo != r.FullName}
	w.resolver.trusted[pullKey(tg)] = tr
	return tg
}

func (w *world) trust(tg inputs.Target, tr inputs.Trusted) { w.resolver.trusted[pullKey(tg)] = tr }

// check puts a completed gate check on the target's head; a run for its receipt exists, completed.
func (w *world) check(tg inputs.Target, rc feedback.Receipt, startedAt time.Time) {
	w.pub.states[checkKey(tg)] = feedback.CheckState{Exists: true, StartedAt: startedAt, Receipt: rc}
	if rc.RunID != 0 {
		w.run(rc.RunID, rc.RunAttempt, "completed")
	}
}

func (w *world) pendingCheck(tg inputs.Target, startedAt time.Time) {
	w.pub.states[checkKey(tg)] = feedback.CheckState{Exists: true, Pending: true, StartedAt: startedAt}
}

func (w *world) run(id int64, attempt int, status string) {
	w.actions.runs[id] = &api.WorkflowRun{ID: id, Attempt: attempt, Status: status}
}

func (w *world) state(tg inputs.Target) feedback.CheckState { return w.pub.states[checkKey(tg)] }

func (w *world) pass(now time.Time) Summary {
	w.t.Helper()
	w.pub.now = now
	sum, err := w.sweeper.Run(context.Background(), now)
	if err != nil {
		w.t.Fatalf("Run: %v", err)
	}
	return sum
}

func (w *world) passRepo(r api.Repo, now time.Time) Summary {
	w.t.Helper()
	w.pub.now = now
	sum, err := w.sweeper.RunRepo(context.Background(), r, now)
	if err != nil {
		w.t.Fatalf("RunRepo: %v", err)
	}
	return sum
}

// calls is a snapshot of every write the sweep can make.
type calls struct{ published, pendings, warnings, reruns int }

func (w *world) calls() calls {
	return calls{len(w.pub.published), len(w.pub.pendings), len(w.pub.warnings), len(w.actions.rerunCalls)}
}

// reasons maps PR number to the reason codes published for it, in order.
func (w *world) reasons() map[int][]verdict.ReasonCode {
	got := map[int][]verdict.ReasonCode{}
	for _, p := range w.pub.published {
		got[p.Target.PullRequest] = append(got[p.Target.PullRequest], p.Receipt.Reason)
	}
	return got
}

// trusted is a required-mode repository with one policy on the protected ref.
func trusted() inputs.Trusted {
	return inputs.Trusted{
		Settings:      settings.Settings{Version: 1, Gate: settings.GateRequired},
		SettingsFound: true,
		PolicyDigests: []string{"p1"},
		Approval:      inputs.ApprovalNotRequired,
	}
}

// receipt is a readable receipt that matches trusted(). Run 0 means "no upload".
func receipt(runID int64, reason verdict.ReasonCode) feedback.Receipt {
	rc := feedback.Receipt{RunID: runID, PolicyDigest: "p1", Conclusion: verdict.ActionRequired, Reason: reason}
	if runID != 0 {
		rc.RunAttempt, rc.ReportDigest = 1, "rd"
	}
	if reason == verdict.ReasonHealthy {
		rc.Conclusion = verdict.Success
	}
	return rc
}

// captureLogs swaps the default logger; callers must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
