package assist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	completeTimeout = 150 * time.Second
	settleTimeout   = 5 * time.Second

	inputBranch      = "branch"
	inputHeadSHA     = "head_sha"
	inputInstruction = "instruction"

	runsPagePrefix = "https://github.com/"
	runsPageInfix  = "/actions/workflows/"
)

// github holds the two github/api roles assist uses: Repos for File, Actions for Dispatch.
type github struct {
	repos   api.Repos
	actions api.Actions
}

type slot struct {
	repoID int64
	branch string
}

type service struct {
	gh        github
	provider  Provider
	consents  account.Consents
	budget    account.Budget
	proposals proposal.Proposals
	now       func() time.Time

	// The in-flight guard is per process. Ceiling: with several replicas two requests for one branch
	// can run in parallel; the head binding and the budget still hold. Upgrade trigger: a second replica.
	mu       sync.Mutex
	inflight map[slot]struct{}
}

func newService(d Deps, gh github) *service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &service{
		gh:        gh,
		provider:  d.Provider,
		consents:  d.Consents,
		budget:    d.Budget,
		proposals: d.Proposals,
		now:       now,
		inflight:  map[slot]struct{}{},
	}
}

// stats is what one request may leave in the log: lengths and cost, never text.
type stats struct {
	policyBytes  int
	costMicroUSD int64
}

func (s *service) Propose(ctx context.Context, mode Mode, r Request) (Result, error) {
	start := s.now()
	var st stats
	res, err := s.propose(ctx, mode, r, &st)
	slog.Default().InfoContext(ctx, "assist: propose",
		"installation_id", r.Repo.InstallationID,
		"repo_id", r.Repo.ID,
		"mode", string(mode),
		"by", r.By,
		"outcome", outcome(err),
		"policy_bytes", st.policyBytes,
		"instruction_bytes", len(r.Instruction),
		"cost_micro_usd", st.costMicroUSD,
		"duration_ms", s.now().Sub(start).Milliseconds(),
	)
	return res, err
}

func (s *service) propose(ctx context.Context, mode Mode, r Request, st *stats) (Result, error) {
	if err := validate(mode, r); err != nil {
		return Result{}, err
	}
	if mode == ModeManaged {
		if err := s.managedAllowed(ctx, r.Repo); err != nil {
			return Result{}, err
		}
	}
	draft, err := s.openDraft(ctx, r)
	if err != nil {
		return Result{}, err
	}
	if mode == ModeCustomer {
		return s.dispatch(ctx, r, draft)
	}
	return s.managed(ctx, r, st)
}

// managedAllowed runs the free managed-mode checks that need no draft: provider, consent, settings.
func (s *service) managedAllowed(ctx context.Context, repo api.Repo) error {
	if s.provider == nil {
		return ErrManagedUnavailable
	}
	if !s.consents.Has(ctx, repo.InstallationID, account.ConsentManagedLLM) {
		return account.ErrNoConsent
	}
	// Settings come from the default branch only: the draft branch is head-tree configuration and
	// could switch the data mode without owner approval.
	raw, _, err := s.gh.repos.File(ctx, repo.RepoRef, repo.DefaultBranch, settings.Path)
	if errors.Is(err, api.ErrNotFound) {
		return fmt.Errorf("%w: settings file missing", ErrNotManaged)
	}
	if err != nil {
		return fmt.Errorf("assist: read settings: %w", err)
	}
	set, err := settings.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: settings file invalid", ErrNotManaged)
	}
	if set.DataMode != settings.DataManaged {
		return ErrNotManaged
	}
	return nil
}

func (s *service) openDraft(ctx context.Context, r Request) (proposal.Draft, error) {
	draft, err := s.proposals.Get(ctx, r.Repo.RepoRef, r.Branch)
	if errors.Is(err, api.ErrNotFound) {
		return proposal.Draft{}, ErrNoDraft
	}
	if err != nil {
		return proposal.Draft{}, fmt.Errorf("assist: read draft: %w", err)
	}
	if draft.State != proposal.DraftOpen && draft.State != proposal.DraftInReview {
		return proposal.Draft{}, fmt.Errorf("%w: state %s", ErrNoDraft, draft.State)
	}
	// After the state check: a caller without a draft has no head to send and must get ErrNoDraft.
	if !proposal.ValidHeadSHA(r.HeadSHA) {
		return proposal.Draft{}, fmt.Errorf("%w: head_sha", ErrInvalidRequest)
	}
	if draft.HeadSHA != r.HeadSHA {
		return proposal.Draft{}, fmt.Errorf("assist: %w", proposal.ErrStale)
	}
	return draft, nil
}

func (s *service) dispatch(ctx context.Context, r Request, draft proposal.Draft) (Result, error) {
	err := s.gh.actions.Dispatch(ctx, r.Repo.RepoRef, AssistWorkflowPath, r.Branch, map[string]string{
		inputBranch:      r.Branch,
		inputHeadSHA:     r.HeadSHA,
		inputInstruction: r.Instruction,
	})
	if errors.Is(err, api.ErrNotFound) {
		return Result{}, ErrNoAssistWorkflow
	}
	if err != nil {
		return Result{}, fmt.Errorf("assist: dispatch workflow: %w", err)
	}
	return Result{
		Mode:   ModeCustomer,
		Draft:  draft,
		RunURL: runsPagePrefix + r.Repo.FullName + runsPageInfix + path.Base(AssistWorkflowPath),
	}, nil
}

func (s *service) managed(ctx context.Context, r Request, st *stats) (Result, error) {
	draftPolicy, _, err := s.gh.repos.File(ctx, r.Repo.RepoRef, r.HeadSHA, policy.PolicyPath)
	if errors.Is(err, api.ErrNotFound) {
		return Result{}, fmt.Errorf("%w: policy file missing", ErrNoDraft)
	}
	if err != nil {
		return Result{}, fmt.Errorf("assist: read draft policy: %w", err)
	}
	st.policyBytes = len(draftPolicy)
	if len(draftPolicy) > maxPolicyBytes {
		return Result{}, ErrPolicyTooLarge
	}

	key := slot{repoID: r.Repo.ID, branch: r.Branch}
	if !s.acquire(key) {
		return Result{}, ErrBusy
	}
	defer s.release(key)

	text, err := s.complete(ctx, buildPrompt(policy.Schema(), draftPolicy, r.Instruction), st)
	if err != nil {
		return Result{}, err
	}
	answer, err := normalizeAnswer(text, draftPolicy)
	if err != nil {
		return Result{}, err
	}

	message := "archfit: AI proposal requested by @" + r.By
	draft, err := s.proposals.Propose(ctx, proposal.Change{
		Repo:    r.Repo.RepoRef,
		Branch:  r.Branch,
		Base:    r.Repo.DefaultBranch,
		Title:   message,
		Body:    message,
		Message: message,
		Files:   []api.FileChange{{Path: policy.PolicyPath, Content: answer}},
	}, r.HeadSHA)
	if err != nil {
		return Result{}, fmt.Errorf("assist: commit proposal: %w", err)
	}
	return Result{Mode: ModeManaged, Draft: draft}, nil
}

// complete is the reservation protocol: reserve the worst case, call the provider once, settle with
// the cost the provider reports, whatever happened to the call or to the request.
func (s *service) complete(ctx context.Context, p Prompt, st *stats) (string, error) {
	reserved := s.provider.MaxCostMicroUSD(p)
	// One instant for Reserve and Settle: account keys the counter by the UTC month of now, and a
	// second read after the provider call could land in the next month.
	now := s.now()
	if err := s.budget.Reserve(ctx, reserved, now); err != nil {
		return "", fmt.Errorf("assist: reserve budget: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, completeTimeout)
	completion, callErr := s.provider.Complete(callCtx, p)
	cancel()
	st.costMicroUSD = completion.CostMicroUSD

	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := s.budget.Settle(settleCtx, reserved, completion.CostMicroUSD, now); err != nil {
		// The full reservation stays booked: the error is on the safe side of the cap.
		slog.Default().ErrorContext(settleCtx, "assist: settle budget failed",
			"reserved_micro_usd", reserved, "actual_micro_usd", completion.CostMicroUSD, "error", err)
	}

	if callErr != nil {
		// The provider's error text is not trusted to be free of model output: keep the kind only.
		return "", fmt.Errorf("%w: %s", ErrProvider, failureKind(callErr))
	}
	return completion.Text, nil
}

func failureKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "error"
	}
}

func (s *service) acquire(key slot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.inflight[key]; busy {
		return false
	}
	s.inflight[key] = struct{}{}
	return true
}

func (s *service) release(key slot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, key)
}

// outcome is the stable log label of a Propose result.
func outcome(err error) string {
	labels := []struct {
		err   error
		label string
	}{
		{ErrInvalidRequest, "invalid_request"},
		{ErrNoDraft, "no_draft"},
		{proposal.ErrStale, "stale"},
		{ErrManagedUnavailable, "managed_unavailable"},
		{account.ErrNoConsent, "no_consent"},
		{ErrNotManaged, "not_managed"},
		{ErrPolicyTooLarge, "policy_too_large"},
		{ErrBusy, "busy"},
		{account.ErrBudgetExhausted, "budget_exhausted"},
		{ErrProvider, "provider_failed"},
		{ErrInvalidProposal, "invalid_proposal"},
		{ErrNoAssistWorkflow, "no_assist_workflow"},
		{api.ErrRateLimited, "rate_limited"},
	}
	if err == nil {
		return "ok"
	}
	for _, l := range labels {
		if errors.Is(err, l.err) {
			return l.label
		}
	}
	return "error"
}
