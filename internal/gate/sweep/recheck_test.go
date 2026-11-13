package sweep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// fakeAnswerGitHub is GitHub for the re-check after an answer: the runs of fakeActions, the pull requests, the default
// branch, the branch update and the comments. calls lists every call in order.
type fakeAnswerGitHub struct {
	*fakeActions
	pulls      map[int]api.Pull
	reread     map[int]api.Pull // what Pull answers after a refused update; absent: the same
	tip        string
	updateErrs map[int]error
	updates    []string // "<pr>@<head>"
	comments   map[int][]string
	calls      []string
}

func (f *fakeAnswerGitHub) Run(ctx context.Context, r api.RepoRef, id int64) (api.WorkflowRun, error) {
	f.calls = append(f.calls, "Run")
	return f.fakeActions.Run(ctx, r, id)
}

func (f *fakeAnswerGitHub) HeadRuns(ctx context.Context, r api.RepoRef, path, head string) ([]api.WorkflowRun, error) {
	f.calls = append(f.calls, "HeadRuns")
	return f.fakeActions.HeadRuns(ctx, r, path, head)
}

func (f *fakeAnswerGitHub) Rerun(ctx context.Context, r api.RepoRef, id int64) error {
	f.calls = append(f.calls, "Rerun")
	return f.fakeActions.Rerun(ctx, r, id)
}

func (f *fakeAnswerGitHub) Pull(_ context.Context, _ api.RepoRef, n int) (api.Pull, error) {
	f.calls = append(f.calls, "Pull")
	if slices.Contains(f.calls[:len(f.calls)-1], "UpdateBranch") {
		if pl, ok := f.reread[n]; ok {
			return pl, nil
		}
	}
	pl, ok := f.pulls[n]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return pl, nil
}

func (f *fakeAnswerGitHub) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	f.calls = append(f.calls, "BranchHead")
	return f.tip, nil
}

func (f *fakeAnswerGitHub) UpdateBranch(_ context.Context, _ api.RepoRef, n int, head string) error {
	f.calls = append(f.calls, "UpdateBranch")
	f.updates = append(f.updates, fmt.Sprintf("%d@%s", n, head))
	return f.updateErrs[n]
}

func (f *fakeAnswerGitHub) CreateComment(_ context.Context, _ api.RepoRef, n int, body string) (api.Comment, error) {
	f.calls = append(f.calls, "CreateComment")
	f.comments[n] = append(f.comments[n], body)
	return api.Comment{}, nil
}

// answered is a transport that gives every request the same status.
type answered int

func (code answered) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: int(code), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"x"}`))}, nil
}

type tokens struct{}

func (tokens) AppJWT() (string, error)                                      { return "jwt", nil }
func (tokens) InstallationToken(context.Context, int64) (string, error)     { return "tok", nil }
func (tokens) Invalidate(int64)                                             {}
func (tokens) RepoReadToken(context.Context, int64, string) (string, error) { return "read", nil }

// updateError is the error the API client returns when GitHub answers an update with code.
func updateError(t *testing.T, code int) error {
	t.Helper()
	c := api.New(tokens{}, &http.Client{Transport: answered(code)})
	err := c.UpdateBranch(context.Background(), answerRepo.RepoRef, 12, strings.Repeat("a", 40))
	if err == nil || api.Status(err) != code && !errors.Is(err, api.ErrUnprocessable) {
		t.Fatalf("UpdateBranch answered %d: %v", code, err)
	}
	return err
}

const (
	answerHead  = "head12"
	answerBase  = "base0"
	answerTip   = "tip1"
	answerProp  = "prop1"
	answerIssue = 431
)

var answerRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, ID: 70, DefaultBranch: "main"}

// answerWorld is one waiting pull request (#12) of a consumed proposal whose answer moved the tip in the policy only,
// with all four conditions of design 10.5 true. Tests break single parts of it.
type answerWorld struct {
	t     *testing.T
	gh    *fakeAnswerGitHub
	store store.Store
	now   time.Time
}

func newAnswerWorld(t *testing.T) *answerWorld {
	t.Helper()
	now := t0
	a := &fakeActions{runs: map[int64]*api.WorkflowRun{}, runErrs: map[int64]error{}, rerunErrs: map[int64]error{},
		compares:    map[string]api.Comparison{answerBase + ".." + answerTip: {Status: "ahead", Paths: []string{".archfit.yaml"}}},
		compareErrs: map[string]error{}}
	w := &answerWorld{
		t: t,
		gh: &fakeAnswerGitHub{fakeActions: a, pulls: map[int]api.Pull{}, reread: map[int]api.Pull{}, tip: answerTip,
			updateErrs: map[int]error{}, comments: map[int][]string{}},
		store: store.NewMemory(),
		now:   now,
	}
	w.waiting(t, 12, answerHead, 9)
	w.request(t, answerIssue, 12)
	return w
}

// waiting adds an open pull request n on head with a completed run id and a waiting pull record of that run.
func (w *answerWorld) waiting(t *testing.T, n int, head string, id int64) {
	t.Helper()
	w.gh.pulls[n] = api.Pull{Number: n, State: "open", HeadSHA: head, HeadRepo: answerRepo.FullName, BaseRef: "main", BaseSHA: answerBase}
	w.gh.runs[id] = &api.WorkflowRun{ID: id, Attempt: 1, Status: "completed", Event: "pull_request", HeadSHA: head,
		WorkflowPath: workflowPath, PullRequests: []int{n}, CreatedAt: w.now.Add(-day)}
	w.put(t, records.PullKey(answerRepo.FullName, n), &records.Pull{HeadSHA: head, RunID: id, RunAttempt: 1, Status: "waiting"})
}

func (w *answerWorld) request(t *testing.T, issue int, waiting ...int) {
	t.Helper()
	w.put(t, records.RequestKey(answerRepo.FullName, issue), &records.Request{Key: "k", State: records.StateAnswered, Waiting: waiting})
}

func (w *answerWorld) put(t *testing.T, key string, v records.Value) {
	t.Helper()
	raw, err := records.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.store.Put(context.Background(), store.Tenant(answerRepo.InstallationID), key, raw); err != nil {
		t.Fatal(err)
	}
}

func (w *answerWorld) pullRecord(t *testing.T, n int) records.Pull {
	t.Helper()
	var rec records.Pull
	if err := records.Load(context.Background(), w.store, store.Tenant(answerRepo.InstallationID), records.PullKey(answerRepo.FullName, n), &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (w *answerWorld) recheck(pulls ...int) error {
	if len(pulls) == 0 {
		pulls = []int{12}
	}
	return AfterAnswer(context.Background(), w.gh, w.store, answerRepo,
		Consumed{Proposal: answerProp, Requests: []int{answerIssue}, Pulls: pulls}, w.now)
}

func TestAfterAnswerPaths(t *testing.T) {
	forbidden, refused := updateError(t, 403), updateError(t, 422)
	tests := []struct {
		name        string
		change      func(w *answerWorld)
		path, unmet string
		head        string   // the head recorded; answerHead when ""
		updates     []string // branch updates requested
		reruns      int
		note        string // a part of the request comment
	}{
		{name: "all four conditions hold: re-run", path: PathRerun, reruns: 1, note: "#12: its check runs again"},
		{name: "a run of the head in progress", change: func(w *answerWorld) { w.gh.runs[9].Status = "in_progress" },
			path: PathRunning, note: "already running"},

		{name: "the record is not an accepted result", change: func(w *answerWorld) {
			w.put(w.t, records.PullKey(answerRepo.FullName, 12), &records.Pull{HeadSHA: answerHead, RunID: 9, RunAttempt: 1, Status: "unknown"})
		}, path: PathUpdated, unmet: string(UnmetPolicyNotFromTip), updates: []string{"12@" + answerHead}, note: "did not read the policy from main"},
		{name: "a newer run of the head completed", change: func(w *answerWorld) {
			w.gh.runs[10] = &api.WorkflowRun{ID: 10, Attempt: 1, Status: "completed", Event: "pull_request", HeadSHA: answerHead,
				WorkflowPath: workflowPath, PullRequests: []int{12}, CreatedAt: w.now.Add(-time.Hour)}
		}, path: PathUpdated, unmet: string(UnmetNotLatest), updates: []string{"12@" + answerHead}, note: "not the latest run"},
		{name: "the run is 30 days old", change: func(w *answerWorld) { w.gh.runs[9].CreatedAt = w.now.Add(-rerunMaxAge) },
			path: PathUpdated, unmet: string(UnmetTooOld), updates: []string{"12@" + answerHead}, note: "30 days old"},
		{name: "the target changed in code too", change: func(w *answerWorld) {
			w.gh.compares[answerBase+".."+answerTip] = api.Comparison{Status: "ahead", Paths: []string{".archfit.yaml", "pkg/a.go"}}
		}, path: PathUpdated, unmet: string(UnmetBaseChanged), updates: []string{"12@" + answerHead}, note: "main changed in more than the policy"},
		{name: "no pull record", change: func(w *answerWorld) {
			_ = w.store.Delete(context.Background(), store.Tenant(answerRepo.InstallationID), records.PullKey(answerRepo.FullName, 12))
		}, path: PathUpdated, unmet: string(UnmetNoRun), updates: []string{"12@" + answerHead}, note: "No run of its check is known"},
		{name: "the head moved before the re-check read it", change: func(w *answerWorld) {
			pl := w.gh.pulls[12]
			pl.HeadSHA = "head13"
			w.gh.pulls[12] = pl
		}, path: PathUpdated, unmet: string(UnmetNotLatest), head: "head13", updates: []string{"12@head13"}, note: "the App updated its branch"},

		{name: "a fork is never updated", change: func(w *answerWorld) {
			pl := w.gh.pulls[12]
			pl.HeadRepo = "mallory/shop"
			w.gh.pulls[12] = pl
		}, path: PathFork, note: "comes from a fork"},
		{name: "the update is refused (422)", change: func(w *answerWorld) {
			w.gh.compares[answerBase+".."+answerTip] = api.Comparison{Status: "ahead", Paths: []string{".github/workflows/ci.yaml"}}
			w.gh.updateErrs[12] = refused
		}, path: PathNotUpdated, unmet: string(UnmetBaseChanged), updates: []string{"12@" + answerHead}, note: "An owner or the author updates the branch"},
		{name: "the update is forbidden (403, workflows permission)", change: func(w *answerWorld) {
			w.gh.compares[answerBase+".."+answerTip] = api.Comparison{Status: "ahead", Paths: []string{".github/workflows/ci.yaml"}}
			w.gh.updateErrs[12] = forbidden
		}, path: PathNotUpdated, unmet: string(UnmetBaseChanged), updates: []string{"12@" + answerHead}, note: "GitHub refused the update"},
		{name: "the head moved between the read and the update", change: func(w *answerWorld) {
			w.gh.runs[9].CreatedAt = w.now.Add(-rerunMaxAge)
			w.gh.updateErrs[12] = refused
			pl := w.gh.pulls[12]
			pl.HeadSHA = "head14"
			w.gh.reread[12] = pl
		}, path: PathHeadMoved, unmet: string(UnmetTooOld), updates: []string{"12@" + answerHead}, note: "its head moved"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newAnswerWorld(t)
			if tc.change != nil {
				tc.change(w)
			}

			if err := w.recheck(); err != nil {
				t.Fatalf("AfterAnswer: %v", err)
			}

			if !slices.Equal(w.gh.updates, tc.updates) || len(w.gh.rerunCalls) != tc.reruns {
				t.Errorf("updates %v, re-runs %v; want %v, %d", w.gh.updates, w.gh.rerunCalls, tc.updates, tc.reruns)
			}
			if i := slices.Index(w.gh.calls, "UpdateBranch"); i >= 0 {
				// The update is asynchronous: only a refusal reads the pull request again, and nothing reads the runs.
				for _, c := range w.gh.calls[i+1:] {
					if c == "HeadRuns" || c == "Run" || (c == "Pull" && w.gh.updateErrs[12] == nil) {
						t.Errorf("calls after UpdateBranch: %v", w.gh.calls[i+1:])
					}
				}
			}
			if notes := w.gh.comments[answerIssue]; len(notes) != 1 || !strings.Contains(notes[0], tc.note) {
				t.Errorf("request comments %q, want one with %q", notes, tc.note)
			}
			if tc.name == "no pull record" {
				return
			}
			head := tc.head
			if head == "" {
				head = answerHead
			}
			want := records.Recheck{Proposal: answerProp, Path: tc.path, Unmet: tc.unmet, HeadSHA: head}
			if got := w.pullRecord(t, 12).Recheck; got == nil || *got != want {
				t.Errorf("recorded %+v, want %+v", got, want)
			}
		})
	}
}

func TestAfterAnswerAgainDoesNothing(t *testing.T) {
	w := newAnswerWorld(t)
	w.gh.compares[answerBase+".."+answerTip] = api.Comparison{Status: "ahead", Paths: []string{"pkg/a.go"}}
	w.waiting(t, 14, "head14", 11)
	w.request(t, answerIssue, 12, 14)
	if err := w.recheck(12, 14); err != nil {
		t.Fatal(err)
	}
	before := len(w.gh.calls)

	if err := w.recheck(12, 14); err != nil {
		t.Fatal(err)
	}

	if after := w.gh.calls[before:]; slices.ContainsFunc(after, func(c string) bool { return c != "Pull" && c != "BranchHead" }) {
		t.Errorf("a second delivery called %v, want no re-run, update or comment", after)
	}
	if len(w.gh.updates) != 2 || len(w.gh.comments[answerIssue]) != 1 {
		t.Errorf("updates %v, comments %d", w.gh.updates, len(w.gh.comments[answerIssue]))
	}
}

func TestAfterAnswerTellsEachRequestItsPulls(t *testing.T) {
	w := newAnswerWorld(t)
	w.waiting(t, 14, "head14", 11)
	w.request(t, answerIssue, 12)
	w.request(t, 432, 12, 14, 15)
	w.gh.pulls[15] = api.Pull{Number: 15, State: "closed", HeadSHA: "head15", HeadRepo: answerRepo.FullName, BaseRef: "main"}

	err := AfterAnswer(context.Background(), w.gh, w.store, answerRepo,
		Consumed{Proposal: answerProp, Requests: []int{answerIssue, 432}, Pulls: []int{15, 14, 12, 12}}, w.now)
	if err != nil {
		t.Fatal(err)
	}

	first, second := w.gh.comments[answerIssue], w.gh.comments[432]
	if len(first) != 1 || !strings.Contains(first[0], "#12:") || strings.Contains(first[0], "#14") {
		t.Errorf("request %d: %q, want #12 only", answerIssue, first)
	}
	if len(second) != 1 || !strings.Contains(second[0], "#12:") || !strings.Contains(second[0], "#14:") || strings.Contains(second[0], "#15") {
		t.Errorf("request 432: %q, want #12 and #14", second)
	}
	if !slices.Equal(w.gh.rerunCalls, []int64{9, 11}) {
		t.Errorf("re-runs %v, want each open pull request once, the closed one never", w.gh.rerunCalls)
	}
}

func TestAfterAnswerFailureIsRetried(t *testing.T) {
	w := newAnswerWorld(t)
	w.gh.compares[answerBase+".."+answerTip] = api.Comparison{Status: "ahead", Paths: []string{"pkg/a.go"}}
	w.gh.updateErrs[12] = updateError(t, 502)

	if err := w.recheck(); err == nil {
		t.Fatal("a failed update was not returned")
	}
	if got := w.pullRecord(t, 12).Recheck; got != nil || len(w.gh.comments) != 0 {
		t.Fatalf("recorded %+v, comments %v after a failure", got, w.gh.comments)
	}

	delete(w.gh.updateErrs, 12)
	if err := w.recheck(); err != nil {
		t.Fatal(err)
	}
	if got := w.pullRecord(t, 12).Recheck; got == nil || got.Path != PathUpdated || len(w.gh.updates) != 2 {
		t.Errorf("recorded %+v, updates %v; want the retry to update", got, w.gh.updates)
	}
}
