package ui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/coupling-dev/archfit-app/internal/control/onboarding"
	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// An installation no operator has admitted: the list reads nothing from it. Operators admit; owners approve.
const stateNotAdmitted = "not admitted"

// A repository whose pull requests get advisory checks before the first approval (see advisoryBeforeApproval).
const repoStateAdvisory = "advisory"

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

// probeQuery asks the list for the Setup probe of its rows. Without it the list answers from stored state alone, so the
// page can show its rows at once and ask for the probe beside it.
const probeQuery = "probe"

// apiRepos lists stored evidence, and with ?probe=1 also a bounded read of setup and required-check facts. Each row runs
// its own state read and, for the first maxSetupProbes admitted rows of a probed list, its own Setup probe: no row
// waits for another. An unprobed row says its required-check settings were not read.
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
	probe := r.URL.Query().Get(probeQuery) == "1"
	probed := make([]bool, len(repos))
	probes := 0
	for i, repo := range repos {
		if probe && admitted[repo.InstallationID] && probes < maxSetupProbes {
			probed[i] = true
			probes++
		}
	}
	failures := make([]error, len(repos))
	start := time.Now()
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
			slotAt := time.Since(start)
			state, err := s.repoStates.RepoState(ctx, repo)
			stateAt := time.Since(start)
			failures[i] = err
			body.Repos[i] = uncheckedOverviewRow(repo.FullName, state)
			// Stored evidence stays when the state read fails: the list then answers the error, as before.
			if err == nil && probed[i] {
				probeAt := time.Since(start)
				s.overviewProbe(onboarding.WithRowTrace(ctx, i, start), repo, &body.Repos[i])
				// Offsets from the request start show whether a row waited on another row, not just how long it ran.
				slog.Info("ui probe row", "row", i, "slot_ms", durMS(slotAt), "state_ms", durMS(stateAt), "probe_ms", durMS(probeAt), "done_ms", durMS(time.Since(start)))
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
	// One stage per probed row: the log shows each row's own wall time, which the summed rules stage cannot.
	done := api.StartStage(ctx, "probe_row")
	ctx, _ = gate.WithHomeSnapshot(ctx, repo)
	setup, err := s.setups.Setup(ctx, repo)
	done(1, 0)
	if err != nil {
		row.EnforcementReason = "The App could not read the required-check settings."
		return // retain stored evidence; one GitHub failure must not break the list.
	}
	applyOverviewSetup(row, setup)
}

func durMS(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func uncheckedOverviewRow(name, state string) overviewRow {
	return overviewRow{
		FullName: name, State: state,
		Enforcement: "check", EnforcementReason: "Required-check settings were not read.",
	}
}

func applyOverviewSetup(row *overviewRow, setup onboarding.Setup) {
	// The stored state cannot tell advisory checks from none before the first approval; the probe can, as Home does.
	if row.State == gate.RepoNotSetUp && advisoryBeforeApproval(setup.State, setup.Mode, setup.Admitted) {
		row.State = repoStateAdvisory
	}
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
