package ui

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	reportArtifact = report.ArtifactName

	// GitHub run values. Only a push run uploads the report; dispatched runs upload policy or baseline artifacts.
	eventPush         = "push"
	conclusionSuccess = "success"

	noReport          = "No report available. Reports come from push runs on the default branch, and GitHub keeps run artifacts for a limited time."
	reportUnavailable = "Report unavailable: the latest report could not be read."
	draftBroken       = "The draft does not parse. The graph shows the policy on the default branch."
)

// graphJSON is a graph page: the view graph.Build made, or why there is none. The web app renders every string in it
// as text, and links only through its safe-href rule.
type graphJSON struct {
	Unavailable string      `json:"unavailable"` // why there is no graph; "" when View is set
	View        *graph.View `json:"view"`
	StaleReason string      `json:"stale_reason"`
	PolicyHref  string      `json:"policy_href"` // the policy text the nodes come from; a module's line is policy_href + "#L<line>"
	Branch      string      `json:"branch"`      // the default branch, whose latest report the repository graph shows
	Pull        *pullHead   `json:"pull"`        // the pull request whose report this is; nil on the repository graph
	Added       int         `json:"added"`       // links containing introduced finding evidence (graph.Link.Origin), not newly added pairs/sites
}

// apiGraph is the repository graph: GET /api/repos/{o}/{n}/graph, from the latest report of the default branch.
func (s *server) apiGraph(r *http.Request, sc scope) (any, error) {
	ctx := r.Context()
	body := graphJSON{Branch: sc.repo.DefaultBranch}
	// The report and the policy text do not depend on each other: they are read side by side, and the text counts
	// only once there is a report, as when they were read in turn.
	var (
		rep           *report.Report
		unavailable   string
		err, draftErr error
		draft         proposal.Draft
		text          []byte
	)
	branch, _ := editBranch(sc.user.Login, "")
	together(
		func() { rep, unavailable, err = s.latestDecoded(ctx, sc.repo) },
		func() { draft, text, draftErr = s.current(ctx, sc.repo, branch) },
	)
	if err != nil || unavailable != "" {
		body.Unavailable = unavailable
		return body, err
	}
	if draftErr != nil {
		return nil, draftErr
	}
	if _, err := policy.Modules(text); err != nil && isOpen(draft) {
		text, _, err = s.gh.files.File(ctx, sc.repo.RepoRef, sc.repo.DefaultBranch, policy.PolicyPath)
		if err != nil && !errors.Is(err, api.ErrNotFound) {
			return nil, err
		}
		body.StaleReason = draftBroken
	}
	view := graph.Build(*rep, text)
	if body.StaleReason != "" {
		view.Stale = true
	}
	body.View, body.PolicyHref = &view, repoPath(sc, "/policy")
	return body, nil
}

// repoFindingsJSON is the overview's "Findings on <default branch>": the latest report of the default branch in the
// comment's groups, or why there is none. Like the repository graph, it shows no origin (latestDecoded).
type repoFindingsJSON struct {
	Unavailable string `json:"unavailable"`
	Branch      string `json:"branch"`
	NoBaseline  bool   `json:"no_baseline"`
	// AttentionReason is why the engine's verdict is needs_attention when it is: "" for any other verdict.
	AttentionReason string `json:"attention_reason"`
	findingsJSON
}

// apiFindings is GET /api/repos/{o}/{n}/findings. The overview loads it on its own, so a slow artifact download never
// holds up the rest of the page.
func (s *server) apiFindings(r *http.Request, sc scope) (any, error) {
	ctx := r.Context()
	body := repoFindingsJSON{Branch: sc.repo.DefaultBranch}
	rep, unavailable, err := s.latestDecoded(ctx, sc.repo)
	if err != nil || unavailable != "" {
		body.Unavailable = unavailable
		return body, err
	}
	body.NoBaseline, body.AttentionReason = noBaseline(*rep), attentionReason(*rep)
	fillFindings(&body.findingsJSON, *rep)
	return body, nil
}

// latestDecoded is the latest default-branch report, or the reason there is none. "Added by this PR" belongs to a pull
// request, so the report's origins are dropped even when a default-branch report says it compared.
func (s *server) latestDecoded(ctx context.Context, repo api.Repo) (*report.Report, string, error) {
	payload, err := s.latestReport(ctx, repo)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, errNoSettings) {
		return nil, noReport, nil
	}
	if err != nil {
		return nil, "", err
	}
	// The artifact carries no envelope that names its engine release. The App only displays it, and the newest
	// release's key set holds every older one, so the newest is the one to read it with.
	releases := report.EngineVersions()
	rep, err := report.Decode(payload, releases[len(releases)-1])
	if err != nil {
		return nil, reportUnavailable, nil
	}
	rep.Comparison.OriginStatus = ""
	return &rep, "", nil
}

// latestReport downloads the report artifact of the latest successful default-branch push run of the settings
// workflow. A dispatched run or a run still in progress carries no report, so it must not hide the last one.
//
// Each read names the next one's parameter: the settings the workflow, its latest run the artifact. The last read
// left both as a hint, so all three start side by side, and the hinted two count only when the fresh settings and
// run name the same workflow and run: a warm page waits for one read, not for three.
func (s *server) latestReport(ctx context.Context, repo api.Repo) ([]byte, error) {
	key := hintKey{repo.InstallationID, repo.FullName, 0}
	h, hinted := s.hints.get(key)
	f := api.RunFilter{Branch: repo.DefaultBranch, Event: eventPush, Status: conclusionSuccess}
	latest := func(workflow string) (api.WorkflowRun, error) {
		return s.gh.actions.LatestRun(ctx, repo.RepoRef, workflow, f)
	}
	artifactID := s.artifactIDs(ctx, repo.RepoRef)
	var (
		set      settings.Settings
		err      error
		run      guess[string, api.WorkflowRun]
		artifact guess[int64, int64]
	)
	reads := []func(){func() { set, err = s.settings(ctx, repo) }}
	if hinted {
		reads = append(reads, run.ahead(h.workflow, latest))
		if h.run != 0 {
			reads = append(reads, artifact.ahead(h.run, artifactID))
		}
	}
	together(reads...)
	if errors.Is(err, errNoSettings) {
		s.hints.drop(key)
	}
	if err != nil {
		return nil, err
	}
	latestRun, err := run.at(set.Workflow, latest)
	if errors.Is(err, api.ErrNotFound) {
		s.hints.put(key, hint{workflow: set.Workflow})
	}
	if err != nil {
		return nil, err
	}
	s.hints.put(key, hint{workflow: set.Workflow, run: latestRun.ID})
	id, err := artifact.at(latestRun.ID, artifactID)
	if err != nil {
		return nil, err
	}
	return s.reportByID(ctx, repo.RepoRef, id)
}
