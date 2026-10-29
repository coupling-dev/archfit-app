package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

func sha(n int) string { return fmt.Sprintf("%040x", n) }

// ghCall is one call that reached the fake GitHub.
type ghCall struct {
	method         string
	installationID int64
	fullName       string
	detail         string
}

// fakeGH is GitHub as ui and session see it: the role-interface methods they call, over maps.
type fakeGH struct {
	mu        sync.Mutex
	repos     map[string]api.Repo          // installed repositories by full name
	perms     map[string]string            // token + " " + full name → permission
	listed    map[string][]string          // token → full names UserRepos returns
	files     map[string][]byte            // full name + "@" + ref + ":" + path
	heads     map[string]string            // full name + "@" + branch
	checks    map[string][]api.CheckRun    // full name + "@" + sha + "#" + check name
	runs      map[string][]api.WorkflowRun // full name + "@" + workflow + "@" + branch, newest first
	artifacts map[int64][]byte
	pulls     map[string]api.Pull // full name + "#" + number
	fail      map[string]error    // method, method + " " + full name, or method + " " + the call's detail
	calls     []ghCall
	meet      *meeting // set before the request: the calls that must be in flight together
}

// meeting holds the first call of each key it names until every key has arrived, so reads made in turn never meet.
// A key is a method, or a method and the call's detail as in fakeGH.fail. A read that never comes leaves the others
// to go on after a timeout, and the meeting reports the miss.
type meeting struct {
	mu      sync.Mutex
	arrived map[string]bool
	all     chan struct{}
	missed  bool
}

func newMeeting(keys ...string) *meeting {
	m := &meeting{arrived: map[string]bool{}, all: make(chan struct{})}
	for _, k := range keys {
		m.arrived[k] = false
	}
	return m
}

func (m *meeting) arrive(method, detail string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	key := ""
	for _, k := range []string{method + " " + detail, method} {
		if arrived, named := m.arrived[k]; named && !arrived {
			key = k
			break
		}
	}
	if key == "" {
		m.mu.Unlock()
		return
	}
	m.arrived[key] = true
	if !slices.Contains(slices.Collect(maps.Values(m.arrived)), false) {
		close(m.all)
	}
	m.mu.Unlock()
	select {
	case <-m.all:
	case <-time.After(2 * time.Second):
		m.mu.Lock()
		m.missed = true
		m.mu.Unlock()
	}
}

// met is true when every key arrived while the others were still in flight.
func (m *meeting) met() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.missed && !slices.Contains(slices.Collect(maps.Values(m.arrived)), false)
}

func newFakeGH() *fakeGH {
	return &fakeGH{
		repos: map[string]api.Repo{}, perms: map[string]string{}, listed: map[string][]string{},
		files: map[string][]byte{}, heads: map[string]string{}, checks: map[string][]api.CheckRun{},
		runs: map[string][]api.WorkflowRun{}, artifacts: map[int64][]byte{}, pulls: map[string]api.Pull{}, fail: map[string]error{},
	}
}

func (g *fakeGH) record(method string, r api.RepoRef, detail string) error {
	g.calls = append(g.calls, ghCall{method, r.InstallationID, r.FullName, detail})
	if err := g.fail[method+" "+r.FullName]; err != nil {
		return err
	}
	if err := g.fail[method+" "+detail]; detail != "" && err != nil {
		return err
	}
	return g.fail[method]
}

func (g *fakeGH) called(method string) []ghCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ghCall
	for _, c := range g.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (g *fakeGH) setFile(fullName, ref, path, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[fullName+"@"+ref+":"+path] = []byte(content)
}

func (g *fakeGH) deleteFile(fullName, ref, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.files, fullName+"@"+ref+":"+path)
}

func (g *fakeGH) Permission(_ context.Context, userToken, fullName string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("Permission", api.RepoRef{FullName: fullName}, ""); err != nil {
		return "", err
	}
	if p, ok := g.perms[userToken+" "+fullName]; ok {
		return p, nil
	}
	return "none", nil
}

func (g *fakeGH) UserRepos(_ context.Context, userToken string) ([]api.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("UserRepos", api.RepoRef{}, ""); err != nil {
		return nil, err
	}
	var out []api.Repo
	for _, name := range g.listed[userToken] {
		if r, ok := g.repos[name]; ok && g.perms[userToken+" "+name] != "" && g.perms[userToken+" "+name] != "none" {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListInstallations answers one installation per installation ID of the repositories, its account the owner.
func (g *fakeGH) ListInstallations(context.Context) ([]api.Installation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("ListInstallations", api.RepoRef{}, ""); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	out := []api.Installation{}
	for _, r := range g.repos {
		if !seen[r.InstallationID] {
			seen[r.InstallationID] = true
			out = append(out, api.Installation{ID: r.InstallationID, Account: ownerOf(r.FullName), AccountType: "Organization",
				CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.InstallationID) * time.Hour)})
		}
	}
	slices.SortFunc(out, func(a, b api.Installation) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (g *fakeGH) ListRepos(_ context.Context, installationID int64) ([]api.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("ListRepos", api.RepoRef{InstallationID: installationID}, ""); err != nil {
		return nil, err
	}
	var out []api.Repo
	for _, r := range g.repos {
		if r.InstallationID == installationID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (g *fakeGH) ResolveRepo(_ context.Context, fullName string) (api.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.repos[fullName]; ok {
		return r, nil
	}
	return api.Repo{}, api.ErrNotFound
}

func (g *fakeGH) File(_ context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	g.meet.arrive("File", ref+":"+path)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("File", r, ref+":"+path); err != nil {
		return nil, "", err
	}
	if sha, ok := g.heads[r.FullName+"@"+ref]; ok {
		if c, ok := g.files[r.FullName+"@"+sha+":"+path]; ok {
			return c, "blob", nil
		}
	}
	c, ok := g.files[r.FullName+"@"+ref+":"+path]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return c, "blob", nil
}

func (g *fakeGH) BranchHead(_ context.Context, r api.RepoRef, branch string) (string, error) {
	g.meet.arrive("BranchHead", branch)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("BranchHead", r, branch); err != nil {
		return "", err
	}
	head, ok := g.heads[r.FullName+"@"+branch]
	if !ok {
		return "", api.ErrNotFound
	}
	return head, nil
}

func (g *fakeGH) CheckRuns(_ context.Context, r api.RepoRef, headSHA, name string) ([]api.CheckRun, error) {
	g.meet.arrive("CheckRuns", headSHA)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("CheckRuns", r, headSHA+"#"+name); err != nil {
		return nil, err
	}
	return g.checks[r.FullName+"@"+headSHA+"#"+name], nil
}

func (g *fakeGH) PullSummary(_ context.Context, r api.RepoRef, number int) (api.Pull, error) {
	g.meet.arrive("PullSummary", "")
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("PullSummary", r, strconv.Itoa(number)); err != nil {
		return api.Pull{}, err
	}
	p, ok := g.pulls[r.FullName+"#"+strconv.Itoa(number)]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return p, nil
}

// OpenPulls lists the open pull requests of a repository by number.
func (g *fakeGH) OpenPulls(_ context.Context, r api.RepoRef) ([]api.Pull, error) {
	g.meet.arrive("OpenPulls", "")
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("OpenPulls", r, ""); err != nil {
		return nil, err
	}
	var open []api.Pull
	for key, p := range g.pulls {
		if strings.HasPrefix(key, r.FullName+"#") && p.State == "open" {
			open = append(open, p)
		}
	}
	slices.SortFunc(open, func(a, b api.Pull) int { return b.Number - a.Number })
	return open, nil
}

func (g *fakeGH) Dispatch(_ context.Context, r api.RepoRef, workflowPath, ref string, inputs map[string]string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.record("Dispatch", r, fmt.Sprintf("%s@%s %v", workflowPath, ref, inputs))
}

// LatestRun filters as GitHub does: status matches a run's status or its conclusion.
func (g *fakeGH) LatestRun(_ context.Context, r api.RepoRef, workflowPath string, f api.RunFilter) (api.WorkflowRun, error) {
	g.meet.arrive("LatestRun", workflowPath)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("LatestRun", r, fmt.Sprintf("%s@%s event=%s status=%s", workflowPath, f.Branch, f.Event, f.Status)); err != nil {
		return api.WorkflowRun{}, err
	}
	for _, run := range g.runs[r.FullName+"@"+workflowPath+"@"+f.Branch] {
		if (f.Event == "" || run.Event == f.Event) && (f.Status == "" || run.Status == f.Status || run.Conclusion == f.Status) {
			return run, nil
		}
	}
	return api.WorkflowRun{}, api.ErrNotFound
}

// addRun makes run the newest run of workflow on branch.
func (g *fakeGH) addRun(fullName, workflowPath, branch string, run api.WorkflowRun) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := fullName + "@" + workflowPath + "@" + branch
	g.runs[key] = append([]api.WorkflowRun{run}, g.runs[key]...)
}

// ArtifactID names a run's artifact by the run's ID: the fake keeps one artifact per run.
func (g *fakeGH) ArtifactID(_ context.Context, r api.RepoRef, runID int64, name string) (int64, error) {
	g.meet.arrive("ArtifactID", "")
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("ArtifactID", r, fmt.Sprintf("%d %s", runID, name)); err != nil {
		return 0, err
	}
	if _, ok := g.artifacts[runID]; !ok {
		return 0, api.ErrNotFound
	}
	return runID, nil
}

func (g *fakeGH) ArtifactByID(_ context.Context, r api.RepoRef, id int64, maxBytes int64) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("ArtifactByID", r, fmt.Sprintf("%d %d", id, maxBytes)); err != nil {
		return nil, err
	}
	a, ok := g.artifacts[id]
	if !ok {
		return nil, api.ErrNotFound
	}
	return a, nil
}

type proposeCall struct {
	change     proposal.Change
	expectHead string
}

// fakeProposals keeps branches and draft pull requests in the fake GitHub, like proposal does on the real one.
type fakeProposals struct {
	mu       sync.Mutex
	gh       *fakeGH
	drafts   map[string]*proposal.Draft // full name + "@" + branch
	proposes []proposeCall
	readied  []int
	commits  int
	gets     int
	fail     map[string]error
	// pushAfter, when set, is a commit made on GitHub right after each Propose: the draft moves between ui's write
	// and its read-back.
	pushAfter string
}

func newFakeProposals(gh *fakeGH) *fakeProposals {
	return &fakeProposals{gh: gh, drafts: map[string]*proposal.Draft{}, fail: map[string]error{}}
}

// commit puts files on the branch as a new head; with no open draft it opens one.
func (p *fakeProposals) commit(fullName, branch string, files []api.FileChange) proposal.Draft {
	p.commits++
	head := sha(0xc0000 + p.commits)
	d := p.drafts[fullName+"@"+branch]
	if d == nil || !isOpen(*d) {
		d = &proposal.Draft{Number: len(p.drafts) + 1, State: proposal.DraftOpen}
		d.URL = fmt.Sprintf("https://github.com/%s/pull/%d", fullName, d.Number)
		p.drafts[fullName+"@"+branch] = d
	}
	d.HeadSHA = head
	p.gh.mu.Lock()
	p.gh.heads[fullName+"@"+branch] = head
	for _, f := range files {
		p.gh.files[fullName+"@"+head+":"+f.Path] = f.Content
	}
	p.gh.mu.Unlock()
	return *d
}

// push is a commit made on GitHub, outside ui.
func (p *fakeProposals) push(fullName, branch, policyText string) proposal.Draft {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.commit(fullName, branch, []api.FileChange{{Path: ".archfit.yaml", Content: []byte(policyText)}})
}

func (p *fakeProposals) Propose(_ context.Context, c proposal.Change, expectHeadSHA string) (proposal.Draft, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proposes = append(p.proposes, proposeCall{c, expectHeadSHA})
	if err := p.fail["Propose"]; err != nil {
		return proposal.Draft{}, err
	}
	if d := p.drafts[c.Repo.FullName+"@"+c.Branch]; d != nil && expectHeadSHA != "" && d.HeadSHA != expectHeadSHA {
		return proposal.Draft{}, proposal.ErrStale
	}
	d := p.commit(c.Repo.FullName, c.Branch, c.Files)
	if p.pushAfter != "" {
		p.commit(c.Repo.FullName, c.Branch, []api.FileChange{{Path: ".archfit.yaml", Content: []byte(p.pushAfter)}})
	}
	return d, nil
}

func (p *fakeProposals) Get(_ context.Context, r api.RepoRef, branch string) (proposal.Draft, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gets++
	if err := p.fail["Get"]; err != nil {
		return proposal.Draft{}, err
	}
	if d := p.drafts[r.FullName+"@"+branch]; d != nil {
		return *d, nil
	}
	return proposal.Draft{State: proposal.DraftNone}, nil
}

func (p *fakeProposals) SetBody(context.Context, api.RepoRef, int, string) error {
	return errors.New("ui must never call SetBody")
}

func (p *fakeProposals) Ready(_ context.Context, r api.RepoRef, number int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.fail["Ready"]; err != nil {
		return err
	}
	p.readied = append(p.readied, number)
	for _, d := range p.drafts {
		if d.Number == number {
			d.State = proposal.DraftInReview
		}
	}
	return nil
}

func (p *fakeProposals) writes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.proposes) + len(p.readied)
}

type fakeAccount struct {
	account.Account // the real account over the env's Store: ui calls Status here, and Admit and Unadmit there
	status          account.Status
	err             error
	installations   []int64
}

func (a *fakeAccount) Status(_ context.Context, installationID int64, _ time.Time) (account.Status, error) {
	a.installations = append(a.installations, installationID)
	return a.status, a.err
}

type fakeConsents struct {
	recorded map[int64]map[account.ConsentKind]account.Consent
	touched  []int64 // installation of every call
	writes   int
	readErr  error // the Store is down: Get and Record fail, Has is false
	// readErrAfterWrite fails every Get once something was written: a change saved but not read back.
	readErrAfterWrite error
}

func newFakeConsents() *fakeConsents {
	return &fakeConsents{recorded: map[int64]map[account.ConsentKind]account.Consent{}}
}

func (c *fakeConsents) Record(_ context.Context, installationID int64, consent account.Consent) error {
	c.touched = append(c.touched, installationID)
	if c.readErr != nil {
		return c.readErr
	}
	if _, ok := c.recorded[installationID][consent.Kind]; ok {
		return account.ErrConsentExists
	}
	c.writes++
	if c.recorded[installationID] == nil {
		c.recorded[installationID] = map[account.ConsentKind]account.Consent{}
	}
	c.recorded[installationID][consent.Kind] = consent
	return nil
}

func (c *fakeConsents) Revoke(_ context.Context, installationID int64, k account.ConsentKind) error {
	c.touched = append(c.touched, installationID)
	c.writes++
	delete(c.recorded[installationID], k)
	return nil
}

func (c *fakeConsents) Has(ctx context.Context, installationID int64, k account.ConsentKind) bool {
	_, ok, err := c.Get(ctx, installationID, k)
	return ok && err == nil
}

func (c *fakeConsents) Get(_ context.Context, installationID int64, k account.ConsentKind) (account.Consent, bool, error) {
	c.touched = append(c.touched, installationID)
	if c.readErr != nil {
		return account.Consent{}, false, c.readErr
	}
	if c.readErrAfterWrite != nil && c.writes > 0 {
		return account.Consent{}, false, c.readErrAfterWrite
	}
	got, ok := c.recorded[installationID][k]
	return got, ok, nil
}

type startCall struct {
	repo   api.Repo
	owners []string
}

type fakeOnboarding struct {
	progress onboarding.Progress
	err      error // of Progress
	panics   bool  // Progress panics
	startErr error
	customer []startCall
	managed  []startCall
	meet     *meeting // as fakeGH.meet, under the key "Progress"
}

func (o *fakeOnboarding) Progress(context.Context, api.Repo) (onboarding.Progress, error) {
	o.meet.arrive("Progress", "")
	if o.panics {
		panic("boom")
	}
	return o.progress, o.err
}

func (o *fakeOnboarding) StartCustomer(_ context.Context, r api.Repo, owners []string) (proposal.Draft, error) {
	o.customer = append(o.customer, startCall{r, owners})
	return proposal.Draft{}, o.startErr
}

func (o *fakeOnboarding) StartManaged(_ context.Context, r api.Repo, owners []string) (proposal.Draft, error) {
	o.managed = append(o.managed, startCall{r, owners})
	return proposal.Draft{}, o.startErr
}

func (o *fakeOnboarding) DiscoveryHandler() http.Handler {
	panic("ui must never mount DiscoveryHandler")
}

func (o *fakeOnboarding) BaselineHandler() http.Handler {
	panic("ui must never mount BaselineHandler")
}

func (o *fakeOnboarding) ReanchorHandler() http.Handler {
	panic("ui must never mount ReanchorHandler")
}

type assistCall struct {
	mode assist.Mode
	req  assist.Request
}

type fakeAssist struct {
	calls   []assistCall
	propose func(mode assist.Mode, r assist.Request) (assist.Result, error)
}

func (a *fakeAssist) Propose(_ context.Context, mode assist.Mode, r assist.Request) (assist.Result, error) {
	a.calls = append(a.calls, assistCall{mode, r})
	if a.propose == nil {
		return assist.Result{Mode: mode}, nil
	}
	return a.propose(mode, r)
}

// fakeOAuth exchanges the code "code-<login>" for that login's token.
type fakeOAuth struct {
	tokens map[string]auth.UserToken
}

func (o *fakeOAuth) AuthorizeURL(state, redirectURL string) string {
	return "https://github.example/login/oauth/authorize?state=" + state + "&redirect_uri=" + redirectURL
}

func (o *fakeOAuth) Exchange(_ context.Context, code string) (auth.UserToken, error) {
	t, ok := o.tokens[code]
	if !ok {
		return auth.UserToken{}, errors.New("bad code")
	}
	return t, nil
}

// downable fails every read while down is set; the approvals are read through it.
type downable struct {
	store.Store
	down atomic.Bool
}

func (d *downable) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if d.down.Load() {
		return nil, errors.New("fake: store down")
	}
	return d.Store.Get(ctx, t, key)
}

// approveCall is one Approve that reached the gate.
type approveCall struct {
	target gate.Target
	login  string
	seen   map[string]string
}

// fakeApprovals is the gate façade's owner approval. Approve records the call and, unless refused, grants the head;
// the rules of who may approve are the gate's and tested there.
type fakeApprovals struct {
	mu            sync.Mutex
	approval      gate.PullApproval // what PullApproval answers
	readErr       error
	approveErr    error // a refusal or a failure of Approve
	reevaluateErr error
	reads         []approvalRead
	approved      []approveCall
	reevaluated   []gate.Target
}

type approvalRead struct {
	target        gate.Target
	login, author string
}

func (f *fakeApprovals) PullApproval(_ context.Context, t gate.Target, login, author string) (gate.PullApproval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, approvalRead{t, login, author})
	return f.approval, f.readErr
}

func (f *fakeApprovals) Approve(_ context.Context, t gate.Target, login string, seen map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.approveErr != nil {
		return f.approveErr
	}
	f.approved = append(f.approved, approveCall{t, login, seen})
	f.approval.Granted, f.approval.ApprovedBy, f.approval.Refusal = true, login, nil
	return nil
}

func (f *fakeApprovals) Reevaluate(_ context.Context, t gate.Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reevaluated = append(f.reevaluated, t)
	return f.reevaluateErr
}

type decideCall struct {
	target                 gate.Target
	login, finding, digest string
}

type approvedCall struct {
	target          gate.Target
	finding, digest string
	head            string
	proposalPR      int
}

// fakeDecisions is the gate façade's decision requests. The rules of who may decide and when a request is stale are
// the gate's and tested there; the fake answers what a test sets and records what ui asked.
type fakeDecisions struct {
	mu           sync.Mutex
	rows         []gate.DecisionRequest // what DecisionRequests answers
	policy       string                 // the candidate policy ApproveDecision hands back
	listErr      error
	approveErr   error
	approvedErr  error // set for ApprovedDecision's first call only when approvedOnce
	declineErr   error
	approvedOnce bool

	listed   []decideCall
	approves []decideCall
	approved []approvedCall
	declined []decideCall
}

func (f *fakeDecisions) DecisionRequests(_ context.Context, t gate.Target, login string) ([]gate.DecisionRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = append(f.listed, decideCall{target: t, login: login})
	return slices.Clone(f.rows), f.listErr
}

func (f *fakeDecisions) ApproveDecision(_ context.Context, t gate.Target, login, finding, digest string) (gate.DecisionCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approves = append(f.approves, decideCall{t, login, finding, digest})
	if f.approveErr != nil {
		return gate.DecisionCase{}, f.approveErr
	}
	for _, r := range f.rows {
		if r.FindingID == finding && r.PolicyDigest == digest {
			return gate.DecisionCase{Record: r.DecisionRecord, Policy: f.policy, Branch: proposalBranchOf(r.DecisionRecord)}, nil
		}
	}
	return gate.DecisionCase{}, gate.ErrDecisionNotFound
}

func (f *fakeDecisions) ApprovedDecision(_ context.Context, t gate.Target, finding, digest, head string, proposalPR int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.approvedErr != nil {
		err := f.approvedErr
		if f.approvedOnce {
			f.approvedErr = nil
		}
		return err
	}
	f.approved = append(f.approved, approvedCall{t, finding, digest, head, proposalPR})
	f.settle(finding, digest, decision.StatusApproved, "", proposalPR)
	return nil
}

func (f *fakeDecisions) DeclineDecision(_ context.Context, t gate.Target, login, finding, digest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.declineErr != nil {
		return f.declineErr
	}
	f.declined = append(f.declined, decideCall{t, login, finding, digest})
	f.settle(finding, digest, decision.StatusDeclined, decision.ReasonOwnerDeclined, 0)
	return nil
}

func (f *fakeDecisions) settle(finding, digest string, status decision.Status, reason string, pr int) {
	for i, r := range f.rows {
		if r.FindingID == finding && r.PolicyDigest == digest {
			f.rows[i].Status, f.rows[i].Reason, f.rows[i].ProposalPR, f.rows[i].CanDecide = status, reason, pr, false
		}
	}
}

// proposalBranchOf is the branch the gate names for a request: its pull request and a suffix only the gate knows.
func proposalBranchOf(r gate.DecisionRecord) string {
	return "archfit/decision-" + strconv.Itoa(r.PullRequest) + "-" + strings.Repeat("c", 26)
}
