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
	FullName string `json:"full_name"`
	Waiting  bool   `json:"waiting,omitempty"` // no operator approved its installation: no state is read
	State    string `json:"state"`             // a word, never a number
	Tone     string `json:"tone"`
	Reason   string `json:"reason"` // the check run's title
}

type overviewBody struct {
	Repos     []overviewRow `json:"repos"`
	Truncated bool          `json:"truncated"`
}

// apiRepos is the signed-in user's repositories, each with archfit/state on its default-branch head.
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
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, repo := range repos {
		if !admitted[repo.InstallationID] {
			body.Repos[i] = overviewRow{FullName: repo.FullName, Waiting: true, State: stateWaiting, Tone: "attention"}
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			state, reason := s.defaultBranchState(r.Context(), repo)
			body.Repos[i] = overviewRow{FullName: repo.FullName, State: state, Tone: toneOf(state), Reason: reason}
		}()
	}
	wg.Wait()
	return body, nil
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
	return stateWord(latest.Conclusion, latest.Title), latest.Title
}
