package ui

import (
	"net/http"
	"sort"
	"sync"
)

const (
	maxOverviewRepos = 100
	parallelLookups  = 8
)

type overviewRow struct {
	FullName string `json:"full_name"`
	Waiting  bool   `json:"waiting,omitempty"` // no operator approved its installation: no state is read
	State    string `json:"state"`
}

type overviewBody struct {
	Repos     []overviewRow `json:"repos"`
	Truncated bool          `json:"truncated"`
}

// apiRepos lists the signed-in user's repositories with their stored repository state.
func (s *server) apiRepos(r *http.Request, sc scope) (any, error) {
	repos, err := s.gh.users.UserRepos(r.Context(), sc.user.Token)
	if err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].FullName < repos[j].FullName })
	body := overviewBody{Truncated: len(repos) > maxOverviewRepos}
	if body.Truncated {
		repos = repos[:maxOverviewRepos]
	}
	body.Repos = make([]overviewRow, len(repos))
	admitted := s.admittedSet(r.Context(), repos)
	failures := make([]error, len(repos))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, repo := range repos {
		if !admitted[repo.InstallationID] {
			body.Repos[i] = overviewRow{FullName: repo.FullName, Waiting: true, State: stateWaiting}
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			state, err := s.repoStates.RepoState(r.Context(), repo)
			failures[i] = err
			body.Repos[i] = overviewRow{FullName: repo.FullName, State: state}
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
