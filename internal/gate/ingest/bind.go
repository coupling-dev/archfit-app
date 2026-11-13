package ingest

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	eventPullRequest = "pull_request"
	eventPush        = "push"

	eventMergeGroup = "merge_group"

	branchRefPrefix   = "refs/heads/"
	pullStateOpen     = "open"
	maxPullCandidates = 10
)

type problemSet map[Problem]bool

func (s problemSet) add(p Problem) { s[p] = true }

func (s problemSet) list() []Problem {
	var out []Problem
	for _, p := range problemOrder {
		if s[p] {
			out = append(out, p)
		}
	}
	return out
}

// queued requires a merge group of the default branch's queue to have head as its commit, by GitHub's GraphQL entries.
// The run's queue branch name is never read: only an entry GitHub lists binds the group. None is 409 stale_group: the
// group was destroyed, rebuilt or merged, or belongs to another branch's queue, which the App does not gate.
func (r *receiver) queued(ctx context.Context, repo api.Repo, head string) error {
	entries, err := r.queues.MergeQueue(ctx, repo.RepoRef, repo.DefaultBranch)
	if err != nil {
		return apiFailure(err, "")
	}
	for _, e := range entries {
		if e.HeadSHA == head {
			return nil
		}
	}
	return reject(http.StatusConflict, codeStaleGroup)
}

// resolvePull finds the one pull request whose head is the run's head. nil means the
// association is missing or ambiguous. hint is the envelope's number: a lookup key
// for fork runs only, every property of the result comes from GitHub.
func (r *receiver) resolvePull(ctx context.Context, repo api.Repo, run api.WorkflowRun, hint int) (*api.Pull, error) {
	candidates := run.PullRequests
	if len(candidates) == 0 && hint > 0 {
		candidates = []int{hint}
	}
	if len(candidates) == 0 || len(candidates) > maxPullCandidates {
		return nil, nil
	}
	var matches []api.Pull
	for _, number := range candidates {
		pull, err := r.pulls.Pull(ctx, repo.RepoRef, number)
		if errors.Is(err, api.ErrNotFound) {
			continue // a number GitHub does not know is no association
		}
		if err != nil {
			return nil, apiFailure(err, "")
		}
		if pull.HeadSHA == run.HeadSHA {
			matches = append(matches, pull)
		} else if len(candidates) == 1 {
			return nil, reject(http.StatusConflict, codeStaleHead)
		}
	}
	if len(matches) != 1 {
		return nil, nil
	}
	return &matches[0], nil
}
