package ui

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	reportArtifact = "archfit-report"

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
	Added       int         `json:"added"`       // dependencies this pull request added (graph.Link.Origin)
}

// apiGraph is the repository graph: GET /api/repos/{o}/{n}/graph, from the latest report of the default branch.
func (s *server) apiGraph(r *http.Request, sc scope) (any, error) {
	ctx := r.Context()
	body := graphJSON{Branch: sc.repo.DefaultBranch}
	rep, unavailable, err := s.latestDecoded(ctx, sc.repo)
	if err != nil || unavailable != "" {
		body.Unavailable = unavailable
		return body, err
	}

	branch, _ := editBranch(sc.user.Login, "")
	draft, text, err := s.current(ctx, sc.repo, branch)
	if err != nil {
		return nil, err
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
	Blocked     bool   `json:"blocked"` // the engine's verdict is blocked: the overview never shows "attention" over it
	findingsJSON
}

// apiFindings is GET /api/repos/{o}/{n}/findings. The overview loads it on its own, so a slow artifact download never
// holds up the rest of the page.
func (s *server) apiFindings(r *http.Request, sc scope) (any, error) {
	body := repoFindingsJSON{Branch: sc.repo.DefaultBranch}
	rep, unavailable, err := s.latestDecoded(r.Context(), sc.repo)
	if err != nil || unavailable != "" {
		body.Unavailable = unavailable
		return body, err
	}
	body.NoBaseline, body.Blocked = noBaseline(*rep), isBlocked(*rep)
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
func (s *server) latestReport(ctx context.Context, repo api.Repo) ([]byte, error) {
	set, err := s.settings(ctx, repo)
	if err != nil {
		return nil, err
	}
	f := api.RunFilter{Branch: repo.DefaultBranch, Event: eventPush, Status: conclusionSuccess}
	run, err := s.gh.actions.LatestRun(ctx, repo.RepoRef, set.Workflow, f)
	if err != nil {
		return nil, err
	}
	return s.gh.actions.Artifact(ctx, repo.RepoRef, run.ID, reportArtifact, report.MaxBytes)
}
