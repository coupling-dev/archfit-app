package gate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

const (
	gateCheckName  = "archfit/gate"
	stateCheckName = "archfit/state"

	runInProgress = "in_progress"
	runCompleted  = "completed"
)

// fakeGitHub is an in-memory GitHub behind every role interface the gate path uses.
type fakeGitHub struct {
	mu sync.Mutex

	installations []api.Installation
	repos         map[string]api.Repo       // by full name
	files         map[string][]byte         // fileKey
	pulls         map[string]api.Pull       // pullKey
	changed       map[string][]string       // pullKey
	reviews       map[string][]api.Review   // pullKey
	teams         map[string]bool           // "team|login"
	runs          map[int64]api.WorkflowRun // run IDs are unique across the fake
	checks        map[string][]api.CheckRun // by full name
	comments      map[string][]api.Comment  // pullKey
	nextID        int64

	fail        map[string]error // method name → injected error
	calls       []fakeCall
	checkWrites []api.CheckRun // every created or updated check run, in order
	commentOps  int            // comments created or updated
	reruns      []int64
	// beforeCheckWrite runs once, unlocked, at the start of the next check run create or update: what it does
	// happens after that publisher's reads and before its first write.
	beforeCheckWrite func()
}

// checkWriteHook takes the one-shot beforeCheckWrite hook and runs it outside the lock.
func (f *fakeGitHub) checkWriteHook() {
	f.mu.Lock()
	hook := f.beforeCheckWrite
	f.beforeCheckWrite = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
}

type fakeCall struct {
	method         string
	installationID int64
	fullName       string
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		repos:    map[string]api.Repo{},
		files:    map[string][]byte{},
		pulls:    map[string]api.Pull{},
		changed:  map[string][]string{},
		reviews:  map[string][]api.Review{},
		teams:    map[string]bool{},
		runs:     map[int64]api.WorkflowRun{},
		checks:   map[string][]api.CheckRun{},
		comments: map[string][]api.Comment{},
		fail:     map[string]error{},
	}
}

func fileKey(fullName, ref, path string) string { return fullName + "|" + ref + "|" + path }
func pullKey(fullName string, number int) string {
	return fmt.Sprintf("%s#%d", fullName, number)
}

// enter records the call and returns the injected failure of the method, if any. The caller holds the lock.
func (f *fakeGitHub) enter(method string, r api.RepoRef) error {
	f.calls = append(f.calls, fakeCall{method, r.InstallationID, r.FullName})
	return f.fail[method]
}

func (f *fakeGitHub) ListInstallations(context.Context) ([]api.Installation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListInstallations", api.RepoRef{}); err != nil {
		return nil, err
	}
	return slices.Clone(f.installations), nil
}

func (f *fakeGitHub) ListRepos(_ context.Context, installationID int64) ([]api.Repo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListRepos", api.RepoRef{InstallationID: installationID}); err != nil {
		return nil, err
	}
	var out []api.Repo
	for _, r := range f.repos {
		if r.InstallationID == installationID {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b api.Repo) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeGitHub) ResolveRepo(_ context.Context, fullName string) (api.Repo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ResolveRepo", api.RepoRef{FullName: fullName}); err != nil {
		return api.Repo{}, err
	}
	r, ok := f.repos[fullName]
	if !ok {
		return api.Repo{}, api.ErrNotFound
	}
	return r, nil
}

func (f *fakeGitHub) File(_ context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("File", r); err != nil {
		return nil, "", err
	}
	raw, ok := f.files[fileKey(r.FullName, ref, path)]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return slices.Clone(raw), "blob", nil
}

func (f *fakeGitHub) BranchHead(_ context.Context, r api.RepoRef, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "", cmpErr(f.enter("BranchHead", r), api.ErrNotFound)
}

func cmpErr(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func (f *fakeGitHub) Pull(_ context.Context, r api.RepoRef, number int) (api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Pull", r); err != nil {
		return api.Pull{}, err
	}
	p, ok := f.pulls[pullKey(r.FullName, number)]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return p, nil
}

func (f *fakeGitHub) OpenPulls(_ context.Context, r api.RepoRef) ([]api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("OpenPulls", r); err != nil {
		return nil, err
	}
	var out []api.Pull
	for key, p := range f.pulls {
		if key == pullKey(r.FullName, p.Number) && p.State == "open" {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b api.Pull) int { return a.Number - b.Number })
	return out, nil
}

// PullByHead returns the newest pull request of r whose head is branch in r itself, like the real client.
func (f *fakeGitHub) PullByHead(_ context.Context, r api.RepoRef, branch string) (api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("PullByHead", r); err != nil {
		return api.Pull{}, err
	}
	var newest api.Pull
	found := false
	for _, p := range f.pulls {
		if p.HeadRef == branch && strings.EqualFold(p.HeadRepo, r.FullName) && (!found || p.Number > newest.Number) {
			newest, found = p, true
		}
	}
	if !found {
		return api.Pull{}, api.ErrNotFound
	}
	return newest, nil
}

func (f *fakeGitHub) ChangedFiles(_ context.Context, r api.RepoRef, number int) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ChangedFiles", r); err != nil {
		return nil, false, err
	}
	return slices.Clone(f.changed[pullKey(r.FullName, number)]), false, nil
}

func (f *fakeGitHub) Reviews(_ context.Context, r api.RepoRef, number int) ([]api.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Reviews", r); err != nil {
		return nil, err
	}
	return slices.Clone(f.reviews[pullKey(r.FullName, number)]), nil
}

func (f *fakeGitHub) TeamMember(_ context.Context, r api.RepoRef, team, login string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("TeamMember", r); err != nil {
		return false, err
	}
	return f.teams[team+"|"+login], nil
}

func (f *fakeGitHub) Run(_ context.Context, r api.RepoRef, runID int64) (api.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Run", r); err != nil {
		return api.WorkflowRun{}, err
	}
	run, ok := f.runs[runID]
	if !ok {
		return api.WorkflowRun{}, api.ErrNotFound
	}
	return run, nil
}

// Rerun models GitHub: an accepted request starts the next attempt of the run.
func (f *fakeGitHub) Rerun(_ context.Context, r api.RepoRef, runID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Rerun", r); err != nil {
		return err
	}
	run, ok := f.runs[runID]
	if !ok {
		return api.ErrNotFound
	}
	run.Attempt++
	run.Status, run.Conclusion = runInProgress, ""
	f.runs[runID] = run
	f.reruns = append(f.reruns, runID)
	return nil
}

func (f *fakeGitHub) Dispatch(_ context.Context, r api.RepoRef, _, _ string, _ map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cmpErr(f.enter("Dispatch", r), errors.New("fake: dispatch is not a gate call"))
}

func (f *fakeGitHub) LatestRun(_ context.Context, r api.RepoRef, _ string, _ api.RunFilter) (api.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return api.WorkflowRun{}, cmpErr(f.enter("LatestRun", r), api.ErrNotFound)
}

func (f *fakeGitHub) Artifact(_ context.Context, r api.RepoRef, _ int64, _ string, _ int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return nil, cmpErr(f.enter("Artifact", r), api.ErrNotFound)
}

func (f *fakeGitHub) CheckRuns(_ context.Context, r api.RepoRef, headSHA, name string) ([]api.CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CheckRuns", r); err != nil {
		return nil, err
	}
	runs := f.checkRuns(r.FullName, headSHA, name)
	for i := range runs {
		runs[i].AnnotationCount = len(runs[i].Annotations) // GitHub lists the number, as feedback reads it
	}
	return runs, nil
}

func (f *fakeGitHub) checkRuns(fullName, headSHA, name string) []api.CheckRun {
	var out []api.CheckRun
	for _, c := range f.checks[fullName] {
		if c.HeadSHA == headSHA && c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeGitHub) CreateCheckRun(_ context.Context, r api.RepoRef, c api.CheckRun) (api.CheckRun, error) {
	f.checkWriteHook()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CreateCheckRun", r); err != nil {
		return api.CheckRun{}, err
	}
	f.nextID++
	c.ID = f.nextID
	f.checks[r.FullName] = append(f.checks[r.FullName], c)
	f.checkWrites = append(f.checkWrites, c)
	return c, nil
}

func (f *fakeGitHub) UpdateCheckRun(_ context.Context, r api.RepoRef, c api.CheckRun) error {
	f.checkWriteHook()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("UpdateCheckRun", r); err != nil {
		return err
	}
	for i, old := range f.checks[r.FullName] {
		if old.ID == c.ID {
			f.checkWrites = append(f.checkWrites, c)
			// As GitHub: the annotations of every update are appended to the run's.
			c.Annotations = append(slices.Clone(old.Annotations), c.Annotations...)
			f.checks[r.FullName][i] = c
			return nil
		}
	}
	return api.ErrNotFound
}

func (f *fakeGitHub) AppComments(_ context.Context, r api.RepoRef, pull int) ([]api.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("AppComments", r); err != nil {
		return nil, err
	}
	return slices.Clone(f.comments[pullKey(r.FullName, pull)]), nil
}

func (f *fakeGitHub) CreateComment(_ context.Context, r api.RepoRef, pull int, body string) (api.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CreateComment", r); err != nil {
		return api.Comment{}, err
	}
	f.nextID++
	c := api.Comment{ID: f.nextID, Body: body}
	key := pullKey(r.FullName, pull)
	f.comments[key] = append(f.comments[key], c)
	f.commentOps++
	return c, nil
}

func (f *fakeGitHub) UpdateComment(_ context.Context, r api.RepoRef, id int64, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("UpdateComment", r); err != nil {
		return err
	}
	for key, list := range f.comments {
		for i := range list {
			if list[i].ID == id {
				f.comments[key][i].Body = body
				f.commentOps++
				return nil
			}
		}
	}
	return api.ErrNotFound
}

// writes is the number of check and comment writes so far.
func (f *fakeGitHub) writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checkWrites) + f.commentOps
}

// fakeVerifier is a stub oidc.Verifier keyed by the bearer token.
type fakeVerifier struct {
	audience string
	tokens   map[string]fakeToken
}

type fakeToken struct {
	claims   oidc.Claims
	audience string // "" = the verifier's audience
	err      error
}

var errBadToken = errors.New("fake oidc: token rejected")

func (v *fakeVerifier) Verify(_ context.Context, token, audience string) (oidc.Claims, error) {
	t, ok := v.tokens[token]
	if !ok {
		return oidc.Claims{}, errBadToken
	}
	if t.err != nil {
		return oidc.Claims{}, t.err
	}
	want := t.audience
	if want == "" {
		want = v.audience
	}
	if audience != want {
		return oidc.Claims{}, errBadToken
	}
	return t.claims, nil
}

// leaseKeyPrefix is gate/feedback's unexported lease key prefix, restated so that the guard can tell a lease
// operation from any other Store access.
const leaseKeyPrefix = "feedback/lease/"

// pusherKeyPrefix and approvalKeyPrefix are gate/inputs' unexported prefixes of the record of who pushed a head and of
// an owner approval recorded in the App, restated for the same reason.
const (
	pusherKeyPrefix   = "pusher/"
	approvalKeyPrefix = "approval/"
)

// isRecord reports whether key is a pusher or an approval record of gate/inputs.
func isRecord(key string) bool {
	return strings.HasPrefix(key, pusherKeyPrefix) || strings.HasPrefix(key, approvalKeyPrefix)
}

// guardStore proves the gate path writes nothing but publication leases and the records of gate/inputs: every other
// write panics, and so does a compare-and-swap outside the lease and record keys. It records the tenants and keys of
// other reads, and the tenants of lease and record operations separately.
type guardStore struct {
	inner store.Store
	// entitlementErr fails every read except the leases: the control plane's records are unreadable.
	entitlementErr error
	// down fails every operation, the leases included: the Store itself is unreachable.
	down error
	// beforeLease runs once, at the next lease read, before the lease is taken: what it does happens while that
	// publisher waits for the lease, between its decision and its first read inside the lease.
	beforeLease   func()
	mu            sync.Mutex
	tenants       []store.Tenant
	keys          []string
	leaseTenants  []store.Tenant
	recordTenants []store.Tenant // tenants of every read and write of a pusher or approval record
	recordKeys    []string       // their keys, in the same order
}

func (s *guardStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.mu.Lock()
	lease := strings.HasPrefix(key, leaseKeyPrefix)
	record := isRecord(key)
	var hook func()
	switch {
	case lease:
		s.leaseTenants = append(s.leaseTenants, t)
		hook, s.beforeLease = s.beforeLease, nil
	case record:
		s.recordTenants, s.recordKeys = append(s.recordTenants, t), append(s.recordKeys, key)
	default:
		s.tenants = append(s.tenants, t)
		s.keys = append(s.keys, key)
	}
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	switch {
	case s.down != nil:
		return nil, s.down
	case !lease && !record && s.entitlementErr != nil:
		return nil, s.entitlementErr
	}
	return s.inner.Get(ctx, t, key)
}

func (s *guardStore) CompareAndSwap(ctx context.Context, t store.Tenant, key string, old, value []byte) (bool, error) {
	record := isRecord(key)
	if !record && !strings.HasPrefix(key, leaseKeyPrefix) {
		panic("gate path wrote to the Store outside a publication lease or a record of gate/inputs: CompareAndSwap")
	}
	s.mu.Lock()
	if record {
		s.recordTenants, s.recordKeys = append(s.recordTenants, t), append(s.recordKeys, key)
	} else {
		s.leaseTenants = append(s.leaseTenants, t)
	}
	s.mu.Unlock()
	if s.down != nil {
		return false, s.down
	}
	return s.inner.CompareAndSwap(ctx, t, key, old, value)
}

func (s *guardStore) CompareAndSwapAll(context.Context, store.Tenant, []store.Swap) (bool, error) {
	panic("gate path wrote to the Store: CompareAndSwapAll")
}

func (s *guardStore) Put(context.Context, store.Tenant, string, []byte) error {
	panic("gate path wrote to the Store: Put")
}

func (s *guardStore) Delete(context.Context, store.Tenant, string) error {
	panic("gate path wrote to the Store: Delete")
}

func (s *guardStore) Add(context.Context, store.Tenant, string, int64) (int64, error) {
	panic("gate path wrote to the Store: Add")
}

func (s *guardStore) DeleteTenant(context.Context, store.Tenant) error {
	panic("gate path wrote to the Store: DeleteTenant")
}

func (s *guardStore) Tenants(context.Context) ([]store.Tenant, error) {
	panic("gate path listed Store tenants")
}

// Hand-written fakes of the children, for the pipeline and HandleEvent rule tests.

type fakeReceiver struct {
	upload Upload
	err    error
	kinds  []envelope.Kind
	ctxs   []context.Context
}

func (r *fakeReceiver) Receive(ctx context.Context, _ *http.Request, kind envelope.Kind) (Upload, error) {
	r.kinds = append(r.kinds, kind)
	r.ctxs = append(r.ctxs, ctx)
	return r.upload, r.err
}

type fakeResolver struct {
	trusted inputs.Trusted
	err     error
	targets []inputs.Target
	nows    []time.Time
	pushers []string // logins recorded by RecordPusher, in call order
}

func (r *fakeResolver) Resolve(_ context.Context, t inputs.Target, now time.Time) (inputs.Trusted, error) {
	r.targets = append(r.targets, t)
	r.nows = append(r.nows, now)
	return r.trusted, r.err
}

func (r *fakeResolver) RecordPusher(_ context.Context, _ inputs.Target, login string) error {
	r.pushers = append(r.pushers, login)
	return nil
}

// The approval methods run only over the real resolver, in the world.
func (r *fakeResolver) Candidate(context.Context, inputs.Target) (inputs.Candidate, error) {
	panic("fakeResolver: Candidate")
}

func (r *fakeResolver) CanApprove(context.Context, inputs.Target, string) error {
	panic("fakeResolver: CanApprove")
}

func (r *fakeResolver) Approve(context.Context, inputs.Target, string, map[string]string, time.Time) error {
	panic("fakeResolver: Approve")
}

type fakePublisher struct {
	state      feedback.CheckState
	stateErr   error
	publishErr error
	pendingErr error
	published  []feedback.Publication
	pended     []inputs.Target
	states     []inputs.Target
}

func (p *fakePublisher) Publish(_ context.Context, pub feedback.Publication) error {
	if p.publishErr != nil {
		return p.publishErr
	}
	p.published = append(p.published, pub)
	return nil
}

func (p *fakePublisher) Pending(_ context.Context, t inputs.Target) error {
	if p.pendingErr != nil {
		return p.pendingErr
	}
	p.pended = append(p.pended, t)
	return nil
}

func (p *fakePublisher) State(_ context.Context, t inputs.Target) (feedback.CheckState, error) {
	p.states = append(p.states, t)
	return p.state, p.stateErr
}

func (p *fakePublisher) AdvisoryState(ctx context.Context, t inputs.Target) (feedback.CheckState, error) {
	return p.State(ctx, t)
}

// Reconcile is State, decide and Publish, as feedback runs them inside the lease.
func (p *fakePublisher) Reconcile(ctx context.Context, t inputs.Target, decide feedback.Decision) error {
	current, err := p.State(ctx, t)
	if err != nil {
		return err
	}
	pub, err := decide(ctx, current)
	if err != nil || pub == nil {
		return err
	}
	return p.Publish(ctx, *pub)
}

func (p *fakePublisher) Warn(context.Context, inputs.Target, time.Time) error {
	panic("the façade never calls Warn")
}

type fakeSweeper struct {
	summary Summary
	err     error
	runs    []time.Time
	repos   []api.Repo
}

func (s *fakeSweeper) Run(_ context.Context, now time.Time) (Summary, error) {
	s.runs = append(s.runs, now)
	return s.summary, s.err
}

func (s *fakeSweeper) RunRepo(_ context.Context, r api.Repo, now time.Time) (Summary, error) {
	s.repos = append(s.repos, r)
	s.runs = append(s.runs, now)
	return s.summary, s.err
}

type fakeActions struct {
	api.Actions // the façade calls only Run and Rerun; anything else panics on the nil interface
	run         api.WorkflowRun
	runErr      error
	rerunErr    error
	ran         []int64
	reran       []int64
	refs        []api.RepoRef
}

func (a *fakeActions) Run(_ context.Context, r api.RepoRef, runID int64) (api.WorkflowRun, error) {
	a.ran = append(a.ran, runID)
	a.refs = append(a.refs, r)
	return a.run, a.runErr
}

func (a *fakeActions) Rerun(_ context.Context, r api.RepoRef, runID int64) error {
	if a.rerunErr != nil {
		return a.rerunErr
	}
	a.reran = append(a.reran, runID)
	a.refs = append(a.refs, r)
	return nil
}
