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
	originComparable = "comparable" // Comparison.OriginStatus: each finding's origin against the base branch is meaningful
	shortSHA         = 7

	noPolicy = "Neither the head nor the base branch holds the policy this report was computed from."
)

// pullScope is the header of a pull request graph: which pull request, which head, what its check says.
type pullScope struct {
	Number     int
	Title      string
	Head       string // short head SHA
	Headline   string // the archfit/state title
	Conclusion string
	URL        string // the pull request on GitHub
	Page       string // the pull request page of the App
	Report     bool   // the page has the report of the check
	Compared   bool   // the report compared with the base branch, so a link can be new in this pull request
	New        int    // links new in this pull request
}

func (s *server) pullGraphPage(w http.ResponseWriter, r *http.Request, sc scope) {
	number, ok := pullNumber(r)
	if !ok {
		s.fail(w, r, sc, api.ErrNotFound)
		return
	}
	body, err := s.buildPullGraph(r.Context(), sc, number)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	s.render(w, r, http.StatusOK, "graph", s.newPage(r, sc, "Pull request #"+strconv.Itoa(number)+" graph", body))
}

// buildPullGraph draws the report the pull request page shows: the run its archfit/state check names, never the
// default branch's latest report.
func (s *server) buildPullGraph(ctx context.Context, sc scope, number int) (graphBody, error) {
	pull, rep, err := s.readPull(ctx, sc, number)
	if err != nil {
		return graphBody{}, err
	}
	head := pull.Pull.HeadSHA
	scope := &pullScope{
		Number: pull.Pull.Number, Title: pull.Pull.Title, Head: head[:min(len(head), shortSHA)],
		Headline: pull.Headline, Conclusion: pull.Conclusion, URL: pull.Pull.URL,
		Page: repoPath(sc, "/pull/"+strconv.Itoa(number)),
	}
	body := graphBody{Pull: scope}
	if rep == nil {
		body.Unavailable = pull.Unavailable
		return body, nil
	}
	scope.Report = true
	scope.Compared = rep.Comparison.OriginStatus == originComparable
	text, ref, err := s.reportPolicy(ctx, sc, pull.Pull, *rep)
	if err != nil {
		return graphBody{}, err
	}
	view := graph.Build(*rep, text)
	if view.Stale {
		body.StaleReason = noPolicy
	}
	body.setView(view, "https://github.com/"+sc.repo.FullName+"/blob/"+ref+"/"+policy.PolicyPath)
	for _, l := range view.Links {
		if l.Introduced {
			scope.New++
		}
	}
	return body, nil
}

// reportPolicy finds the policy text the report was computed from, for the module names, layers and lines. The
// Action reads the policy from the head when the pull request changes it, else from the base branch: the head first,
// then the base, the first whose digest is the report's config_hash. With neither, the head's text (or the base's,
// when the head has none) and graph.Build marks the view stale. Display only: the gate never reads this text.
func (s *server) reportPolicy(ctx context.Context, sc scope, pull api.Pull, rep report.Report) (text []byte, ref string, err error) {
	for _, r := range []string{pull.HeadSHA, pull.BaseRef} {
		t, _, err := s.gh.files.File(ctx, sc.repo.RepoRef, r, policy.PolicyPath)
		if errors.Is(err, api.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if policy.Digest(t) == rep.Comparison.ConfigHash {
			return t, r, nil
		}
		if text == nil {
			text, ref = t, r
		}
	}
	if ref == "" {
		ref = pull.HeadSHA
	}
	return text, ref, nil
}
