package ui

import (
	"context"
	"net/http"
	"sort"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	maxOverviewRepos = 100
	parallelLookups  = 8

	checkState = "archfit/state"
	checkGate  = "archfit/gate"
)

type overviewRow struct {
	FullName string
	State    string // a word, never a number
	Reason   string // the check run's title
}

type overviewBody struct {
	Rows      []overviewRow
	Truncated bool
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	u, ok := s.sessions.User(r)
	if !ok {
		s.render(w, r, http.StatusOK, "landing", page{Title: "Archfit"})
		return
	}
	sc := scope{user: u}
	logOf(r).userID = u.UserID

	repos, err := s.gh.users.UserRepos(r.Context(), u.Token)
	if err != nil {
		if !s.endIfTokenRejected(w, r, err) {
			s.fail(w, r, sc, err)
		}
		return
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].FullName < repos[j].FullName })
	body := overviewBody{Truncated: len(repos) > maxOverviewRepos}
	if body.Truncated {
		repos = repos[:maxOverviewRepos]
	}

	body.Rows = make([]overviewRow, len(repos))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			state, reason := s.defaultBranchState(r.Context(), repo)
			body.Rows[i] = overviewRow{FullName: repo.FullName, State: state, Reason: reason}
		}()
	}
	wg.Wait()
	s.render(w, r, http.StatusOK, "overview", s.newPage(r, sc, "Repositories", body))
}

// defaultBranchState reads archfit/state on the default-branch head. A failed lookup is "unknown" for this row only.
func (s *server) defaultBranchState(ctx context.Context, repo api.Repo) (state, reason string) {
	head, err := s.gh.files.BranchHead(ctx, repo.RepoRef, repo.DefaultBranch)
	if err != nil {
		return stateUnknown, ""
	}
	return s.checkWord(ctx, repo.RepoRef, head, checkState)
}

// checkWord is the latest run of one check on a commit, as a word and its reason title.
func (s *server) checkWord(ctx context.Context, ref api.RepoRef, headSHA, name string) (state, reason string) {
	runs, err := s.gh.checks.CheckRuns(ctx, ref, headSHA, name)
	if err != nil {
		return stateUnknown, ""
	}
	if len(runs) == 0 {
		return stateNone, ""
	}
	latest := runs[0]
	for _, run := range runs[1:] {
		if run.StartedAt.After(latest.StartedAt) {
			latest = run
		}
	}
	return conclusionWord(latest.Conclusion), latest.Title
}
