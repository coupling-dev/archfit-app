package feedback

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

var (
	errSummaryTooLarge    = errors.New("fake: summary over 65535 bytes")
	errTooManyAnnotations = errors.New("fake: more than 50 annotations in one request")
)

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
	// annotationRequests is the number of annotations each create or update carried, in call order.
	annotationRequests []int
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
	if err := requestLimits(c); err != nil {
		return api.CheckRun{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.annotationRequests = append(f.annotationRequests, len(c.Annotations))
	f.lastID++
	c.ID = f.lastID
	f.runs = append(f.runs, c)
	return c, nil
}

// UpdateCheckRun appends the annotations of the request to the run, as GitHub does.
func (f *fakeChecks) UpdateCheckRun(_ context.Context, _ api.RepoRef, c api.CheckRun) error {
	f.record("UpdateCheckRun")
	if err := f.take("UpdateCheckRun"); err != nil {
		return err
	}
	if err := requestLimits(c); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.runs {
		if f.runs[i].ID == c.ID {
			f.annotationRequests = append(f.annotationRequests, len(c.Annotations))
			c.Annotations = append(slices.Clone(f.runs[i].Annotations), c.Annotations...)
			f.runs[i] = c
			return nil
		}
	}
	return api.ErrNotFound
}

// requestLimits rejects what the real client rejects before sending.
func requestLimits(c api.CheckRun) error {
	switch {
	case len(c.Summary) > maxBody:
		return errSummaryTooLarge
	case len(c.Annotations) > 50:
		return errTooManyAnnotations
	}
	return nil
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

// fakePulls answers Pull with a fixed head SHA and ChangedFiles with a fixed list; feedback uses no other method.
type fakePulls struct {
	failures
	mu        sync.Mutex
	headSHA   string
	changed   []string
	truncated bool
	listed    int // ChangedFiles calls
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
	f.mu.Lock()
	f.listed++
	f.mu.Unlock()
	if err := f.take("ChangedFiles"); err != nil {
		return nil, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.changed), f.truncated, nil
}

func (f *fakePulls) Reviews(context.Context, api.RepoRef, int) ([]api.Review, error) {
	return nil, nil
}

func (f *fakePulls) TeamMember(context.Context, api.RepoRef, string, string) (bool, error) {
	return false, nil
}
