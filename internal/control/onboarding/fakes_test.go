package onboarding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

// forbiddenOps are the GitHub calls this module must never make.
var forbiddenOps = []string{"CreateCheckRun", "UpdateCheckRun", "Rerun", "Dispatch"}

type fakePR struct {
	number int
	repo   string
	head   string
	base   string
	title  string
	body   string
	open   bool
	merged bool
	rev    int
}

// hub is one in-memory GitHub: the role interfaces and the Proposals fake share its files and pull requests.
type hub struct {
	mu     sync.Mutex
	files  map[string]map[string][]byte // "owner/name@ref" → path → content
	prs    []*fakePR
	runs   map[string]api.WorkflowRun // by repository
	checks map[string][]api.CheckRun  // by repository
	calls  map[string]int
	failFn func(op, detail string) error
}

func newHub() *hub {
	return &hub{
		files:  map[string]map[string][]byte{},
		runs:   map[string]api.WorkflowRun{},
		checks: map[string][]api.CheckRun{},
		calls:  map[string]int{},
	}
}

// enter records the call and returns the injected failure, if any. Callers hold no lock.
func (h *hub) enter(ctx context.Context, op, detail string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls[op]++
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.failFn != nil {
		return h.failFn(op, detail)
	}
	return nil
}

func (h *hub) failOn(op string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failFn = func(o, _ string) error {
		if o == op {
			return err
		}
		return nil
	}
}

func (h *hub) failWhen(fn func(op, detail string) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failFn = fn
}

func (h *hub) count(op string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[op]
}

func (h *hub) forbiddenCalls() int {
	n := 0
	for _, op := range forbiddenOps {
		n += h.count(op)
	}
	return n
}

func refKey(repo, ref string) string { return repo + "@" + ref }

func (h *hub) put(r api.Repo, ref, path string, content []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.putLocked(r.FullName, ref, path, content)
}

func (h *hub) putLocked(repo, ref, path string, content []byte) {
	k := refKey(repo, ref)
	if h.files[k] == nil {
		h.files[k] = map[string][]byte{}
	}
	h.files[k][path] = append([]byte(nil), content...)
}

func (h *hub) file(r api.Repo, ref, path string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.files[refKey(r.FullName, ref)][path]
	return b, ok
}

func (h *hub) paths(r api.Repo, ref string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for p := range h.files[refKey(r.FullName, ref)] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (h *hub) setReported(r api.Repo, run api.WorkflowRun, checks ...api.CheckRun) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs[r.FullName] = run
	h.checks[r.FullName] = checks
}

func (h *hub) newest(repo, branch string) *fakePR {
	for i := len(h.prs) - 1; i >= 0; i-- {
		if h.prs[i].repo == repo && h.prs[i].head == branch {
			return h.prs[i]
		}
	}
	return nil
}

func (h *hub) prCount(r api.Repo, branch string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.prs {
		if p.repo == r.FullName && (branch == "" || p.head == branch) {
			n++
		}
	}
	return n
}

func (h *hub) body(r api.Repo, branch string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.newest(r.FullName, branch); p != nil {
		return p.body
	}
	return ""
}

func (h *hub) bodies() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, p := range h.prs {
		out = append(out, p.title, p.body)
	}
	return out
}

// merge lands the branch files on the base branch and closes the pull request as merged.
func (h *hub) merge(r api.Repo, branch string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.newest(r.FullName, branch)
	if p == nil || !p.open {
		panic("merge: no open pull request on " + branch)
	}
	for path, content := range h.files[refKey(r.FullName, branch)] {
		h.putLocked(r.FullName, p.base, path, content)
	}
	delete(h.files, refKey(r.FullName, branch))
	p.open, p.merged = false, true
}

func (h *hub) closePR(r api.Repo, branch string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.newest(r.FullName, branch); p != nil {
		p.open = false
	}
}

func draftOf(p *fakePR) proposal.Draft {
	d := proposal.Draft{
		Number:  p.number,
		HeadSHA: fmt.Sprintf("%040x", p.number*1000+p.rev),
		URL:     fmt.Sprintf("https://github.com/%s/pull/%d", p.repo, p.number),
	}
	switch {
	case p.merged:
		d.State = proposal.DraftMerged
	case !p.open:
		d.State = proposal.DraftClosed
	default:
		d.State = proposal.DraftOpen
	}
	return d
}

// Proposals fake.

type fakeProposals struct{ h *hub }

func (f fakeProposals) Propose(ctx context.Context, c proposal.Change, expectHeadSHA string) (proposal.Draft, error) {
	var paths []string
	for _, fc := range c.Files {
		paths = append(paths, fc.Path)
	}
	if err := f.h.enter(ctx, "Propose", c.Branch+" "+strings.Join(paths, ",")); err != nil {
		return proposal.Draft{}, err
	}
	if expectHeadSHA != "" {
		return proposal.Draft{}, errors.New("fake: this module never passes expectHeadSHA")
	}
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	for _, fc := range c.Files {
		f.h.putLocked(c.Repo.FullName, c.Branch, fc.Path, fc.Content)
	}
	p := f.h.newest(c.Repo.FullName, c.Branch)
	if p == nil || !p.open {
		p = &fakePR{number: len(f.h.prs) + 1, repo: c.Repo.FullName, head: c.Branch, base: c.Base, title: c.Title, body: c.Body, open: true}
		f.h.prs = append(f.h.prs, p)
	}
	p.rev++
	return draftOf(p), nil
}

func (f fakeProposals) Get(ctx context.Context, r api.RepoRef, branch string) (proposal.Draft, error) {
	if err := f.h.enter(ctx, "Get", branch); err != nil {
		return proposal.Draft{}, err
	}
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	p := f.h.newest(r.FullName, branch)
	if p == nil {
		return proposal.Draft{State: proposal.DraftNone}, nil
	}
	return draftOf(p), nil
}

func (f fakeProposals) SetBody(ctx context.Context, r api.RepoRef, number int, body string) error {
	if err := f.h.enter(ctx, "SetBody", ""); err != nil {
		return err
	}
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	for _, p := range f.h.prs {
		if p.repo == r.FullName && p.number == number {
			p.body = body
			return nil
		}
	}
	return api.ErrNotFound
}

func (f fakeProposals) Ready(ctx context.Context, _ api.RepoRef, _ int) error {
	return f.h.enter(ctx, "Ready", "")
}

// GitHub role fakes. Only File, LatestRun, CheckRuns and PullByHead are expected; the rest is recorded.

func (h *hub) ListInstallations(ctx context.Context) ([]api.Installation, error) {
	return nil, h.enter(ctx, "ListInstallations", "")
}

func (h *hub) ListRepos(ctx context.Context, _ int64) ([]api.Repo, error) {
	return nil, h.enter(ctx, "ListRepos", "")
}

func (h *hub) ResolveRepo(ctx context.Context, _ string) (api.Repo, error) {
	return api.Repo{}, h.enter(ctx, "ResolveRepo", "")
}

func (h *hub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	if err := h.enter(ctx, "File", path); err != nil {
		return nil, "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.files[refKey(r.FullName, ref)][path]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return append([]byte(nil), b...), "blob", nil
}

func (h *hub) BranchHead(ctx context.Context, _ api.RepoRef, _ string) (string, error) {
	return "", h.enter(ctx, "BranchHead", "")
}

func (h *hub) Run(ctx context.Context, _ api.RepoRef, _ int64) (api.WorkflowRun, error) {
	return api.WorkflowRun{}, h.enter(ctx, "Run", "")
}

func (h *hub) Rerun(ctx context.Context, _ api.RepoRef, _ int64) error {
	return h.enter(ctx, "Rerun", "")
}

func (h *hub) Dispatch(ctx context.Context, _ api.RepoRef, _, _ string, _ map[string]string) error {
	return h.enter(ctx, "Dispatch", "")
}

func (h *hub) LatestRun(ctx context.Context, r api.RepoRef, workflowPath, branch string) (api.WorkflowRun, error) {
	if err := h.enter(ctx, "LatestRun", workflowPath+"@"+branch); err != nil {
		return api.WorkflowRun{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	run, ok := h.runs[r.FullName]
	if !ok {
		return api.WorkflowRun{}, api.ErrNotFound
	}
	return run, nil
}

func (h *hub) Artifact(ctx context.Context, _ api.RepoRef, _ int64, _ string, _ int64) ([]byte, error) {
	return nil, h.enter(ctx, "Artifact", "")
}

func (h *hub) CheckRuns(ctx context.Context, r api.RepoRef, headSHA, name string) ([]api.CheckRun, error) {
	if err := h.enter(ctx, "CheckRuns", name); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []api.CheckRun
	for _, c := range h.checks[r.FullName] {
		if c.HeadSHA == headSHA && c.Name == name {
			out = append(out, c)
		}
	}
	return out, nil
}

func (h *hub) CreateCheckRun(ctx context.Context, _ api.RepoRef, c api.CheckRun) (api.CheckRun, error) {
	return c, h.enter(ctx, "CreateCheckRun", "")
}

func (h *hub) UpdateCheckRun(ctx context.Context, _ api.RepoRef, _ api.CheckRun) error {
	return h.enter(ctx, "UpdateCheckRun", "")
}

func (h *hub) Pull(ctx context.Context, _ api.RepoRef, _ int) (api.Pull, error) {
	return api.Pull{}, h.enter(ctx, "Pull", "")
}

func (h *hub) OpenPulls(ctx context.Context, _ api.RepoRef) ([]api.Pull, error) {
	return nil, h.enter(ctx, "OpenPulls", "")
}

func (h *hub) PullByHead(ctx context.Context, r api.RepoRef, branch string) (api.Pull, error) {
	if err := h.enter(ctx, "PullByHead", branch); err != nil {
		return api.Pull{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.newest(r.FullName, branch)
	if p == nil {
		return api.Pull{}, api.ErrNotFound
	}
	state := "closed"
	if p.open {
		state = "open"
	}
	d := draftOf(p)
	return api.Pull{Number: p.number, State: state, Merged: p.merged, Draft: p.open, Body: p.body, URL: d.URL, HeadSHA: d.HeadSHA, HeadRef: p.head, HeadRepo: p.repo, BaseRef: p.base}, nil
}

func (h *hub) ChangedFiles(ctx context.Context, _ api.RepoRef, _ int) ([]string, bool, error) {
	return nil, false, h.enter(ctx, "ChangedFiles", "")
}

func (h *hub) Reviews(ctx context.Context, _ api.RepoRef, _ int) ([]api.Review, error) {
	return nil, h.enter(ctx, "Reviews", "")
}

func (h *hub) TeamMember(ctx context.Context, _ api.RepoRef, _, _ string) (bool, error) {
	return false, h.enter(ctx, "TeamMember", "")
}

// Receiver fake.

type fakeReceiver struct {
	upload gate.Upload
	err    error
	kinds  []envelope.Kind
}

func (f *fakeReceiver) Receive(_ context.Context, _ *http.Request, kind envelope.Kind) (gate.Upload, error) {
	f.kinds = append(f.kinds, kind)
	return f.upload, f.err
}

// Consents fake.

type fakeConsents struct {
	mu     sync.Mutex
	has    bool
	asked  []account.ConsentKind
	writes int
}

func (f *fakeConsents) set(has bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.has = has
}

func (f *fakeConsents) Record(context.Context, int64, account.Consent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	return nil
}

func (f *fakeConsents) Revoke(context.Context, int64, account.ConsentKind) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	return nil
}

func (f *fakeConsents) Has(_ context.Context, _ int64, k account.ConsentKind) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, k)
	return f.has
}

// TokenSource fake.

type tokenCall struct {
	installationID int64
	fullName       string
}

type fakeTokens struct {
	mu    sync.Mutex
	err   error
	calls []tokenCall
	other int // AppJWT, InstallationToken, Invalidate: never expected
}

func (f *fakeTokens) AppJWT() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.other++
	return "", errors.New("fake: AppJWT is never called here")
}

func (f *fakeTokens) InstallationToken(context.Context, int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.other++
	return "", errors.New("fake: InstallationToken is never called here")
}

func (f *fakeTokens) Invalidate(int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.other++
}

func (f *fakeTokens) RepoReadToken(_ context.Context, installationID int64, fullName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tokenCall{installationID, fullName})
	if f.err != nil {
		return "", f.err
	}
	return "token-for-" + fullName, nil
}

func (f *fakeTokens) requested() []tokenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tokenCall(nil), f.calls...)
}

// Sandbox fake: fn decides the outcome; started signals every call so tests never sleep.

type fakeSandbox struct {
	mu      sync.Mutex
	fn      func(ctx context.Context, job DiscoveryJob) (DiscoveryResult, error)
	jobs    []DiscoveryJob
	started chan DiscoveryJob
}

func newSandbox(fn func(ctx context.Context, job DiscoveryJob) (DiscoveryResult, error)) *fakeSandbox {
	return &fakeSandbox{fn: fn, started: make(chan DiscoveryJob, 16)}
}

func (f *fakeSandbox) Discover(ctx context.Context, job DiscoveryJob) (DiscoveryResult, error) {
	f.mu.Lock()
	f.jobs = append(f.jobs, job)
	fn := f.fn
	f.mu.Unlock()
	f.started <- job
	return fn(ctx, job)
}

func (f *fakeSandbox) calls() []DiscoveryJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]DiscoveryJob(nil), f.jobs...)
}

// clock is the injected, manually advanced time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
