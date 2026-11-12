package ingest

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// Rejection codes of envelope 2 only.
const (
	codeWorkflowPath = "workflow_path"
	codeStaleRun     = "stale_run"
)

func (r *receiver) ReceiveV2(ctx context.Context, req *http.Request) (Upload4, error) {
	start := time.Now()
	f := facts{v2: true}
	up, err := r.receiveV2(ctx, req, &f)
	f.log(ctx, envelope.KindReport, up.Problems, err, time.Since(start))
	return up, err
}

// receiveV2 runs the envelope 2 step table in order. Every error return carries the zero Upload4.
func (r *receiver) receiveV2(ctx context.Context, req *http.Request, f *facts) (Upload4, error) {
	if req.Method != http.MethodPost {
		return Upload4{}, reject(http.StatusBadRequest, codeMethodNotAllowed)
	}
	body, err := readBody(req)
	if err != nil {
		return Upload4{}, err
	}
	raw := req.Header.Get(envelopeHeader)
	if raw == "" {
		return Upload4{}, reject(http.StatusBadRequest, codeEnvelopeMissing)
	}
	// DecodeV2 also checks that payload_digest names these exact bytes.
	env, err := envelope.DecodeV2([]byte(raw), body)
	if err != nil {
		return Upload4{}, reject(http.StatusBadRequest, codeEnvelopeInvalid)
	}
	f.digest = env.PayloadDigest
	claims, err := r.verify(ctx, req)
	if err != nil {
		return Upload4{}, err
	}
	f.repositoryID, f.runID, f.runAttempt = claims.RepositoryID, claims.RunID, claims.RunAttempt
	f.runnerEnvironment = claims.RunnerEnvironment

	repo, run, err := r.tenant(ctx, env.Repository, claims)
	if err != nil {
		return Upload4{}, err
	}
	f.installationID = repo.InstallationID
	// Only the fixed workflow file uploads envelope 2 (M2 D15), as the run's own top-level workflow at the run's ref.
	// A job of another workflow that calls archfit.yaml as a reusable workflow, from this repository at another ref
	// or from another repository, never qualifies: the file it ran is not the one the gate compares at RunSHA.
	if run.WorkflowPath != WorkflowPath || oidc.WorkflowPath(claims.JobWorkflowRef, repo.FullName) != WorkflowPath ||
		!strings.HasSuffix(claims.JobWorkflowRef, "@"+claims.Ref) {
		return Upload4{}, reject(http.StatusForbidden, codeWorkflowPath)
	}

	b, problems, err := r.bindV2(ctx, claims, repo, run, env.PullRequest)
	if err != nil {
		return Upload4{}, err
	}
	if err := r.latestRun(ctx, repo, run, b.PullRequest); err != nil {
		return Upload4{}, err
	}
	if !envelopeMatchesV2(env, b, repo.DefaultBranch) {
		problems.add(ProblemEnvelopeMismatch)
	}

	up := Upload4{Binding: b, Envelope: env, Payload: body}
	res, err := result.Decode(body)
	switch {
	case err != nil, res.Status == result.StatusWaiting: // the App sets waiting; the engine never sends it
		problems.add(ProblemReportInvalid)
	default:
		up.Result = &res
		if !resultMatches(res, env) {
			problems.add(ProblemEnvelopeMismatch)
		}
	}
	up.Problems = problems.list()
	return up, nil
}

// bindV2 binds the upload to the run's event: an open pull request into the default branch, a push to the default
// branch, or a merge group the default branch's queue lists. The rejections are the rate limit, a moved head and a
// merge group the queue no longer lists.
func (r *receiver) bindV2(ctx context.Context, claims oidc.Claims, repo api.Repo, run api.WorkflowRun, hint int) (Binding4, problemSet, error) {
	problems := problemSet{}
	b := Binding4{
		Repo:       repo,
		Event:      run.Event,
		HeadSHA:    run.HeadSHA,
		RunSHA:     claims.SHA,
		Fork:       run.HeadRepo != repo.FullName,
		RunID:      claims.RunID,
		RunAttempt: claims.RunAttempt,
	}
	switch run.Event {
	case eventPullRequest:
		pull, err := r.resolvePull(ctx, repo, run, hint)
		if err != nil {
			return Binding4{}, nil, err
		}
		if pull == nil {
			problems.add(ProblemEnvelopeMismatch)
			break
		}
		b.PullRequest = pull.Number
		b.Fork = b.Fork || pull.HeadRepo != repo.FullName
		if pull.State != pullStateOpen {
			problems.add(ProblemEnvelopeMismatch)
		}
		// The default branch is the only protected target: a pull request into another base never passes.
		if pull.BaseRef != repo.DefaultBranch {
			problems.add(ProblemUnsupportedEvent)
		}
	case eventPush:
		if claims.Ref != branchRefPrefix+repo.DefaultBranch {
			problems.add(ProblemUnsupportedEvent)
		}
	case eventMergeGroup:
		if err := r.queued(ctx, repo, run.HeadSHA); err != nil {
			return Binding4{}, nil, err
		}
	default:
		problems.add(ProblemUnsupportedEvent)
	}
	if b.Fork {
		problems.add(ProblemFork)
	}
	return b, problems, nil
}

// latestRun refuses the upload when the workflow has a newer run of the same event for the same head (409
// stale_run): only the latest attempt of the latest run decides. "Newer" is GitHub's creation time, which a re-run
// keeps, then the run id. A run of a pull request counts against another pull request's only when GitHub
// associates it with that pull request or with none.
func (r *receiver) latestRun(ctx context.Context, repo api.Repo, run api.WorkflowRun, pull int) error {
	runs, err := r.runs.HeadRuns(ctx, repo.RepoRef, WorkflowPath, run.HeadSHA)
	if err != nil {
		return apiFailure(err, "")
	}
	for _, other := range runs {
		if other.ID == run.ID || other.Event != run.Event {
			continue
		}
		if run.Event == eventPullRequest && len(other.PullRequests) > 0 && !slices.Contains(other.PullRequests, pull) {
			continue
		}
		if other.CreatedAt.After(run.CreatedAt) || other.CreatedAt.Equal(run.CreatedAt) && other.ID > run.ID {
			return reject(http.StatusConflict, codeStaleRun)
		}
	}
	return nil
}

// envelopeMatchesV2 compares every envelope 2 field GitHub also states. The repository was compared with the token.
func envelopeMatchesV2(env envelope.EnvelopeV2, b Binding4, defaultBranch string) bool {
	return env.Event == b.Event &&
		env.PullRequest == b.PullRequest &&
		env.HeadSHA == b.HeadSHA &&
		env.RunID == b.RunID &&
		env.RunAttempt == b.RunAttempt &&
		env.TargetRef == branchRefPrefix+defaultBranch
}

// resultMatches ties the result to the envelope: the same policy digest, the same head, and the engine the
// envelope names ("v4.0.0" in the envelope is "4.0.0" in the result).
func resultMatches(res result.Result, env envelope.EnvelopeV2) bool {
	return res.Policy.Digest == env.PolicyDigest &&
		res.Head == env.HeadSHA &&
		strings.HasPrefix(env.EngineVersion, "v") && res.Engine == strings.TrimPrefix(env.EngineVersion, "v")
}
