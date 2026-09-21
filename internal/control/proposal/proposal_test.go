package proposal

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const testBranch = "archfit/onboarding"

var (
	testRepo   = api.RepoRef{InstallationID: 7, FullName: "acme/shop"}
	errGeneric = errors.New("boom")
)

func validChange() Change {
	return Change{
		Repo:    testRepo,
		Branch:  testBranch,
		Base:    fakeBase,
		Title:   "Add archfit",
		Body:    "Adds the policy and the workflow.",
		Message: "chore: add archfit",
		Files: []api.FileChange{
			{Path: ".archfit/policy.yaml", Content: []byte("version: 1\n")},
			{Path: ".github/workflows/archfit.yml", Content: []byte("name: archfit\n")},
		},
	}
}

func mustPropose(t *testing.T, p Proposals, c Change, expectHeadSHA string) Draft {
	t.Helper()
	d, err := p.Propose(context.Background(), c, expectHeadSHA)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	return d
}

func mustGet(t *testing.T, p Proposals) Draft {
	t.Helper()
	d, err := p.Get(context.Background(), testRepo, testBranch)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return d
}

func TestDraftStateMapping(t *testing.T) {
	tests := []struct {
		name string
		pull *api.Pull
		want DraftState
	}{
		{"open draft", &api.Pull{State: "open", Draft: true}, DraftOpen},
		{"open ready", &api.Pull{State: "open"}, DraftInReview},
		{"closed merged", &api.Pull{State: "closed", Merged: true}, DraftMerged},
		{"closed", &api.Pull{State: "closed"}, DraftClosed},
		{"closed draft", &api.Pull{State: "closed", Draft: true}, DraftClosed},
		{"no pull", nil, DraftNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGit()
			if tt.pull != nil {
				pull := *tt.pull
				pull.Number = 1
				pull.HeadRef = testBranch
				f.pulls = append(f.pulls, pull)
			}
			if got := mustGet(t, New(f, f)).State; got != tt.want {
				t.Errorf("State = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateChange(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Change)
		wantErr bool
	}{
		{"valid", func(*Change) {}, false},
		{"branch main", func(c *Change) { c.Branch = "main" }, true},
		{"branch outside namespace", func(c *Change) { c.Branch = "feature/x" }, true},
		{"branch with dot-dot", func(c *Change) { c.Branch = "archfit/../x" }, true},
		{"branch with space", func(c *Change) { c.Branch = "archfit/a b" }, true},
		{"branch with control character", func(c *Change) { c.Branch = "archfit/a\tb" }, true},
		{"branch is only the namespace", func(c *Change) { c.Branch = "archfit/" }, true},
		{"branch equal to base", func(c *Change) { c.Base = c.Branch }, true},
		{"empty base", func(c *Change) { c.Base = "" }, true},
		{"empty title", func(c *Change) { c.Title = "" }, true},
		{"empty message", func(c *Change) { c.Message = "" }, true},
		{"no files", func(c *Change) { c.Files = nil }, true},
		{"absolute path", func(c *Change) { c.Files[0].Path = "/etc/x" }, true},
		{"path escapes", func(c *Change) { c.Files[0].Path = "a/../../b" }, true},
		{"empty path", func(c *Change) { c.Files[0].Path = "" }, true},
		{"duplicate path", func(c *Change) { c.Files[1].Path = c.Files[0].Path }, true},
		{"installation 0", func(c *Change) { c.Repo.InstallationID = 0 }, true},
		{"full name without slash", func(c *Change) { c.Repo.FullName = "acme" }, true},
		{"full name with empty owner", func(c *Change) { c.Repo.FullName = "/shop" }, true},
		{"full name with two slashes", func(c *Change) { c.Repo.FullName = "acme/shop/x" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGit()
			c := validChange()
			tt.mutate(&c)
			_, err := New(f, f).Propose(context.Background(), c, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && len(f.calls) != 0 {
				t.Errorf("GitHub calls = %v, want none", f.calls)
			}
		})
	}
}

func TestDraftCarriesPullURL(t *testing.T) {
	const want = "https://ghe.example/acme/shop/pull/12"
	f := newFakeGit()
	f.nextNumber = 12
	p := New(f, f)

	if got := mustPropose(t, p, validChange(), "").URL; got != want {
		t.Errorf("Propose URL = %q, want %q", got, want)
	}
	if got := mustGet(t, p).URL; got != want {
		t.Errorf("Get URL = %q, want %q", got, want)
	}
}

func TestProposeCreatesBranchCommitAndDraftPull(t *testing.T) {
	f := newFakeGit()
	c := validChange()
	d := mustPropose(t, New(f, f), c, "")

	if want := []string{"EnsureBranch", "Commit", "EnsurePull"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	wantPull := api.NewPull{Title: c.Title, Body: c.Body, Head: c.Branch, Base: c.Base, Draft: true}
	if len(f.newPulls) != 1 || f.newPulls[0] != wantPull {
		t.Errorf("EnsurePull got %+v, want %+v", f.newPulls, wantPull)
	}
	if !reflect.DeepEqual(f.commitFiles, [][]api.FileChange{c.Files}) {
		t.Errorf("committed files = %v, want %v", f.commitFiles, c.Files)
	}
	want := Draft{Number: 1, State: DraftOpen, HeadSHA: "sha1", URL: fakeURLBase + "1"}
	if d != want {
		t.Errorf("Draft = %+v, want %+v", d, want)
	}
	if got := f.history(c.Branch); !slices.Equal(got, []string{fakeBaseSHA, "sha1"}) {
		t.Errorf("branch history = %v", got)
	}
}

func TestProposeTwiceKeepsOnePull(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)
	same := validChange()
	different := validChange()
	different.Files = []api.FileChange{{Path: "README.md", Content: []byte("hello\n")}}

	var previous []string
	for i, c := range []Change{same, same, same, different} {
		d := mustPropose(t, p, c, "")
		if d.Number != 1 {
			t.Errorf("call %d: Number = %d, want 1", i, d.Number)
		}
		history := f.history(testBranch)
		if len(history) != len(previous)+1 && i > 0 {
			t.Errorf("call %d: history = %v, want one commit on top of %v", i, history, previous)
		}
		if i > 0 && !slices.Equal(history[:len(previous)], previous) {
			t.Errorf("call %d: history rewritten: %v, was %v", i, history, previous)
		}
		if d.HeadSHA != history[len(history)-1] {
			t.Errorf("call %d: HeadSHA = %q, branch head %q", i, d.HeadSHA, history[len(history)-1])
		}
		previous = history
	}
	if len(f.pulls) != 1 {
		t.Errorf("pulls = %d, want 1", len(f.pulls))
	}
}

func TestProposeExpectHead(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*testing.T, *fakeGit, Proposals) (expect string)
		wantStale bool
	}{
		{"equals the draft head", func(t *testing.T, _ *fakeGit, p Proposals) string {
			return mustPropose(t, p, validChange(), "").HeadSHA
		}, false},
		{"differs", func(t *testing.T, _ *fakeGit, p Proposals) string {
			mustPropose(t, p, validChange(), "")
			return "other"
		}, true},
		{"no pull exists", func(*testing.T, *fakeGit, Proposals) string { return "sha1" }, true},
		{"newest pull is merged", func(t *testing.T, f *fakeGit, p Proposals) string {
			d := mustPropose(t, p, validChange(), "")
			f.merge(d.Number)
			return d.HeadSHA
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGit()
			p := New(f, f)
			expect := tt.setup(t, f, p)
			f.resetCalls()
			before := f.history(testBranch)

			d, err := p.Propose(context.Background(), validChange(), expect)

			if tt.wantStale {
				if !errors.Is(err, ErrStale) {
					t.Fatalf("err = %v, want ErrStale", err)
				}
				if slices.Contains(f.calls, "Commit") || slices.Contains(f.calls, "EnsurePull") {
					t.Errorf("calls = %v, want nothing written", f.calls)
				}
				if got := f.history(testBranch); len(before) > 0 && !slices.Equal(got, before) {
					t.Errorf("history = %v, want %v", got, before)
				}
				return
			}
			if err != nil {
				t.Fatalf("Propose: %v", err)
			}
			want := []string{"EnsureBranch", "PullByHead", "Commit", "EnsurePull"}
			if !slices.Equal(f.calls, want) {
				t.Errorf("calls = %v, want %v", f.calls, want)
			}
			if history := f.history(testBranch); d.HeadSHA != history[len(history)-1] || len(history) != len(before)+1 {
				t.Errorf("HeadSHA = %q, history %v", d.HeadSHA, history)
			}
		})
	}
}

func TestGetFindsOnlyOwnBranch(t *testing.T) {
	other := api.Pull{Number: 99, State: "open", Draft: true, HeadRef: "archfit/other", HeadSHA: "zzz", URL: fakeURLBase + "99"}
	own := api.Pull{Number: 4, State: "open", Draft: true, HeadRef: testBranch, HeadSHA: "abc", URL: fakeURLBase + "4"}
	merged := own
	merged.State, merged.Merged, merged.Draft = "closed", true, false

	tests := []struct {
		name    string
		pulls   []api.Pull
		err     error
		want    Draft
		wantErr error
	}{
		{"open pull", []api.Pull{own, other}, nil, Draft{4, DraftOpen, "abc", fakeURLBase + "4"}, nil},
		{"merged pull", []api.Pull{merged, other}, nil, Draft{4, DraftMerged, "abc", fakeURLBase + "4"}, nil},
		{"not found", []api.Pull{other}, nil, Draft{State: DraftNone}, nil},
		{"rate limited", []api.Pull{own}, api.ErrRateLimited, Draft{}, api.ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGit()
			f.pulls = tt.pulls
			if tt.err != nil {
				f.errs["PullByHead"] = tt.err
			}
			got, err := New(f, f).Get(context.Background(), testRepo, testBranch)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Draft = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSetBodyAndReady(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)
	d := mustPropose(t, p, validChange(), "")

	if err := p.SetBody(context.Background(), testRepo, d.Number, "new body"); err != nil {
		t.Fatalf("SetBody: %v", err)
	}
	if err := p.Ready(context.Background(), testRepo, d.Number); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	if want := []bodyCall{{d.Number, "new body"}}; !slices.Equal(f.bodies, want) {
		t.Errorf("UpdatePullBody calls = %v, want %v", f.bodies, want)
	}
	if want := []int{d.Number}; !slices.Equal(f.ready, want) {
		t.Errorf("MarkReady calls = %v, want %v", f.ready, want)
	}
	if got := mustGet(t, p).State; got != DraftInReview {
		t.Errorf("State = %q, want %q", got, DraftInReview)
	}

	f.errs["UpdatePullBody"] = api.ErrNotFound
	f.errs["MarkReady"] = api.ErrRateLimited
	if err := p.SetBody(context.Background(), testRepo, d.Number, "x"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("SetBody err = %v, want ErrNotFound", err)
	}
	if err := p.Ready(context.Background(), testRepo, d.Number); !errors.Is(err, api.ErrRateLimited) {
		t.Errorf("Ready err = %v, want ErrRateLimited", err)
	}
}

const (
	markerContent = "MARKER-CONTENT-7f3a"
	markerPath    = "MARKER-PATH-91bc"
	markerBody    = "MARKER-BODY-52de"
	markerTitle   = "MARKER-TITLE-c40f"
	markerMessage = "MARKER-MESSAGE-e8a1"
)

var markers = []string{markerContent, markerPath, markerBody, markerTitle, markerMessage}

func markedChange() Change {
	c := validChange()
	c.Title, c.Body, c.Message = markerTitle, markerBody, markerMessage
	c.Files = []api.FileChange{{Path: "docs/" + markerPath + ".yaml", Content: []byte(markerContent)}}
	return c
}

func assertNoMarkers(t *testing.T, what, text string) {
	t.Helper()
	for _, m := range markers {
		if strings.Contains(text, m) {
			t.Errorf("%s leaks %s: %s", what, m, text)
		}
	}
}

var proposeSteps = []string{"EnsureBranch", "PullByHead", "Commit", "EnsurePull"}

func TestGitHubErrorsPassThrough(t *testing.T) {
	for stepIndex, step := range proposeSteps {
		for _, fail := range []error{api.ErrRateLimited, api.ErrNotFound, errGeneric} {
			t.Run(step+"/"+fail.Error(), func(t *testing.T) {
				f := newFakeGit()
				p := New(f, f)
				head := mustPropose(t, p, markedChange(), "").HeadSHA
				f.resetCalls()
				before := f.history(testBranch)
				f.errs[step] = fail

				_, err := p.Propose(context.Background(), markedChange(), head)

				want := fail
				if step == "PullByHead" && errors.Is(fail, api.ErrNotFound) {
					want = ErrStale // a missing pull is DraftNone: the draft the caller read is gone
				}
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want %v", err, want)
				}
				if wantCalls := proposeSteps[:stepIndex+1]; !slices.Equal(f.calls, wantCalls) {
					t.Errorf("calls = %v, want %v", f.calls, wantCalls)
				}
				if stepIndex < 2 && !slices.Equal(f.history(testBranch), before) {
					t.Errorf("history = %v, want %v", f.history(testBranch), before)
				}
				assertNoMarkers(t, "error", err.Error())
			})
		}
	}
}

func TestRetryAfterPartialFailure(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)
	f.errs["EnsurePull"] = errGeneric

	if _, err := p.Propose(context.Background(), validChange(), ""); !errors.Is(err, errGeneric) {
		t.Fatalf("first Propose err = %v, want %v", err, errGeneric)
	}
	delete(f.errs, "EnsurePull")
	d := mustPropose(t, p, validChange(), "")

	if len(f.pulls) != 1 || d.Number != f.pulls[0].Number {
		t.Errorf("pulls = %+v, draft %+v, want exactly one", f.pulls, d)
	}
	if want := []string{fakeBaseSHA, "sha1", "sha2"}; !slices.Equal(f.history(testBranch), want) {
		t.Errorf("history = %v, want %v", f.history(testBranch), want)
	}
	if d.HeadSHA != "sha2" || d.State != DraftOpen {
		t.Errorf("Draft = %+v", d)
	}
}

func TestProposeKeepsReadyState(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)
	first := mustPropose(t, p, validChange(), "")
	if err := p.Ready(context.Background(), testRepo, first.Number); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	f.resetCalls()

	second := mustPropose(t, p, validChange(), "")

	if second.State != DraftInReview || second.Number != first.Number {
		t.Errorf("Draft = %+v, want %q on pull %d", second, DraftInReview, first.Number)
	}
	if want := []string{"EnsureBranch", "Commit", "EnsurePull"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if got := mustGet(t, p).State; got != DraftInReview {
		t.Errorf("State after Get = %q, want %q", got, DraftInReview)
	}
}

func TestNeverTouchesBaseOrMerges(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)
	ctx := context.Background()

	d := mustPropose(t, p, validChange(), "")
	mustPropose(t, p, validChange(), d.HeadSHA)
	if _, err := p.Propose(ctx, validChange(), "stale"); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	mustGet(t, p)
	if err := p.SetBody(ctx, testRepo, d.Number, "body"); err != nil {
		t.Fatalf("SetBody: %v", err)
	}
	if err := p.Ready(ctx, testRepo, d.Number); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	allowed := []string{"EnsureBranch", "Commit", "EnsurePull", "UpdatePullBody", "MarkReady", "PullByHead"}
	for _, call := range f.calls {
		if !slices.Contains(allowed, call) {
			t.Errorf("unexpected call %s", call)
		}
	}
	for _, method := range allowed {
		if !slices.Contains(f.calls, method) {
			t.Errorf("%s was never exercised", method)
		}
	}
	if slices.Contains(f.commitTargets, fakeBase) {
		t.Errorf("commit targets = %v, must not contain %s", f.commitTargets, fakeBase)
	}
	if want := []string{fakeBaseSHA}; !slices.Equal(f.history(fakeBase), want) {
		t.Errorf("base history = %v, want %v", f.history(fakeBase), want)
	}
}

func TestLogsCarryNoContent(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	f := newFakeGit()
	p := New(f, f)
	ctx := context.Background()
	d := mustPropose(t, p, markedChange(), "")
	if err := p.SetBody(ctx, testRepo, d.Number, markerBody); err != nil {
		t.Fatalf("SetBody: %v", err)
	}

	var errs []error
	for _, step := range proposeSteps {
		f.errs[step] = errGeneric
		_, err := p.Propose(ctx, markedChange(), d.HeadSHA)
		errs = append(errs, err)
		delete(f.errs, step)
	}
	_, err := p.Propose(ctx, markedChange(), "stale")
	errs = append(errs, err)
	f.errs["UpdatePullBody"] = errGeneric
	errs = append(errs, p.SetBody(ctx, testRepo, d.Number, markerBody))

	invalid := []func(*Change){
		func(c *Change) { c.Files[0].Path = "/" + c.Files[0].Path },
		func(c *Change) { c.Files[0].Path = "../" + c.Files[0].Path },
		func(c *Change) { c.Files = append(c.Files, c.Files[0]) },
		func(c *Change) { c.Branch = "feature/x" },
		func(c *Change) { c.Repo.InstallationID = 0 },
	}
	for _, mutate := range invalid {
		c := markedChange()
		mutate(&c)
		_, err := p.Propose(ctx, c, "")
		errs = append(errs, err)
	}

	for i, err := range errs {
		if err == nil {
			t.Fatalf("forced failure %d returned nil", i)
		}
		assertNoMarkers(t, "error", err.Error())
	}
	if logs.Len() == 0 {
		t.Error("no logs captured")
	}
	assertNoMarkers(t, "logs", logs.String())
}

func TestEditorSaveConflict(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)
	a := mustPropose(t, p, validChange(), "").HeadSHA

	first := validChange()
	first.Files[0].Content = []byte("version: 1 # first editor\n")
	b := mustPropose(t, p, first, a).HeadSHA

	second := validChange()
	second.Files[0].Content = []byte("version: 1 # second editor\n")
	if _, err := p.Propose(context.Background(), second, a); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if history := f.history(testBranch); history[len(history)-1] != b {
		t.Fatalf("history = %v, want head %s", history, b)
	}

	reloaded := mustGet(t, p)
	if reloaded.HeadSHA != b {
		t.Fatalf("reloaded HeadSHA = %q, want %q", reloaded.HeadSHA, b)
	}
	saved := mustPropose(t, p, second, reloaded.HeadSHA)
	if history := f.history(testBranch); saved.HeadSHA != history[len(history)-1] || saved.HeadSHA == b {
		t.Errorf("saved HeadSHA = %q, history %v", saved.HeadSHA, history)
	}
}

func TestOnboardingRetryStorm(t *testing.T) {
	const callers = 10
	f := newFakeGit()
	p := New(f, f)
	drafts := make([]Draft, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			drafts[i], errs[i] = p.Propose(context.Background(), validChange(), "")
		})
	}
	wg.Wait()

	open := f.openPulls(testBranch)
	if len(open) != 1 || len(f.pulls) != 1 {
		t.Fatalf("open pulls = %d, all pulls = %d, want 1 and 1", len(open), len(f.pulls))
	}
	for i := range callers {
		if errs[i] == nil && drafts[i].Number != open[0].Number {
			t.Errorf("caller %d: Number = %d, want %d", i, drafts[i].Number, open[0].Number)
		}
	}
	if got := len(f.history(testBranch)); got != callers+1 {
		t.Errorf("history length = %d, want %d", got, callers+1)
	}
}

func TestDraftLifecycle(t *testing.T) {
	f := newFakeGit()
	p := New(f, f)

	first := mustPropose(t, p, validChange(), "")
	if first.State != DraftOpen {
		t.Fatalf("after propose: State = %q, want %q", first.State, DraftOpen)
	}
	if err := p.Ready(context.Background(), testRepo, first.Number); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if got := mustGet(t, p).State; got != DraftInReview {
		t.Fatalf("after Ready: State = %q, want %q", got, DraftInReview)
	}
	f.merge(first.Number)
	if got := mustGet(t, p); got.State != DraftMerged || got.Number != first.Number {
		t.Fatalf("after merge: Draft = %+v, want %q on pull %d", got, DraftMerged, first.Number)
	}

	second := mustPropose(t, p, validChange(), "")
	if second.Number == first.Number || second.State != DraftOpen {
		t.Fatalf("after second propose: Draft = %+v, want a new draft pull", second)
	}
	if got := mustGet(t, p); got != second {
		t.Errorf("Get = %+v, want %+v", got, second)
	}
}
