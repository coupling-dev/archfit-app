package ui

import (
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	maxOverviewRepos = 100
	parallelLookups  = 8
	maxSetupProbes   = 25
)

type overviewRow struct {
	FullName          string           `json:"full_name"`
	Waiting           bool             `json:"waiting,omitempty"` // no operator approved its installation: no state is read
	State             string           `json:"state"`             // retained stored evidence, not a readiness verdict
	SetupStage        string           `json:"setup_stage,omitempty"`
	SetupReason       string           `json:"setup_reason,omitempty"`
	Enforcement       string           `json:"enforcement"`
	EnforcementReason string           `json:"enforcement_reason,omitempty"`
	Attention         string           `json:"attention,omitempty"`
	NextStep          overviewNextStep `json:"next_step"`
}

type overviewNextStep struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

type overviewBody struct {
	Repos     []overviewRow `json:"repos"`
	Truncated bool          `json:"truncated"`
}

// apiRepos lists stored evidence separately from a bounded read of setup and required-check facts. Each row runs its
// own state read and, for the first maxSetupProbes admitted rows, its own Setup probe: no row waits for another.
func (s *server) apiRepos(r *http.Request, sc scope) (any, error) {
	repos, err := s.gh.users.UserRepos(r.Context(), sc.user.Token)
	if err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].FullName < repos[j].FullName })
	body := overviewBody{Truncated: len(repos) > maxOverviewRepos}
	if body.Truncated {
		repos = repos[:maxOverviewRepos]
	}
	body.Repos = make([]overviewRow, len(repos))
	admitted := s.admittedSet(r.Context(), repos)
	// The probed rows are chosen before any read starts, so each row's goroutine decides alone.
	probed := make([]bool, len(repos))
	probes := 0
	for i, repo := range repos {
		if admitted[repo.InstallationID] && probes < maxSetupProbes {
			probed[i] = true
			probes++
		}
	}
	failures := make([]error, len(repos))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, repo := range repos {
		if !admitted[repo.InstallationID] {
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, stateWaiting, "Installation approval is pending.")
			body.Repos[i].Waiting = true
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			state, err := s.repoStates.RepoState(r.Context(), repo)
			failures[i] = err
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, state, "Setup was not checked in this list; open Home to check.")
			// Stored evidence stays when the state read fails: the list then answers the error, as before.
			if err == nil && probed[i] {
				s.overviewProbe(r, repo, &body.Repos[i])
			}
		}()
	}
	wg.Wait()
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// overviewProbe reads one row's Setup through the existing probe. Its own Home snapshot reads the default branch's tip,
// policy and workflow once for this row, so the approval summary does not read them again.
func (s *server) overviewProbe(r *http.Request, repo api.Repo, row *overviewRow) {
	ctx, _ := gate.WithHomeSnapshot(r.Context(), repo)
	setup, err := s.setups.Setup(ctx, repo)
	if err != nil {
		row.SetupReason = "The App could not read setup; open Home to try again."
		row.EnforcementReason = "The App could not read the required-check settings."
		return // retain stored evidence; one GitHub failure must not break the list.
	}
	applyOverviewSetup(row, setup)
}

func uncheckedOverviewRow(name, state, reason string) overviewRow {
	return overviewRow{
		FullName: name, State: state, SetupReason: reason,
		Enforcement: "check", EnforcementReason: "Required-check settings were not read.",
		NextStep: overviewNextStep{Text: "Open Home", Href: "/r/" + name},
	}
}

func applyOverviewSetup(row *overviewRow, setup onboarding.Setup) {
	row.SetupStage, row.SetupReason = repoSetupStage(setup)
	row.Enforcement, row.EnforcementReason = repoEnforcement(setup.Rules)
	base := "/r/" + row.FullName
	switch row.SetupStage {
	case "workflow_review":
		row.Attention = fmt.Sprintf("PR #%d", setup.OpenPull)
		// Setup establishes an accepted workflow in a PR, not owner-action eligibility.
		row.NextStep = overviewNextStep{Text: "Review workflow PR", Href: fmt.Sprintf("%s/pull/%d", base, setup.OpenPull)}
	case "first_approval":
		if setup.FirstPull > 0 {
			row.Attention = fmt.Sprintf("PR #%d", setup.FirstPull)
			row.NextStep = overviewNextStep{Text: "Review first policy", Href: fmt.Sprintf("%s/pull/%d", base, setup.FirstPull)}
		} else if setup.Policy && setup.Workflow {
			row.Attention = "First approval"
			row.NextStep = overviewNextStep{Text: "Review first policy", Href: base}
		} else {
			row.NextStep = overviewNextStep{Text: "Continue setup", Href: base}
		}
	case "not_started":
		row.NextStep = overviewNextStep{Text: "Continue setup", Href: base}
	}
}

func repoSetupStage(setup onboarding.Setup) (stage, reason string) {
	switch {
	case setup.State == "outside_flow":
		return "", "The policy changed outside the approval flow."
	case setup.Policy && !setup.Workflow && setup.OpenPull > 0:
		return "workflow_review", ""
	case setup.FirstPull > 0 || setup.State == "first_approval":
		return "first_approval", ""
	case setup.Policy && setup.Workflow && setup.State == "approved":
		return "policy_approved", ""
	case setup.State != "" && setup.State != "not_admitted":
		return "", "The setup facts do not establish a current policy approval."
	case !setup.Policy:
		return "not_started", ""
	default:
		return "not_started", "Continue setup to add the workflow and approve the policy."
	}
}

func repoEnforcement(report onboarding.RulesetReport) (state, reason string) {
	var gateState, pullState string
	for _, finding := range report.Findings {
		switch finding.Code {
		case "gate_check":
			gateState = finding.State
		case "pull_requests":
			pullState = finding.State
		}
	}
	switch {
	case gateState == "ok":
		return "required", ""
	case gateState == "warning" && pullState == "ok":
		return "not_required", ""
	default:
		return "check", "The rules do not establish whether archfit/gate is required; check with an admin."
	}
}
