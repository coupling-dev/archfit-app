package ui

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
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
	MainSHA       string    `json:"main_sha"` // the default-branch commit that check is on, short
	MainAt        string    `json:"main_at"`  // when it started, RFC 3339 UTC; "" without a check
	Pulls         []pullRow `json:"pulls"`
	PullsMore     int       `json:"pulls_more"`      // open pull requests past maxStatusPulls
	PullsError    bool      `json:"pulls_error"`     // the list could not be read; the rest of the page stands
	PullsToUpdate int       `json:"pulls_to_update"` // listed pull requests that wait for an update after the target moved

	// DecisionsPending counts the decision requests that wait for an owner in all pull requests; Decisions lists the
	// first few. Neither is read for a repository without an open pull request.
	DecisionsPending int           `json:"decisions_pending"`
	Decisions        []decisionRow `json:"decisions"`
	DecisionsError   bool          `json:"decisions_error"` // the requests could not be read; the rest of the page stands
	Steps            []step        `json:"steps"`
	Next             string        `json:"next"`     // the next setup step in one sentence, "" when there is none
	NextHow          string        `json:"next_how"` // its details, shown behind "How"

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

	Workflow *workflowJSON `json:"workflow,omitempty"` // the file the owner adds by hand; absent when none is due
}

// workflowJSON is the Archfit workflow the owner adds: the App has no permission to write .github/workflows.
type workflowJSON struct {
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Content string `json:"content"`
	AddURL  string `json:"add_url"` // GitHub's new-file page on Branch, with the path and the content filled in
}

// newWorkflowJSON fills GitHub's new-file page through its filename and value query parameters. GitHub does not
// document them; the page shows the content to copy as well, so a change on GitHub's side costs one paste.
func newWorkflowJSON(repo string, wf *onboarding.WorkflowFile) *workflowJSON {
	segments := strings.Split(wf.Branch, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	q := url.Values{"filename": {wf.Path}, "value": {string(wf.Content)}}
	return &workflowJSON{
		Path:    wf.Path,
		Branch:  wf.Branch,
		Content: string(wf.Content),
		AddURL:  "https://github.com/" + repo + "/new/" + strings.Join(segments, "/") + "?" + q.Encode(),
	}
}

// apiStatus is the repository overview: the default branch, open pull requests and the onboarding path.
func (s *server) apiStatus(r *http.Request, sc scope) (any, error) {
	// Every read starts at once, side by side. The default branch and the open pull requests count only once a report
	// came in, which the onboarding progress tells last: they are read on the chance and dropped without one, so the
	// page waits for the slowest read, not for their sum. Each part writes only its own variables.
	ctx := r.Context()
	var (
		progress    onboarding.Progress
		progressErr error
		admin       bool
		managedAI   string
		bannerText  string
		main        branchCheck
		pulls       struct {
			rows   []pullRow
			more   int
			failed bool
		}
	)
	together(
		func() { progress, progressErr = s.onboarding.Progress(ctx, sc.repo) },
		func() {
			_, adminErr := s.sessions.Authorize(ctx, sc.user, sc.repo.FullName, permAdmin)
			if admin = adminErr == nil; admin {
				managedAI = s.managedAIWord(ctx, sc)
			}
		},
		func() {
			st, err := s.account.Status(ctx, sc.repo.InstallationID, s.now())
			if err != nil {
				slog.Warn("ui entitlement status unavailable", "installation_id", sc.repo.InstallationID)
				return
			}
			bannerText = banner(st)
		},
		func() { main = s.defaultBranchState(ctx, sc.repo) },
		func() { pulls.rows, pulls.more, pulls.failed = s.openPulls(ctx, sc) },
	)
	if progressErr != nil {
		return nil, progressErr
	}
	body := statusBody{
		DefaultBranch: sc.repo.DefaultBranch,
		Stage:         stageWords[progress.Stage],
		Diagnostic:    progress.Diagnostic,
		CanStart:      progress.Stage == onboarding.StageInstalled && progress.OpenDraft == nil && progress.Workflow == nil,
		CanDiscover:   discoveryStep(progress),
		PullsURL:      "https://github.com/" + sc.repo.FullName + "/pulls?q=" + url.QueryEscape("is:pr head:archfit/"),
		ChecksURL:     "https://github.com/" + sc.repo.FullName + "/commits/" + sc.repo.DefaultBranch,
		Admin:         admin,
		ManagedAI:     managedAI,
		Banner:        bannerText,
		Decisions:     []decisionRow{},
	}
	if d := progress.OpenDraft; d != nil {
		body.DraftURL, body.DraftState = d.URL, draftWords[d.State]
	}
	if progress.Workflow != nil {
		body.Workflow = newWorkflowJSON(sc.repo.FullName, progress.Workflow)
	}
	var next nextStep
	body.Steps, next = setupSteps(progress.Stage)
	if stage, advice, ok := unenforcedGate(progress.Enforcement, sc.repo.DefaultBranch); ok {
		body.Stage, next = stage, advice
	}
	if body.CanDiscover {
		next = discoverNext
	}
	body.Next, body.NextHow = next.next, next.how
	if hasReport(progress.Stage) {
		body.MainState, body.MainReason, body.MainTone = main.state, main.reason, toneOf(main.state)
		if !main.at.IsZero() {
			body.MainSHA, body.MainAt = main.sha[:min(len(main.sha), shortSHA)], main.at.UTC().Format(time.RFC3339)
		}
		body.Pulls, body.PullsMore, body.PullsError = pulls.rows, pulls.more, pulls.failed
		for _, p := range pulls.rows {
			if p.Rerun != "" {
				body.PullsToUpdate++
			}
		}
		// A decision request belongs to an open pull request: without one there is nothing to read.
		if len(pulls.rows) > 0 || pulls.failed {
			titles := map[int]string{}
			for _, p := range pulls.rows {
				titles[p.Number] = p.Title
			}
			body.DecisionsPending, body.Decisions, body.DecisionsError = s.pendingDecisions(ctx, sc, maxNeedsYouDecisions, titles)
		}
	}
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
	// Rerun is set while the pull request waits for an update after the target branch moved (target_moved): the next
	// step as the comment words it, with the modules the pull request and the target close a cycle through.
	Rerun string `json:"rerun,omitempty"`
}

// pullCheck is an open pull request with the archfit/state check on its head.
type pullCheck struct {
	pull          api.Pull
	state, reason string
	run           *api.CheckRun // nil without a check or when it could not be read
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
}

// unenforcedGate is what a repository shows whose settings say gate: required while GitHub is not known to require
// archfit/gate: the settings alone never block a merge. Its stage stays Advisory until a ruleset requires the check.
func unenforcedGate(e onboarding.Enforcement, branch string) (stage string, advice nextStep, ok bool) {
	switch e {
	case onboarding.EnforcementNotEnforced:
		return "Gate is required in settings, not enforced by GitHub", nextStep{
			next: "Make archfit/gate a required status check in a GitHub ruleset on " + branch + ".",
			how: "The settings say gate: required, but GitHub does not require the check on " + branch + ", so a failing " +
				"pull request can still be merged. In the repository's Settings, Rules, add a branch ruleset for " + branch +
				" with the required status check archfit/gate.",
		}, true
	case onboarding.EnforcementUnknown:
		return "Gate is required in settings, enforcement not confirmed", nextStep{
			next: "Confirm archfit/gate is a required status check on " + branch + ".",
			how: "The settings say gate: required. The App cannot read whether GitHub requires the check on " + branch +
				": GitHub did not answer, or the branch uses classic branch protection, which the App cannot read. In the " +
				"repository's Settings, check that archfit/gate is a required status check, preferably in a ruleset.",
		}, true
	}
	return "", nextStep{}, false
}

// discoverNext is the step of a repository with the workflow and no policy: its button is "Run discovery".
var discoverNext = nextStep{
	next: "Run discovery: it proposes a first policy as a pull request.",
	how: "Discovery runs in your own workflow (workflow_dispatch with mode: discovery); source code never leaves your " +
		"CI. Its proposal arrives as a pull request that the policy owners review and merge.",
}

// discoveryStep reports whether running discovery is this step's action: the workflow is merged, no policy yet, and no
// policy pull request open (then the step is to review it). From the merged policy on, discovery is no step of the way.
func discoveryStep(p onboarding.Progress) bool {
	return (p.Stage == onboarding.StageWorkflowMerged || p.Stage == onboarding.StageReportReceived) && p.OpenDraft == nil
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

// openPullChecks reads each open pull request's archfit/state, newest first. A failed list leaves the rest of the page
// standing; a failed lookup is "unknown" for that pull request only.
func (s *server) openPullChecks(ctx context.Context, sc scope) (checks []pullCheck, more int, failed bool) {
	pulls, err := s.gh.pulls.OpenPulls(ctx, sc.repo.RepoRef)
	if err != nil {
		slog.Warn("ui open pull requests unavailable", "repository_id", sc.repo.ID, "installation_id", sc.repo.InstallationID)
		return nil, 0, true
	}
	if len(pulls) > maxStatusPulls {
		more, pulls = len(pulls)-maxStatusPulls, pulls[:maxStatusPulls]
	}
	checks = make([]pullCheck, len(pulls))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, p := range pulls {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			c := pullCheck{pull: p}
			c.run, c.state, c.reason = s.latestCheck(ctx, sc.repo.RepoRef, p.HeadSHA, checkState)
			checks[i] = c
		}()
	}
	wg.Wait()
	return checks, more, false
}

// openPulls is openPullChecks as the repository page lists it.
func (s *server) openPulls(ctx context.Context, sc scope) (rows []pullRow, more int, failed bool) {
	checks, more, failed := s.openPullChecks(ctx, sc)
	rows = make([]pullRow, len(checks))
	for i, c := range checks {
		rows[i] = pullRow{
			Number: c.pull.Number, Title: c.pull.Title, State: c.state, Tone: toneOf(c.state), Reason: c.reason,
			Href: repoPath(sc, "/pull/"+strconv.Itoa(c.pull.Number)), Rerun: rerunStep(c.run),
		}
	}
	return rows, more, failed
}

// targetMoved is the reason code of a pull request the sweep withdrew because the target branch moved under it.
const targetMoved = "target_moved"

// rerunStep is the next step of a pull request whose check records target_moved, in the comment's words; "" for any
// other check. The sweep records it in required mode only. The words and the module names are the recorded Next of the
// reason, which feedback sanitized when it wrote it: nothing is parsed out of the detail.
func rerunStep(run *api.CheckRun) string {
	if run == nil || run.Conclusion != conclusionActionRequired {
		return ""
	}
	recorded := actionContext(*run)
	if code, _, _ := strings.Cut(recorded.Reason, " ("); code != targetMoved {
		return ""
	}
	return gate.RecoveryStep(targetMoved, cmp.Or(recorded.Next, updateBranchSentence))
}

// updateBranchSentence is the step when the check records the move without saying more.
const updateBranchSentence = "The target branch moved. Update the branch to measure it again."
