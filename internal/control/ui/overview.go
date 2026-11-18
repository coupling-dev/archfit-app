package ui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
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
	// The list writes nothing, so each installation's admission and each repository's record is read once per request,
	// however many rows and steps ask for it. The memo dies with the request.
	ctx := gate.WithRequestMemo(r.Context())
	repos, err := s.gh.users.UserRepos(ctx, sc.user.Token)
	if err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].FullName < repos[j].FullName })
	body := overviewBody{Truncated: len(repos) > maxOverviewRepos}
	if body.Truncated {
		repos = repos[:maxOverviewRepos]
	}
	body.Repos = make([]overviewRow, len(repos))
	admitted := s.admittedSet(ctx, repos)
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
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, stateNotAdmitted)
			body.Repos[i].Waiting = true
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			state, err := s.repoStates.RepoState(ctx, repo)
			failures[i] = err
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, state)
			// Stored evidence stays when the state read fails: the list then answers the error, as before.
			if err == nil && probed[i] {
				s.overviewProbe(ctx, repo, &body.Repos[i])
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
func (s *server) overviewProbe(ctx context.Context, repo api.Repo, row *overviewRow) {
	ctx, _ = gate.WithHomeSnapshot(ctx, repo)
	setup, err := s.setups.Setup(ctx, repo)
	if err != nil {
		row.EnforcementReason = "The App could not read the required-check settings."
		return // retain stored evidence; one GitHub failure must not break the list.
	}
	applyOverviewSetup(row, setup)
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
		row.NextStep = overviewNextStep{Text: "Review workflow change", Href: fmt.Sprintf("%s/pull/%d", base, setup.OpenPull)}
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
		if found, ok := findings[code]; ok && found != "ok" {
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
