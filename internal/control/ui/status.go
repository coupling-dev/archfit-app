package ui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	modeCustomer = "customer"
	modeManaged  = "managed"

	// probeWorkflow only completes the settings document that checks the owners; it is never written.
	probeWorkflow = ".github/workflows/probe.yaml"
)

// stageOrder is the onboarding path; discovery needs the workflow on the default branch.
var stageOrder = []onboarding.Stage{
	onboarding.StageInstalled, onboarding.StageWorkflowMerged, onboarding.StageReportReceived,
	onboarding.StagePolicyMerged, onboarding.StageAdvisory, onboarding.StageRequired,
}

func reached(have, want onboarding.Stage) bool {
	return slices.Index(stageOrder, have) >= slices.Index(stageOrder, want)
}

type statusBody struct {
	DefaultBranch string    `json:"default_branch"`
	MainState     string    `json:"main_state"`  // archfit/state on the default-branch head, a word; "" before a report
	MainReason    string    `json:"main_reason"` // its title
	MainTone      string    `json:"main_tone"`
	Pulls         []pullRow `json:"pulls"`
	PullsMore     int       `json:"pulls_more"`  // open pull requests past maxStatusPulls
	PullsError    bool      `json:"pulls_error"` // the list could not be read; the rest of the page stands
	Steps         []step    `json:"steps"`
	Next          string    `json:"next"`     // the next setup step in one sentence, "" when there is none
	NextHow       string    `json:"next_how"` // its details, shown behind "How"

	Stage       string `json:"stage"`
	Diagnostic  string `json:"diagnostic"`
	DraftURL    string `json:"draft_url"`
	DraftState  string `json:"draft_state"`
	Banner      string `json:"banner"`
	Admin       bool   `json:"admin"`
	ManagedAI   string `json:"managed_ai,omitempty"` // for an administrator: allowed, not_allowed or unknown
	CanStart    bool   `json:"can_start"`
	CanDiscover bool   `json:"can_discover"`
	PullsURL    string `json:"pulls_url"`
	ChecksURL   string `json:"checks_url"`
}

// apiStatus is the repository overview: the default branch, open pull requests and the onboarding path.
func (s *server) apiStatus(r *http.Request, sc scope) (any, error) {
	progress, err := s.onboarding.Progress(r.Context(), sc.repo)
	if err != nil {
		return nil, err
	}
	body := statusBody{
		DefaultBranch: sc.repo.DefaultBranch,
		Stage:         stageWords[progress.Stage],
		Diagnostic:    progress.Diagnostic,
		CanStart:      progress.Stage == onboarding.StageInstalled && progress.OpenDraft == nil,
		CanDiscover:   reached(progress.Stage, onboarding.StageWorkflowMerged),
		PullsURL:      "https://github.com/" + sc.repo.FullName + "/pulls?q=" + url.QueryEscape("is:pr head:archfit/"),
		ChecksURL:     "https://github.com/" + sc.repo.FullName + "/commits/" + sc.repo.DefaultBranch,
	}
	if d := progress.OpenDraft; d != nil {
		body.DraftURL, body.DraftState = d.URL, draftWords[d.State]
	}
	var next nextStep
	body.Steps, next = setupSteps(progress.Stage)
	body.Next, body.NextHow = next.next, next.how

	// The parts below never fail the page, each says so in its own field, and each writes only its own fields: they
	// run side by side.
	ctx := r.Context()
	parts := []func(){
		func() {
			_, adminErr := s.sessions.Authorize(ctx, sc.user, sc.repo.FullName, permAdmin)
			if body.Admin = adminErr == nil; body.Admin {
				body.ManagedAI = s.managedAIWord(ctx, sc)
			}
		},
		func() {
			st, err := s.account.Status(ctx, sc.repo.InstallationID, s.now())
			if err != nil {
				slog.Warn("ui entitlement status unavailable", "installation_id", sc.repo.InstallationID)
				return
			}
			body.Banner = banner(st)
		},
	}
	if hasReport(progress.Stage) {
		parts = append(parts,
			func() {
				body.MainState, body.MainReason = s.defaultBranchState(ctx, sc.repo)
				body.MainTone = toneOf(body.MainState)
			},
			func() { body.Pulls, body.PullsMore, body.PullsError = s.openPulls(ctx, sc) },
		)
	}
	together(parts...)
	return body, nil
}

// parseOwners reads one owner per line; blank lines are skipped. wire/settings owns the owner rule, so the
// list is checked the way onboarding checks it. The wording is fixed: Parse's error text is never shown.
func parseOwners(text string) ([]string, error) {
	var owners []string
	for line := range strings.Lines(text) {
		if owner := strings.TrimSpace(line); owner != "" {
			owners = append(owners, owner)
		}
	}
	probe := settings.Settings{Version: 1, Owners: owners, Gate: settings.GateAdvisory, Workflow: probeWorkflow, DataMode: settings.DataCustomer}
	if _, err := settings.Parse(settings.Render(probe)); err != nil {
		return nil, invalid("Name at least one owner. Owners are GitHub logins or org/team-slug, one per line, each named once.")
	}
	return owners, nil
}

type onboardingJSON struct {
	Mode   string `json:"mode"`
	Owners string `json:"owners"` // one owner per line, as typed
}

func (s *server) apiStartOnboarding(r *http.Request, sc scope) (any, error) {
	var in onboardingJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	owners, err := parseOwners(in.Owners)
	if err != nil {
		return nil, err
	}
	switch in.Mode {
	case modeCustomer:
		_, err = s.onboarding.StartCustomer(r.Context(), sc.repo, owners)
	case modeManaged:
		if !s.consents.Has(r.Context(), sc.repo.InstallationID, account.ConsentSourceProcessing) {
			logOf(r).outcome = "consent_needed"
			return noticeJSON{Location: repoPath(sc, "/data-sharing")}, nil
		}
		_, err = s.onboarding.StartManaged(r.Context(), sc.repo, owners)
	default:
		err = invalid("Choose customer or managed onboarding.")
	}
	if err != nil {
		return nil, err
	}
	return noticeJSON{Notice: notices["onboarding"]}, nil
}

func (s *server) apiDiscover(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil { // the body is {}: the repository comes from the path
		return nil, err
	}
	progress, err := s.onboarding.Progress(r.Context(), sc.repo)
	if err != nil {
		return nil, err
	}
	if !reached(progress.Stage, onboarding.StageWorkflowMerged) {
		return nil, conflict("Discovery needs the Archfit workflow on the default branch. Merge the onboarding pull request first.")
	}
	set, err := s.settings(r.Context(), sc.repo)
	if err != nil {
		return nil, err
	}
	err = s.gh.actions.Dispatch(r.Context(), sc.repo.RepoRef, set.Workflow, sc.repo.DefaultBranch, map[string]string{"mode": "discovery"})
	if err != nil {
		return nil, err
	}
	return noticeJSON{Notice: notices["discover"]}, nil
}

var errNoSettings = conflict("The default branch has no valid " + settings.Path + " yet. Finish onboarding first.")

// settings reads the owner-approved settings file on the default branch.
func (s *server) settings(ctx context.Context, repo api.Repo) (settings.Settings, error) {
	raw, _, err := s.gh.files.File(ctx, repo.RepoRef, repo.DefaultBranch, settings.Path)
	if errors.Is(err, api.ErrNotFound) {
		return settings.Settings{}, errNoSettings
	}
	if err != nil {
		return settings.Settings{}, err
	}
	set, err := settings.Parse(raw)
	if err != nil {
		return settings.Settings{}, errNoSettings
	}
	return set, nil
}

// maxStatusPulls bounds the open pull requests the repository page looks up, one check-runs read each.
const maxStatusPulls = 20

type pullRow struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // archfit/state on the head, a word
	Tone   string `json:"tone"`
	Reason string `json:"reason"` // its title: the headline feedback wrote
	Href   string `json:"href"`
}

type step struct {
	Label   string `json:"label"`
	Done    bool   `json:"done"`
	Current bool   `json:"current"`
}

var stepLabels = map[onboarding.Stage]string{
	onboarding.StageInstalled: "Installed", onboarding.StageWorkflowMerged: "Workflow merged",
	onboarding.StageReportReceived: "First report", onboarding.StagePolicyMerged: "Policy merged",
	onboarding.StageAdvisory: "Advisory", onboarding.StageRequired: "Required",
}

// nextStep is the advice for the step after the current one: one sentence, and the details behind "How".
type nextStep struct{ next, how string }

var nextSteps = map[onboarding.Stage]nextStep{
	onboarding.StageAdvisory: {
		next: "Make the gate required. Rehearse it first on one forbidden and one legitimate change.",
		how: "A forbidden change must show a rule violation on archfit/state and a legitimate change must not. Then merge an " +
			"owner-approved settings pull request that sets gate: required, and add archfit/gate as a required check in a " +
			"GitHub ruleset: the settings alone never block a merge.",
	},
	onboarding.StageRequired: {
		next: "Make archfit/gate a required check in a GitHub ruleset.",
		how:  "The settings alone never block a merge.",
	},
}

// hasReport reports whether onboarding has seen a report. policy_merged sorts after report_received on the path but
// means a merged policy with no report yet.
func hasReport(st onboarding.Stage) bool {
	return st == onboarding.StageReportReceived || st == onboarding.StageAdvisory || st == onboarding.StageRequired
}

// setupSteps is the onboarding path with the current stage marked, and the advice for the step after it.
func setupSteps(current onboarding.Stage) ([]step, nextStep) {
	steps := make([]step, len(stageOrder))
	for i, st := range stageOrder {
		done := reached(current, st) && st != current
		if st == onboarding.StageReportReceived {
			done = hasReport(current) && st != current
		}
		steps[i] = step{Label: stepLabels[st], Done: done, Current: st == current}
	}
	return steps, nextSteps[current]
}

// openPulls reads each open pull request's archfit/state, newest first. A failed list leaves the rest of the page
// standing; a failed lookup is "unknown" for that row only.
func (s *server) openPulls(ctx context.Context, sc scope) (rows []pullRow, more int, failed bool) {
	pulls, err := s.gh.pulls.OpenPulls(ctx, sc.repo.RepoRef)
	if err != nil {
		slog.Warn("ui open pull requests unavailable", "repository_id", sc.repo.ID, "installation_id", sc.repo.InstallationID)
		return nil, 0, true
	}
	if len(pulls) > maxStatusPulls {
		more, pulls = len(pulls)-maxStatusPulls, pulls[:maxStatusPulls]
	}
	rows = make([]pullRow, len(pulls))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, p := range pulls {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			state, reason := s.checkWord(ctx, sc.repo.RepoRef, p.HeadSHA, checkState)
			rows[i] = pullRow{Number: p.Number, Title: p.Title, State: state, Tone: toneOf(state), Reason: reason, Href: repoPath(sc, "/pull/"+strconv.Itoa(p.Number))}
		}()
	}
	wg.Wait()
	return rows, more, false
}
