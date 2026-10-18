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
	MainState  string // archfit/state on the default-branch head, a word
	MainReason string // its title
	Pulls      []pullRow
	PullsMore  int  // open pull requests past maxStatusPulls
	PullsError bool // the list could not be read; the rest of the page stands
	Steps      []step
	Next       string // the next setup step, "" when there is none
	GraphHref  string

	Stage       string
	Diagnostic  string
	DraftURL    string
	DraftState  string
	Banner      string
	Admin       bool
	CanStart    bool
	CanDiscover bool
	PullsURL    string
	ChecksURL   string
}

func (s *server) status(w http.ResponseWriter, r *http.Request, sc scope) {
	progress, err := s.onboarding.Progress(r.Context(), sc.repo)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	_, adminErr := s.sessions.Authorize(r.Context(), sc.user, sc.repo.FullName, permAdmin)
	body := statusBody{
		Stage:       stageWords[progress.Stage],
		Diagnostic:  progress.Diagnostic,
		Admin:       adminErr == nil,
		CanStart:    progress.Stage == onboarding.StageInstalled && progress.OpenDraft == nil,
		CanDiscover: reached(progress.Stage, onboarding.StageWorkflowMerged),
		PullsURL:    "https://github.com/" + sc.repo.FullName + "/pulls?q=" + url.QueryEscape("is:pr head:archfit/"),
		ChecksURL:   "https://github.com/" + sc.repo.FullName + "/commits/" + sc.repo.DefaultBranch,
	}
	if d := progress.OpenDraft; d != nil {
		body.DraftURL, body.DraftState = d.URL, draftWords[d.State]
	}
	body.Steps, body.Next = setupSteps(progress.Stage)
	body.GraphHref = repoPath(sc, "/graph")
	if hasReport(progress.Stage) {
		body.MainState, body.MainReason = s.defaultBranchState(r.Context(), sc.repo)
		body.Pulls, body.PullsMore, body.PullsError = s.openPulls(r.Context(), sc)
	}
	st, err := s.account.Status(r.Context(), sc.repo.InstallationID, s.now())
	if err != nil {
		slog.Warn("ui entitlement status unavailable", "installation_id", sc.repo.InstallationID)
	} else {
		body.Banner = banner(st)
	}
	s.render(w, r, http.StatusOK, "status", s.newPage(r, sc, sc.repo.FullName, body))
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

func (s *server) startOnboarding(w http.ResponseWriter, r *http.Request, sc scope) {
	owners, err := parseOwners(r.PostForm.Get("owners"))
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	switch r.PostForm.Get("mode") {
	case modeCustomer:
		_, err = s.onboarding.StartCustomer(r.Context(), sc.repo, owners)
	case modeManaged:
		if !s.consents.Has(r.Context(), sc.repo.InstallationID, account.ConsentSourceProcessing) {
			logOf(r).outcome = "consent_needed"
			seeOther(w, r, repoPath(sc, "/consent"), nil)
			return
		}
		_, err = s.onboarding.StartManaged(r.Context(), sc.repo, owners)
	default:
		err = invalid("Choose customer or managed onboarding.")
	}
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	seeOther(w, r, repoPath(sc, ""), url.Values{"notice": {"onboarding"}})
}

func (s *server) discover(w http.ResponseWriter, r *http.Request, sc scope) {
	progress, err := s.onboarding.Progress(r.Context(), sc.repo)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	if !reached(progress.Stage, onboarding.StageWorkflowMerged) {
		s.fail(w, r, sc, conflict("Discovery needs the Archfit workflow on the default branch. Merge the onboarding pull request first."))
		return
	}
	set, err := s.settings(r.Context(), sc.repo)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	err = s.gh.actions.Dispatch(r.Context(), sc.repo.RepoRef, set.Workflow, sc.repo.DefaultBranch, map[string]string{"mode": "discovery"})
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	seeOther(w, r, repoPath(sc, ""), url.Values{"notice": {"discover"}})
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

type consentRow struct {
	Kind       account.ConsentKind
	Title      string
	Disclosure string
	Recorded   bool
}

var consentKinds = []consentRow{
	{
		Kind:  account.ConsentSourceProcessing,
		Title: "Managed discovery",
		Disclosure: "Archfit clones the repository into a short-lived sandbox to propose a first policy. " +
			"Source code leaves your environment for the duration of that job and is deleted with the sandbox.",
	},
	{
		Kind:  account.ConsentManagedLLM,
		Title: "Managed AI",
		Disclosure: "When a user asks for a proposal in managed mode, the draft policy text and the instruction are sent " +
			"to the AI provider. No source code and no report is sent.",
	},
}

func (s *server) consentPage(w http.ResponseWriter, r *http.Request, sc scope) {
	rows := slices.Clone(consentKinds)
	for i := range rows {
		rows[i].Recorded = s.consents.Has(r.Context(), sc.repo.InstallationID, rows[i].Kind)
	}
	s.render(w, r, http.StatusOK, "consent", s.newPage(r, sc, "Consent", rows))
}

func (s *server) consent(w http.ResponseWriter, r *http.Request, sc scope) {
	kind := account.ConsentKind(r.PostForm.Get("kind"))
	if kind != account.ConsentSourceProcessing && kind != account.ConsentManagedLLM {
		s.fail(w, r, sc, invalid("Unknown consent."))
		return
	}
	var err error
	switch r.PostForm.Get("action") {
	case "record":
		err = s.consents.Record(r.Context(), sc.repo.InstallationID, account.Consent{Kind: kind, By: sc.user.Login, At: s.now()})
	case "revoke":
		err = s.consents.Revoke(r.Context(), sc.repo.InstallationID, kind)
	default:
		err = invalid("Unknown action.")
	}
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	seeOther(w, r, repoPath(sc, "/consent"), url.Values{"notice": {"consent"}})
}

// maxStatusPulls bounds the open pull requests the repository page looks up, one check-runs read each.
const maxStatusPulls = 20

type pullRow struct {
	Number int
	Title  string
	State  string // archfit/state on the head, a word
	Reason string // its title: the headline feedback wrote
	Href   string
}

type step struct {
	Label         string
	Done, Current bool
}

var stepLabels = map[onboarding.Stage]string{
	onboarding.StageInstalled: "Installed", onboarding.StageWorkflowMerged: "Workflow merged",
	onboarding.StageReportReceived: "First report", onboarding.StagePolicyMerged: "Policy merged",
	onboarding.StageAdvisory: "Advisory", onboarding.StageRequired: "Required",
}

var nextSteps = map[onboarding.Stage]string{
	onboarding.StageAdvisory: "Rehearse the gate: one forbidden change must show a rule violation on archfit/state and one " +
		"legitimate change must not. Then merge an owner-approved settings pull request that sets gate: required, and add " +
		"archfit/gate as a required check in a GitHub ruleset: the settings alone never block a merge.",
	onboarding.StageRequired: "Make sure archfit/gate is a required check in a GitHub ruleset. The settings alone never block a merge.",
}

// hasReport reports whether onboarding has seen a report. policy_merged sorts after report_received on the path but
// means a merged policy with no report yet.
func hasReport(st onboarding.Stage) bool {
	return st == onboarding.StageReportReceived || st == onboarding.StageAdvisory || st == onboarding.StageRequired
}

// setupSteps is the onboarding path with the current stage marked, and the advice for the step after it.
func setupSteps(current onboarding.Stage) ([]step, string) {
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
			rows[i] = pullRow{Number: p.Number, Title: p.Title, State: state, Reason: reason, Href: repoPath(sc, "/pull/"+strconv.Itoa(p.Number))}
		}()
	}
	wg.Wait()
	return rows, more, false
}
