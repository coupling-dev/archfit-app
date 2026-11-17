package ui

import (
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
)

// An installation no operator has admitted: the list reads nothing from it. Operators admit; owners approve.
const stateNotAdmitted = "not admitted"

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
	Enforcement       string           `json:"enforcement"`
	EnforcementReason string           `json:"enforcement_reason,omitempty"`
	NextStep          overviewNextStep `json:"next_step"` // empty when the list names no step
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
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, stateNotAdmitted)
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
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, state)
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
				body.Repos[i].EnforcementReason = "The App could not read the required-check settings."
				return // retain stored evidence; one GitHub failure must not break the list.
			}
			applyOverviewSetup(&body.Repos[i], setup)
		}()
	}
	wg.Wait()
	return body, nil
}

func uncheckedOverviewRow(name, state string) overviewRow {
	return overviewRow{
		FullName: name, State: state,
		Enforcement: "check", EnforcementReason: "Required-check settings were not read.",
	}
}

func applyOverviewSetup(row *overviewRow, setup onboarding.Setup) {
	row.SetupStage = repoSetupStage(setup)
	row.Enforcement, row.EnforcementReason = repoEnforcement(setup.Rules)
	base := "/r/" + row.FullName
	switch row.SetupStage {
	case "workflow_review":
		// Setup establishes an accepted workflow in a PR, not owner-action eligibility.
		row.NextStep = overviewNextStep{Text: "Review workflow PR", Href: fmt.Sprintf("%s/pull/%d", base, setup.OpenPull)}
	case "first_approval":
		if setup.FirstPull > 0 {
			row.NextStep = overviewNextStep{Text: "Review first policy", Href: fmt.Sprintf("%s/pull/%d", base, setup.FirstPull)}
		} else if setup.Policy && setup.Workflow {
			row.NextStep = overviewNextStep{Text: "Review first policy", Href: base}
		} else {
			row.NextStep = overviewNextStep{Text: "Continue setup", Href: base}
		}
	case "not_started":
		row.NextStep = overviewNextStep{Text: "Continue setup", Href: base}
	}
}

func repoSetupStage(setup onboarding.Setup) string {
	switch {
	case setup.State == "outside_flow":
		return ""
	case setup.Policy && !setup.Workflow && setup.OpenPull > 0:
		return "workflow_review"
	case setup.FirstPull > 0 || setup.State == "first_approval":
		return "first_approval"
	case setup.Policy && setup.Workflow && setup.State == "approved":
		return "policy_approved"
	case setup.State != "" && setup.State != "not_admitted":
		return ""
	default:
		return "not_started"
	}
}

// repoEnforcement answers the same question as Home's step 4: required only when the gate and pull request rules
// are both confirmed on and no rules finding says the rulesets are unreadable. A confirmed-off rule is not
// required; an unknown or missing gate or pull request finding is not confirmed.
func repoEnforcement(report onboarding.RulesetReport) (state, reason string) {
	findings := map[string]string{}
	for _, finding := range report.Findings {
		findings[finding.Code] = finding.State
	}
	gate, pulls := findings["gate_check"], findings["pull_requests"]
	// Home reads a missing rules finding as readable; only a finding the App could not read blocks the check.
	readable := true
	for _, code := range []string{"rulesets", "branch_rules"} {
		if state, ok := findings[code]; ok && state != "ok" {
			readable = false
		}
	}
	switch {
	case gate == "ok" && pulls == "ok" && readable:
		return "required", ""
	case gate == "warning" || pulls == "warning":
		return "not_required", ""
	default:
		return "check", "The App could not confirm that archfit/gate is required; ask a repository administrator."
	}
}
