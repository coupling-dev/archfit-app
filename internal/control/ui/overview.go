package ui

import (
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
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

// apiRepos lists stored evidence separately from a bounded read of setup and required-check facts.
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
		}()
	}
	wg.Wait()
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}

	// Stored drift state says nothing about setup or required checks. Reuse the existing bounded Setup probe
	// for the first 25 admitted rows, including approved records; never add a second GitHub read path.
	probes := 0
	for i, repo := range repos {
		if !admitted[repo.InstallationID] || probes >= maxSetupProbes {
			continue
		}
		probes++
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			setup, err := s.setups.Setup(r.Context(), repo)
			if err != nil {
				body.Repos[i].SetupReason = "The App could not read setup; open Home to try again."
				body.Repos[i].EnforcementReason = "The App could not read the required-check settings."
				return // retain stored evidence; one GitHub failure must not break the list.
			}
			applyOverviewSetup(&body.Repos[i], setup)
		}()
	}
	wg.Wait()
	return body, nil
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
