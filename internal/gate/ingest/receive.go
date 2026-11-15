package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
)

const (
	envelopeHeader = "X-Archfit-Envelope" // compact envelope JSON, verbatim
	bearerPrefix   = "Bearer "
)

// Rejection codes of the step table.
const (
	codeMethodNotAllowed = "method_not_allowed"
	codePayloadTooLarge  = "payload_too_large"
	codeEnvelopeMissing  = "envelope_missing"
	codeEnvelopeInvalid  = "envelope_invalid"

	codeTokenMissing       = "token_missing"
	codeTokenInvalid       = "token_invalid"
	codeOIDCUnavailable    = "oidc_unavailable"
	codeRepositoryMismatch = "repository_mismatch"
	codeNotInstalled       = "not_installed"
	codeNotAdmitted        = "installation_not_approved"
	codeAdmissionDown      = "admission_unavailable"
	codeRunNotFound        = "run_not_found"
	codeRunMismatch        = "run_mismatch"
	codeStaleAttempt       = "stale_attempt"
	codeStaleHead          = "stale_head"
	codeStaleGroup         = "stale_group"

	codeRateLimited = "rate_limited"
)

type receiver struct {
	repos    api.Repos
	pulls    api.Pulls
	actions  api.Actions
	runs     api.WorkflowRuns // envelope 2 only
	queues   api.MergeQueues
	verifier oidc.Verifier
	audience string
	admit    Admitter
}

func reject(status int, code string) error {
	return &Rejection{Status: status, Code: code}
}

// apiFailure maps a GitHub API error: the rate limit is a 429, ErrNotFound is the step's
// 403 when notFoundCode is set, and anything else stays a plain error for the caller's 503.
func apiFailure(err error, notFoundCode string) error {
	switch {
	case errors.Is(err, api.ErrRateLimited):
		return reject(http.StatusTooManyRequests, codeRateLimited)
	case notFoundCode != "" && errors.Is(err, api.ErrNotFound):
		return reject(http.StatusForbidden, notFoundCode)
	}
	return fmt.Errorf("ingest: github api: %w", err)
}

// readBody reads the body once, capped at maxPayloadBytes for the result.
func readBody(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, req.Body, maxPayloadBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, reject(http.StatusRequestEntityTooLarge, codePayloadTooLarge)
		}
		return nil, fmt.Errorf("ingest: read body: %w", err)
	}
	return body, nil
}

func (r *receiver) verify(ctx context.Context, req *http.Request) (oidc.Claims, error) {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), bearerPrefix)
	if !ok || token == "" {
		return oidc.Claims{}, reject(http.StatusUnauthorized, codeTokenMissing)
	}
	claims, err := r.verifier.Verify(ctx, token, r.audience)
	switch {
	case errors.Is(err, oidc.ErrUnavailable):
		return oidc.Claims{}, reject(http.StatusServiceUnavailable, codeOIDCUnavailable)
	case err != nil:
		return oidc.Claims{}, reject(http.StatusUnauthorized, codeTokenInvalid)
	}
	return claims, nil
}

// tenant authenticates the tenant (steps 7 to 12): the repository and the run come from
// the verified claims and the GitHub API only.
// repository is the envelope's claim; it must be the token's.
func (r *receiver) tenant(ctx context.Context, repository string, claims oidc.Claims) (api.Repo, api.WorkflowRun, error) {
	fail := func(err error) (api.Repo, api.WorkflowRun, error) { return api.Repo{}, api.WorkflowRun{}, err }

	if claims.Repository != repository {
		return fail(reject(http.StatusForbidden, codeRepositoryMismatch))
	}
	repo, err := r.repos.ResolveRepo(ctx, claims.Repository)
	if err != nil {
		return fail(apiFailure(err, codeNotInstalled))
	}
	if repo.ID != claims.RepositoryID {
		return fail(reject(http.StatusForbidden, codeRepositoryMismatch))
	}
	// An installation no operator approved costs nothing past this point: no run read, no report decoded.
	// An approval that cannot be read is a 503 the Action retries, never a refusal.
	owner, _, _ := strings.Cut(repo.FullName, "/")
	switch admitted, err := r.admit.Admitted(ctx, repo.InstallationID, owner); {
	case err != nil:
		return fail(reject(http.StatusServiceUnavailable, codeAdmissionDown))
	case !admitted:
		return fail(reject(http.StatusForbidden, codeNotAdmitted))
	}
	run, err := r.actions.Run(ctx, repo.RepoRef, claims.RunID)
	if err != nil {
		return fail(apiFailure(err, codeRunNotFound))
	}
	// Claims.SHA is the merge commit for pull_request runs, so it is compared for push and merge_group only: there it is
	// the commit the run checked out.
	if run.Event != claims.EventName || ((run.Event == eventPush || run.Event == eventMergeGroup) && run.HeadSHA != claims.SHA) {
		return fail(reject(http.StatusForbidden, codeRunMismatch))
	}
	if run.Attempt != claims.RunAttempt {
		return fail(reject(http.StatusConflict, codeStaleAttempt))
	}
	return repo, run, nil
}

// maxPayloadBytes caps an uploaded result at 5 MiB.
const maxPayloadBytes = 5 << 20
