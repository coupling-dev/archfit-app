package ingest

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

const (
	eventPullRequest = "pull_request"
	eventPush        = "push"
	eventDispatch    = "workflow_dispatch"

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

// eligible is the event table of step 13.
func eligible(kind envelope.Kind, event string, onDefaultBranch bool) bool {
	switch event {
	case eventPush:
		// A re-anchor is a dispatched run only: it rewrites a protected baseline on purpose, never from a push.
		return onDefaultBranch && kind != envelope.KindReanchor
	case eventPullRequest:
		return kind == envelope.KindReport
	case eventDispatch:
		return kind == envelope.KindDiscovery || kind == envelope.KindBaseline || kind == envelope.KindReanchor
	}
	return false
}

// bind builds the binding from the claims, the run and the resolved pull (steps 13 to 15).
// The only rejections here are the rate limit and a moved head.
func (r *receiver) bind(ctx context.Context, kind envelope.Kind, env envelope.Envelope, claims oidc.Claims,
	repo api.Repo, run api.WorkflowRun,
) (Binding, problemSet, error) {
	problems := problemSet{}
	branch, onBranch := strings.CutPrefix(claims.Ref, branchRefPrefix)
	if !eligible(kind, run.Event, onBranch && branch == repo.DefaultBranch) {
		problems.add(ProblemUnsupportedEvent)
	}

	b := Binding{
		Repo:         repo,
		Event:        run.Event,
		HeadSHA:      run.HeadSHA,
		AnalyzedSHA:  run.HeadSHA,
		BaseRef:      repo.DefaultBranch,
		Fork:         run.HeadRepo != repo.FullName, // also the head repository of a workflow_run run
		RunID:        claims.RunID,
		RunAttempt:   claims.RunAttempt,
		WorkflowPath: oidc.WorkflowPath(claims.JobWorkflowRef, repo.FullName),
	}
	if onBranch {
		b.BaseRef = branch
	}

	if run.Event == eventPullRequest {
		pull, err := r.resolvePull(ctx, repo, run, env.PullRequest)
		if err != nil {
			return Binding{}, nil, err
		}
		if pull == nil {
			problems.add(ProblemEnvelopeMismatch)
		} else {
			if pull.State != pullStateOpen {
				problems.add(ProblemEnvelopeMismatch)
			}
			b.PullRequest = pull.Number
			b.BaseRef, b.BaseSHA, b.MergeBaseSHA = pull.BaseRef, pull.BaseSHA, pull.MergeBaseSHA
			b.AnalyzedSHA, b.Integration = landing(pull, run.HeadSHA)
			b.Fork = b.Fork || pull.HeadRepo != repo.FullName
		}
	}
	if b.Fork {
		problems.add(ProblemFork)
	}
	if !envelopeMatches(env, b) {
		problems.add(ProblemEnvelopeMismatch)
	}
	return b, problems, nil
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

// landing is what a pull request run must have measured, from GitHub's own answer and never from the envelope: the
// head alone when GitHub says it conflicts, else GitHub's test merge commit. While GitHub has not computed the merge
// there is no answer, and no envelope matches it: the run is made again.
func landing(pull *api.Pull, head string) (analyzed, integration string) {
	switch {
	case pull.Mergeable != nil && !*pull.Mergeable:
		return head, envelope.IntegrationConflicts
	case pull.MergeCommitSHA != "":
		return pull.MergeCommitSHA, envelope.IntegrationMerged
	}
	return "", ""
}

// envelopeMatches is step 15. Repository was compared in step 7.
func envelopeMatches(env envelope.Envelope, b Binding) bool {
	return env.Event == b.Event &&
		env.PullRequest == b.PullRequest &&
		env.HeadSHA == b.HeadSHA &&
		env.BaseSHA == b.BaseSHA &&
		env.MergeBaseSHA == b.MergeBaseSHA &&
		env.AnalyzedSHA == b.AnalyzedSHA &&
		env.Integration == b.Integration &&
		env.RunID == b.RunID &&
		env.RunAttempt == b.RunAttempt
}
