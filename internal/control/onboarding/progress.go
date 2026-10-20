package onboarding

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// state is what one pass over GitHub says about a repository. Nothing of it is kept.
type state struct {
	stage           Stage
	draft           *proposal.Draft // open PR of the branch that belongs to the stage
	settingsInvalid bool
	pull            api.Pull // the open onboarding PR; zero unless stage is installed and draft is set
}

func (s *service) Progress(ctx context.Context, r api.Repo) (Progress, error) {
	st, err := s.derive(ctx, r)
	if err != nil {
		return Progress{}, err
	}
	return Progress{Stage: st.stage, Diagnostic: st.diagnostic(s.now), OpenDraft: st.draft}, nil
}

func (s *service) derive(ctx context.Context, r api.Repo) (state, error) {
	// The settings name the workflow, its latest run the commit whose check says "reported". The last derive left
	// both as a hint: every read starts beside the settings', and the hinted ones count only when the fresh settings
	// and run name the same. Warm, the progress waits for one read, not for three.
	key := hintKey{r.InstallationID, r.FullName}
	h, hinted := s.hints.get(key)
	fileFound := func(path string) (bool, error) {
		_, found, err := s.file(ctx, r, path)
		return found, err
	}
	var (
		raw                      []byte
		found                    bool
		err                      error
		workflowFile, policyFile guess[string, bool]
		latest                   guess[string, api.WorkflowRun]
		checked                  guess[string, bool]
	)
	reads := []func(){func() { raw, found, err = s.file(ctx, r, settings.Path) }}
	if hinted {
		reads = append(reads,
			workflowFile.ahead(h.workflow, fileFound),
			policyFile.ahead(policy.PolicyPath, fileFound),
			latest.ahead(h.workflow, s.latestRun(ctx, r)))
		if h.head != "" {
			reads = append(reads, checked.ahead(h.head, s.stateChecked(ctx, r)))
		}
	}
	together(reads...)
	if err != nil {
		return state{}, fmt.Errorf("onboarding: read settings: %w", err)
	}
	set, parseErr := settings.Parse(raw)
	if !found || parseErr != nil {
		s.hints.drop(key)
		return s.installed(ctx, r, found && parseErr != nil)
	}

	// The three reads run side by side and are judged in order: the policy and the report count only once the
	// workflow is there, so their errors never fail a repository that has none.
	var (
		workflowFound, policyFound   bool
		reported                     bool
		head                         string
		workflowErr, policyErr, rErr error
	)
	together(
		func() { workflowFound, workflowErr = workflowFile.at(set.Workflow, fileFound) },
		func() { policyFound, policyErr = policyFile.at(policy.PolicyPath, fileFound) },
		func() { reported, head, rErr = s.reported(ctx, r, set.Workflow, &latest, &checked) },
	)
	if rErr == nil {
		s.hints.put(key, hint{workflow: set.Workflow, head: head})
	}
	switch {
	case workflowErr != nil:
		return state{}, fmt.Errorf("onboarding: read workflow: %w", workflowErr)
	case !workflowFound:
		return s.installed(ctx, r, false)
	case policyErr != nil:
		return state{}, fmt.Errorf("onboarding: read policy: %w", policyErr)
	case rErr != nil:
		return state{}, rErr
	}
	if policyFound {
		switch {
		case !reported:
			return state{stage: StagePolicyMerged}, nil
		case set.Gate == settings.GateRequired:
			return state{stage: StageRequired}, nil
		}
		return state{stage: StageAdvisory}, nil
	}

	st := state{stage: StageWorkflowMerged}
	if reported {
		st.stage = StageReportReceived
	}
	st.draft, err = s.openDraft(ctx, r, policyBranch)
	return st, err
}

func (s *service) installed(ctx context.Context, r api.Repo, settingsInvalid bool) (state, error) {
	st := state{stage: StageInstalled, settingsInvalid: settingsInvalid}
	var err error
	if st.draft, err = s.openDraft(ctx, r, onboardingBranch); err != nil || st.draft == nil {
		return st, err
	}
	// The PR body carries the managed job state, so every instance reads the same one.
	st.pull, err = s.gh.pulls.PullByHead(ctx, r.RepoRef, onboardingBranch)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return state{}, fmt.Errorf("onboarding: read onboarding pull request: %w", err)
	}
	return st, nil
}

// reported is true when the App accepted an upload of the latest completed default-branch push run; head is that
// run's head SHA, "" without a run. latest and checked are the reads derive made ahead; each counts only for the
// workflow and the head read here.
func (s *service) reported(ctx context.Context, r api.Repo, workflow string,
	latest *guess[string, api.WorkflowRun], checked *guess[string, bool],
) (ok bool, head string, err error) {
	run, err := latest.at(workflow, s.latestRun(ctx, r))
	switch {
	case errors.Is(err, api.ErrNotFound):
		return false, "", nil
	case err != nil:
		return false, "", fmt.Errorf("onboarding: latest run: %w", err)
	}
	ok, err = checked.at(run.HeadSHA, s.stateChecked(ctx, r))
	if err != nil {
		return false, "", fmt.Errorf("onboarding: state check: %w", err)
	}
	return ok, run.HeadSHA, nil
}

// latestRun reads a workflow's latest completed default-branch push run. Push only: archfit/state is published for a
// report upload, and on the default branch only a push run uploads one. A dispatched discovery, baseline or re-anchor
// run never does, so it would hide an accepted report. Completed: a run in progress has not uploaded yet. Its
// conclusion does not matter; the check is the evidence.
func (s *service) latestRun(ctx context.Context, r api.Repo) func(workflow string) (api.WorkflowRun, error) {
	f := api.RunFilter{Branch: r.DefaultBranch, Event: eventPush, Status: runCompleted}
	return func(workflow string) (api.WorkflowRun, error) {
		return s.gh.actions.LatestRun(ctx, r.RepoRef, workflow, f)
	}
}

// stateChecked reads whether a commit carries the archfit/state check.
func (s *service) stateChecked(ctx context.Context, r api.Repo) func(head string) (bool, error) {
	return func(head string) (bool, error) {
		checks, err := s.gh.checks.CheckRuns(ctx, r.RepoRef, head, stateCheckName)
		return len(checks) > 0, err
	}
}

func (s *service) openDraft(ctx context.Context, r api.Repo, branch string) (*proposal.Draft, error) {
	d, err := s.proposals.Get(ctx, r.RepoRef, branch)
	if err != nil {
		return nil, fmt.Errorf("onboarding: draft of %s: %w", branch, err)
	}
	if d.State != proposal.DraftOpen && d.State != proposal.DraftInReview {
		return nil, nil
	}
	return &d, nil
}

func (st state) diagnostic(now func() time.Time) string {
	switch st.stage {
	case StageInstalled:
		switch {
		case st.draft == nil && st.settingsInvalid:
			return "`" + settings.Path + "` on the default branch is invalid"
		case st.draft == nil:
			return "Start onboarding"
		}
		if d := managedDiagnostic(st.pull, now()); d != "" {
			return d
		}
		return fmt.Sprintf("Review and merge PR #%d", st.draft.Number)
	case StageWorkflowMerged, StageReportReceived:
		if st.draft != nil {
			return fmt.Sprintf("Review and merge the policy PR #%d", st.draft.Number)
		}
		return "Run discovery (workflow_dispatch with `mode` set to `discovery`)"
	case StagePolicyMerged:
		return "Waiting for the first run on the default branch"
	case StageRequired:
		return "Required gate is active"
	}
	return "Advisory feedback is active; an owner enables the gate by PR"
}

// together runs fns side by side and returns when all have. A panic in one is raised again on the caller's goroutine
// once the others are done, so the caller's recover still sees it. ui keeps the same helper: two small copies cost less
// than a package of their own.
func together(fns ...func()) {
	var (
		wg     sync.WaitGroup
		once   sync.Once
		thrown any
	)
	for _, fn := range fns {
		wg.Go(func() {
			defer func() {
				if p := recover(); p != nil {
					once.Do(func() { thrown = p })
				}
			}()
			fn()
		})
	}
	wg.Wait()
	if thrown != nil {
		panic(thrown)
	}
}
