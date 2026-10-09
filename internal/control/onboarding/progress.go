package onboarding

import (
	"context"
	"errors"
	"fmt"
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
	raw, found, err := s.file(ctx, r, settings.Path)
	if err != nil {
		return state{}, fmt.Errorf("onboarding: read settings: %w", err)
	}
	set, parseErr := settings.Parse(raw)
	workflowFound := false
	if found && parseErr == nil {
		if _, workflowFound, err = s.file(ctx, r, set.Workflow); err != nil {
			return state{}, fmt.Errorf("onboarding: read workflow: %w", err)
		}
	}
	if !workflowFound {
		return s.installed(ctx, r, found && parseErr != nil)
	}

	_, policyFound, err := s.file(ctx, r, policy.PolicyPath)
	if err != nil {
		return state{}, fmt.Errorf("onboarding: read policy: %w", err)
	}
	reported, err := s.reported(ctx, r, set.Workflow)
	if err != nil {
		return state{}, err
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

// reported is true when the App accepted an upload of the latest default-branch run.
func (s *service) reported(ctx context.Context, r api.Repo, workflow string) (bool, error) {
	run, err := s.gh.actions.LatestRun(ctx, r.RepoRef, workflow, r.DefaultBranch)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("onboarding: latest run: %w", err)
	}
	checks, err := s.gh.checks.CheckRuns(ctx, r.RepoRef, run.HeadSHA, stateCheckName)
	if err != nil {
		return false, fmt.Errorf("onboarding: state check: %w", err)
	}
	return len(checks) > 0, nil
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
