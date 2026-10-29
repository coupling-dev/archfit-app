package ui

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

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
			main := s.defaultBranchState(r.Context(), repo)
			body.Repos[i] = overviewRow{FullName: repo.FullName, State: main.state, Tone: toneOf(main.state), Reason: main.reason}
		}()
	}
	wg.Wait()
	return body, nil
}

// branchCheck is archfit/state on a commit of the default branch: the word and title of its latest run, and which
// commit and when.
type branchCheck struct {
	state, reason string
	sha           string    // the branch tip the check was read on
	at            time.Time // when the check started; zero without a check
}

// defaultBranchState reads archfit/state on the default-branch head. A failed lookup is "unknown" for this row only.
func (s *server) defaultBranchState(ctx context.Context, repo api.Repo) branchCheck {
	head, err := s.gh.files.BranchHead(ctx, repo.RepoRef, repo.DefaultBranch)
	if err != nil {
		return branchCheck{state: stateUnknown}
	}
	run, state, reason := s.latestCheck(ctx, repo.RepoRef, head, checkState)
	out := branchCheck{state: state, reason: reason, sha: head}
	if run != nil {
		out.at = run.StartedAt
	}
	return out
}

// checkWord is the latest run of one check on a commit, as a word and its reason title.
func (s *server) checkWord(ctx context.Context, ref api.RepoRef, headSHA, name string) (state, reason string) {
	_, state, reason = s.latestCheck(ctx, ref, headSHA, name)
	return state, reason
}

// latestCheck is the latest run of one check on a commit, as the run, a word and its reason title; the run is nil when
// there is none or the read failed.
func (s *server) latestCheck(ctx context.Context, ref api.RepoRef, headSHA, name string) (run *api.CheckRun, state, reason string) {
	runs, err := s.gh.checks.CheckRuns(ctx, ref, headSHA, name)
	if err != nil {
		return nil, stateUnknown, ""
	}
	if len(runs) == 0 {
		return nil, stateNone, ""
	}
	latest := runs[0]
	for _, run := range runs[1:] {
		if run.StartedAt.After(latest.StartedAt) {
			latest = run
		}
	}
	return &latest, stateWord(latest.Conclusion, latest.Title), latest.Title
}
