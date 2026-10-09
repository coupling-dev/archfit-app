package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	onboardingTitle   = "Add Archfit advisory feedback"
	onboardingMessage = "Add Archfit workflow, settings and CODEOWNERS entries"
)

func (s *service) StartCustomer(ctx context.Context, r api.Repo, owners []string) (proposal.Draft, error) {
	set, err := newSettings(owners, settings.DataCustomer)
	if err != nil {
		return proposal.Draft{}, err
	}
	st, err := s.derive(ctx, r)
	if err != nil {
		return proposal.Draft{}, err
	}
	if st.stage != StageInstalled {
		return proposal.Draft{}, ErrAlreadyOnboarded
	}
	return s.proposeOnboarding(ctx, r, set)
}

// newSettings is the only place settings are generated: the gate is always advisory.
func newSettings(owners []string, mode settings.DataMode) (settings.Settings, error) {
	if len(owners) == 0 {
		return settings.Settings{}, errNoOwners
	}
	set := settings.Settings{Version: 1, Owners: owners, Gate: settings.GateAdvisory, Workflow: workflowPath, DataMode: mode}
	if _, err := settings.Parse(settings.Render(set)); err != nil {
		return settings.Settings{}, fmt.Errorf("onboarding: owners: %w", err)
	}
	return set, nil
}

// proposeOnboarding opens or updates PR 1: workflow, settings and CODEOWNERS entries.
func (s *service) proposeOnboarding(ctx context.Context, r api.Repo, set settings.Settings) (proposal.Draft, error) {
	workflow, err := renderWorkflow(manifest.Current(), r.DefaultBranch, s.baseURL)
	if err != nil {
		return proposal.Draft{}, fmt.Errorf("onboarding: render workflow: %w", err)
	}
	ownersPath, ownersFile, err := s.codeowners(ctx, r, set.Owners)
	if err != nil {
		return proposal.Draft{}, err
	}
	files := []api.FileChange{
		{Path: workflowPath, Content: workflow},
		{Path: settings.Path, Content: settings.Render(set)},
		{Path: ownersPath, Content: ownersFile},
	}
	body := onboardingBody(set.DataMode)
	draft, err := s.proposals.Propose(ctx, changeFor(r, onboardingBranch, onboardingTitle, body, onboardingMessage, files), "")
	if err != nil {
		return proposal.Draft{}, fmt.Errorf("onboarding: propose %s: %w", onboardingBranch, err)
	}
	return draft, nil
}

func onboardingBody(mode settings.DataMode) string {
	next := "Review and merge this pull request, then run discovery: start the `archfit` workflow with `workflow_dispatch` and `mode` set to `discovery`. " +
		"Discovery opens a second pull request with a proposed `" + policy.PolicyPath + "`."
	if mode == settings.DataManaged {
		next = "Review the proposed `" + policy.PolicyPath + "` that managed discovery adds to this pull request, then merge it."
	}
	var b strings.Builder
	b.WriteString("This pull request sets up Archfit advisory feedback. Nothing is enforced: the gate stays `advisory` until an owner enables it by pull request.\n\n")
	b.WriteString("### Files\n\n")
	fmt.Fprintf(&b, "- `%s`: runs the Archfit Action, pinned to a commit, with the pinned engine image. It needs `contents: read` and `id-token: write`, no secrets, and runs no repository script. ", workflowPath)
	fmt.Fprintf(&b, "The Action reads `%s`, `%s` and `%s` per file: on a pull request from the pull request head when the pull request changes that file, otherwise from the tip of the base branch. "+
		"The App accepts a head copy only when an architecture owner approved that exact head commit. "+
		"The Action reports `baseline_digest` and `labels_digest` (SHA-256, empty when the file is absent) from exactly the files the engine read.\n",
		policy.PolicyPath, policy.BaselinePath, policy.LabelsPath)
	fmt.Fprintf(&b, "- `%s`: the Archfit App settings: the architecture owners, the gate mode and the approved workflow.\n", settings.Path)
	b.WriteString("- `CODEOWNERS`: makes the architecture owners the code owners of the protected paths. Existing lines are unchanged.\n\n")
	b.WriteString("### Protected paths\n\n")
	for _, p := range protectedPaths() {
		fmt.Fprintf(&b, "- `%s`\n", p)
	}
	b.WriteString("\nA change to any of them needs an architecture owner's approval on the exact head SHA.\n\n")
	b.WriteString("### Repository settings\n\n")
	b.WriteString("- Keep Archfit in its own ruleset with a documented bypass. A bypass is never reported as success.\n")
	b.WriteString("- Enable automatic deletion of head branches.\n\n")
	b.WriteString("### Next step\n\n")
	b.WriteString(next + "\n")
	return b.String()
}
