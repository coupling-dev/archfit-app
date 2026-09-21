package feedback

import (
	"context"
	"errors"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

var errSummaryTooLarge = errors.New("fake: summary over 65535 bytes")

// failures holds one-shot injected errors, keyed by method name.
type failures struct {
	mu   sync.Mutex
	next map[string]error
}

func (f *failures) failOnce(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next == nil {
		f.next = map[string]error{}
	}
	f.next[method] = err
}

func (f *failures) take(method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.next[method]
	delete(f.next, method)
	return err
}

type fakeChecks struct {
	failures
	mu     sync.Mutex
	runs   []api.CheckRun
	calls  []string
	lastID int64
}

func (f *fakeChecks) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeChecks) CheckRuns(_ context.Context, _ api.RepoRef, headSHA, name string) ([]api.CheckRun, error) {
	f.record("CheckRuns")
	if err := f.take("CheckRuns"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []api.CheckRun
	for _, r := range f.runs {
		if r.HeadSHA == headSHA && r.Name == name {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeChecks) CreateCheckRun(_ context.Context, _ api.RepoRef, c api.CheckRun) (api.CheckRun, error) {
	f.record("CreateCheckRun")
	if err := f.take("CreateCheckRun"); err != nil {
		return api.CheckRun{}, err
	}
	if len(c.Summary) > maxBody {
		return api.CheckRun{}, errSummaryTooLarge
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastID++
	c.ID = f.lastID
	f.runs = append(f.runs, c)
	return c, nil
}

func (f *fakeChecks) UpdateCheckRun(_ context.Context, _ api.RepoRef, c api.CheckRun) error {
	f.record("UpdateCheckRun")
	if err := f.take("UpdateCheckRun"); err != nil {
		return err
	}
	if len(c.Summary) > maxBody {
		return errSummaryTooLarge
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.runs {
		if f.runs[i].ID == c.ID {
			f.runs[i] = c
			return nil
		}
	}
	return api.ErrNotFound
}

func (f *fakeChecks) named(headSHA, name string) []api.CheckRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []api.CheckRun
	for _, r := range f.runs {
		if r.HeadSHA == headSHA && r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeChecks) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

type fakeComments struct {
	failures
	mu       sync.Mutex
	comments []api.Comment
	calls    []string
	lastID   int64
}

func (f *fakeComments) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeComments) AppComments(context.Context, api.RepoRef, int) ([]api.Comment, error) {
	f.record("AppComments")
	if err := f.take("AppComments"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.Comment(nil), f.comments...), nil
}

func (f *fakeComments) CreateComment(_ context.Context, _ api.RepoRef, _ int, body string) (api.Comment, error) {
	f.record("CreateComment")
	if err := f.take("CreateComment"); err != nil {
		return api.Comment{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastID++
	c := api.Comment{ID: f.lastID, Body: body}
	f.comments = append(f.comments, c)
	return c, nil
}

func (f *fakeComments) UpdateComment(_ context.Context, _ api.RepoRef, id int64, body string) error {
	f.record("UpdateComment")
	if err := f.take("UpdateComment"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.comments {
		if f.comments[i].ID == id {
			f.comments[i].Body = body
			return nil
		}
	}
	return api.ErrNotFound
}

func (f *fakeComments) all() []api.Comment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.Comment(nil), f.comments...)
}

func (f *fakeComments) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

// fakePulls answers Pull with a fixed head SHA; the other methods are unused by feedback.
type fakePulls struct {
	failures
	mu      sync.Mutex
	headSHA string
}

func (f *fakePulls) Pull(_ context.Context, _ api.RepoRef, number int) (api.Pull, error) {
	if err := f.take("Pull"); err != nil {
		return api.Pull{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return api.Pull{Number: number, State: "open", HeadSHA: f.headSHA}, nil
}

func (f *fakePulls) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) { return nil, nil }

func (f *fakePulls) PullByHead(context.Context, api.RepoRef, string) (api.Pull, error) {
	return api.Pull{}, api.ErrNotFound
}

func (f *fakePulls) ChangedFiles(context.Context, api.RepoRef, int) ([]string, bool, error) {
	return nil, false, nil
}

func (f *fakePulls) Reviews(context.Context, api.RepoRef, int) ([]api.Review, error) {
	return nil, nil
}

func (f *fakePulls) TeamMember(context.Context, api.RepoRef, string, string) (bool, error) {
	return false, nil
}
