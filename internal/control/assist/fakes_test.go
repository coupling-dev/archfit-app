package assist

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	testBranch  = "archfit/policy"
	otherBranch = "archfit/other"
	mainBranch  = "main"

	draftYAML  = "version: 1\nrules:\n  max_fan_out: 10\n"
	editedYAML = "version: 1\nrules:\n  max_fan_out: 7\n"

	testMaxCost    = int64(90_000)
	testActualCost = int64(7_000)
)

var (
	headA = strings.Repeat("a", 40)
	headB = strings.Repeat("b", 40)
	headC = strings.Repeat("c", 40)

	testRepo = api.Repo{
		RepoRef:       api.RepoRef{InstallationID: 11, FullName: "acme/shop"},
		ID:            42,
		DefaultBranch: mainBranch,
	}
	testNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	managedSettings  = string(renderSettings(settings.DataManaged))
	customerSettings = string(renderSettings(settings.DataCustomer))
)

func renderSettings(mode settings.DataMode) []byte {
	return settings.Render(settings.Settings{
		Version:  1,
		Owners:   []string{"alice"},
		Gate:     settings.GateAdvisory,
		Workflow: ".github/workflows/archfit.yaml",
		DataMode: mode,
	})
}

type fileKey struct{ ref, path string }

// fakeRepos embeds the nil interface: any method other than File panics, which proves File is the only one used.
type fakeRepos struct {
	api.Repos
	mu    sync.Mutex
	files map[fileKey][]byte
	errs  map[fileKey]error
	reads []fileKey
}

func (f *fakeRepos) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := fileKey{ref, path}
	f.reads = append(f.reads, k)
	if err := f.errs[k]; err != nil {
		return nil, "", err
	}
	content, ok := f.files[k]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return content, "blob", nil
}

type dispatchCall struct {
	repo         api.RepoRef
	workflowPath string
	ref          string
	inputs       map[string]string
}

type fakeActions struct {
	api.Actions
	err   error
	calls []dispatchCall
}

func (f *fakeActions) Dispatch(_ context.Context, r api.RepoRef, workflowPath, ref string, inputs map[string]string) error {
	f.calls = append(f.calls, dispatchCall{r, workflowPath, ref, inputs})
	return f.err
}

// fakeConsents stands for account over a Store: down means the Store fails, so Has is false.
type fakeConsents struct {
	mu      sync.Mutex
	granted map[account.ConsentKind]bool
	down    bool
	calls   int
	writes  int
}

func (f *fakeConsents) Record(_ context.Context, _ int64, c account.Consent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	f.granted[c.Kind] = true
	return nil
}

func (f *fakeConsents) Revoke(_ context.Context, _ int64, k account.ConsentKind) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	delete(f.granted, k)
	return nil
}

func (f *fakeConsents) Has(_ context.Context, _ int64, k account.ConsentKind) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return !f.down && f.granted[k]
}

type settleCall struct {
	reserved, actual int64
	ctxErr           error
	hasDeadline      bool
}

type fakeBudget struct {
	mu         sync.Mutex
	reserveErr error
	settleErr  error
	reserves   []int64
	settles    []settleCall
	times      []time.Time
	months     map[string]int64 // one counter per UTC month of now, as account keeps them
}

func (f *fakeBudget) book(now time.Time, delta int64) {
	if f.months == nil {
		f.months = map[string]int64{}
	}
	f.months[now.UTC().Format("2006-01")] += delta
}

func (f *fakeBudget) Reserve(_ context.Context, maxCost int64, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserves = append(f.reserves, maxCost)
	f.times = append(f.times, now)
	f.book(now, maxCost)
	return f.reserveErr
}

func (f *fakeBudget) Settle(ctx context.Context, reserved, actual int64, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, hasDeadline := ctx.Deadline()
	f.settles = append(f.settles, settleCall{reserved, actual, ctx.Err(), hasDeadline})
	f.times = append(f.times, now)
	f.book(now, actual-reserved)
	return f.settleErr
}

func (f *fakeBudget) reserveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reserves)
}

type fakeProvider struct {
	mu       sync.Mutex
	max      int64
	complete func(ctx context.Context, p Prompt) (Completion, error)
	prompts  []Prompt
}

func (f *fakeProvider) MaxCostMicroUSD(Prompt) int64 { return f.max }

func (f *fakeProvider) Complete(ctx context.Context, p Prompt) (Completion, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	f.mu.Unlock()
	return f.complete(ctx, p)
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

func answers(text string, cost int64) func(context.Context, Prompt) (Completion, error) {
	return func(context.Context, Prompt) (Completion, error) {
		return Completion{Text: text, CostMicroUSD: cost}, nil
	}
}

type proposeCall struct {
	change     proposal.Change
	expectHead string
}

// fakeProposals models one draft branch per name with proposal's head-binding rule:
// an expected head must match an open pull request's head, else ErrStale and nothing is written.
type fakeProposals struct {
	mu         sync.Mutex
	drafts     map[string]proposal.Draft
	getErr     error
	proposeErr error
	attempts   int
	commits    []proposeCall
	otherCalls int
}

func (f *fakeProposals) Get(_ context.Context, _ api.RepoRef, branch string) (proposal.Draft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return proposal.Draft{}, f.getErr
	}
	d, ok := f.drafts[branch]
	if !ok {
		return proposal.Draft{State: proposal.DraftNone}, nil
	}
	return d, nil
}

func (f *fakeProposals) Propose(_ context.Context, c proposal.Change, expectHeadSHA string) (proposal.Draft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.proposeErr != nil {
		return proposal.Draft{}, f.proposeErr
	}
	d := f.drafts[c.Branch]
	open := d.State == proposal.DraftOpen || d.State == proposal.DraftInReview
	if expectHeadSHA != "" && (!open || d.HeadSHA != expectHeadSHA) {
		return proposal.Draft{}, proposal.ErrStale
	}
	f.commits = append(f.commits, proposeCall{c, expectHeadSHA})
	d.HeadSHA = headB
	f.drafts[c.Branch] = d
	return d, nil
}

func (f *fakeProposals) SetBody(context.Context, api.RepoRef, int, string) error {
	f.otherCalls++
	return nil
}

func (f *fakeProposals) Ready(context.Context, api.RepoRef, int) error {
	f.otherCalls++
	return nil
}

func (f *fakeProposals) setDraft(branch string, d proposal.Draft) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drafts[branch] = d
}

func (f *fakeProposals) draft(branch string) proposal.Draft {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drafts[branch]
}

func (f *fakeProposals) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// fixture is a repository where a managed request succeeds; tests break one thing at a time.
type fixture struct {
	repos      *fakeRepos
	actions    *fakeActions
	consents   *fakeConsents
	budget     *fakeBudget
	provider   *fakeProvider
	proposals  *fakeProposals
	noProvider bool
	now        func() time.Time // nil: the clock stands at testNow
}

func newFixture() *fixture {
	return &fixture{
		repos: &fakeRepos{
			files: map[fileKey][]byte{
				{mainBranch, settings.Path}:     []byte(managedSettings),
				{headA, policy.PolicyPath}:      []byte(draftYAML),
				{mainBranch, policy.PolicyPath}: []byte(draftYAML),
			},
			errs: map[fileKey]error{},
		},
		actions:  &fakeActions{},
		consents: &fakeConsents{granted: map[account.ConsentKind]bool{account.ConsentManagedLLM: true}},
		budget:   &fakeBudget{},
		provider: &fakeProvider{max: testMaxCost, complete: answers(editedYAML, testActualCost)},
		proposals: &fakeProposals{drafts: map[string]proposal.Draft{
			testBranch:  {Number: 7, State: proposal.DraftOpen, HeadSHA: headA, URL: "https://github.com/acme/shop/pull/7"},
			otherBranch: {Number: 8, State: proposal.DraftOpen, HeadSHA: headA, URL: "https://github.com/acme/shop/pull/8"},
		}},
	}
}

func (f *fixture) assist() Assist {
	now := f.now
	if now == nil {
		now = func() time.Time { return testNow }
	}
	d := Deps{
		Consents:  f.consents,
		Budget:    f.budget,
		Proposals: f.proposals,
		Now:       now,
	}
	if !f.noProvider {
		d.Provider = f.provider
	}
	return newService(d, github{repos: f.repos, actions: f.actions})
}

func validRequest() Request {
	return Request{Repo: testRepo, Branch: testBranch, HeadSHA: headA, Instruction: "lower the fan-out limit to 7", By: "alice"}
}

// captureLogs swaps slog.Default; tests that use it must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
