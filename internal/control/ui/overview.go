package ui

import (
	"net/http"
	"sort"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
)

const (
	maxOverviewRepos = 100
	parallelLookups  = 8
	maxSetupProbes   = 25
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

// apiRepos lists repositories with cheap stored state and a bounded onboarding probe for not-set-up rows.
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

	// Setup reads a few GitHub objects per repository. Probe only the first 25 sorted rows that need setup;
	// approved-digest rows already have authoritative stored state and need no GitHub reads.
	probes := 0
	for i, repo := range repos {
		if !admitted[repo.InstallationID] || body.Repos[i].State != gate.RepoNotSetUp || probes >= maxSetupProbes {
			continue
		}
		probes++
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			setup, err := s.setups.Setup(r.Context(), repo)
			if err != nil {
				return // retain the stored "not set up" state; one GitHub failure must not break the list.
			}
			body.Repos[i].State = repoSetupStage(setup)
		}()
	}
	wg.Wait()
	return body, nil
}

func repoSetupStage(setup onboarding.Setup) string {
	switch {
	case !setup.Policy:
		return "no policy"
	case !setup.Workflow:
		return "needs workflow"
	case setup.State == "approved":
		return "active"
	default:
		return "awaiting approval"
	}
}
