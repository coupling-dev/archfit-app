package ui

import (
	"context"
	"errors"
	"net/http"
	"strconv"

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

type graphNode struct {
	graph.Node
	Href string // "" when the module's line is unknown
}

type graphBody struct {
	Unavailable string // why there is no graph; "" when View is set
	View        graph.View
	Nodes       []graphNode
	StaleReason string
	PolicyURL   string     // the policy text the nodes come from; a module's line is PolicyURL + "#L<line>"
	Pull        *pullScope // the pull request whose report this is; nil on the default-branch graph
}

// setView sets the view and links each declared module to its line in the policy text.
func (b *graphBody) setView(v graph.View, policyURL string) {
	b.View, b.PolicyURL = v, policyURL
	for _, n := range v.Nodes {
		node := graphNode{Node: n}
		if n.PolicyLine != 0 {
			node.Href = policyURL + "#L" + strconv.Itoa(n.PolicyLine)
		}
		b.Nodes = append(b.Nodes, node)
	}
}

func (s *server) graphPage(w http.ResponseWriter, r *http.Request, sc scope) {
	body, err := s.buildGraph(r.Context(), sc)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	s.render(w, r, http.StatusOK, "graph", s.newPage(r, sc, "Graph", body))
}

// buildGraph never returns a partial graph: either a whole view or the reason there is none.
func (s *server) buildGraph(ctx context.Context, sc scope) (graphBody, error) {
	payload, err := s.latestReport(ctx, sc.repo)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, errNoSettings) {
		return graphBody{Unavailable: noReport}, nil
	}
	if err != nil {
		return graphBody{}, err
	}
	// The artifact carries no envelope that names its engine release. The graph only displays it, and the
	// newest release's key set holds every older one, so the newest is the one to read it with.
	releases := report.EngineVersions()
	rep, err := report.Decode(payload, releases[len(releases)-1])
	if err != nil {
		return graphBody{Unavailable: reportUnavailable}, nil
	}

	branch, _ := editBranch(sc.user.Login, "")
	draft, text, err := s.current(ctx, sc.repo, branch)
	if err != nil {
		return graphBody{}, err
	}
	var body graphBody
	if _, err := policy.Modules(text); err != nil && isOpen(draft) {
		text, _, err = s.gh.files.File(ctx, sc.repo.RepoRef, sc.repo.DefaultBranch, policy.PolicyPath)
		if err != nil && !errors.Is(err, api.ErrNotFound) {
			return graphBody{}, err
		}
		body.StaleReason = draftBroken
	}
	view := graph.Build(rep, text)
	if body.StaleReason != "" {
		view.Stale = true
	}
	body.setView(view, repoPath(sc, "/policy"))
	return body, nil
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
