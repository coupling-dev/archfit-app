package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	envelopeHeader = "X-Archfit-Envelope" // compact envelope JSON, verbatim
	bearerPrefix   = "Bearer "
)

// Rejection codes of the step table.
const (
	codeMethodNotAllowed   = "method_not_allowed"
	codePayloadTooLarge    = "payload_too_large"
	codeEnvelopeMissing    = "envelope_missing"
	codeEnvelopeInvalid    = "envelope_invalid"
	codeKindMismatch       = "kind_mismatch"
	codeTokenMissing       = "token_missing"
	codeTokenInvalid       = "token_invalid"
	codeOIDCUnavailable    = "oidc_unavailable"
	codeRepositoryMismatch = "repository_mismatch"
	codeNotInstalled       = "not_installed"
	codeRunNotFound        = "run_not_found"
	codeRunMismatch        = "run_mismatch"
	codeStaleAttempt       = "stale_attempt"
	codeStaleHead          = "stale_head"
	codeDigestMismatch     = "digest_mismatch"
	codeRateLimited        = "rate_limited"
)

type receiver struct {
	repos    api.Repos
	pulls    api.Pulls
	actions  api.Actions
	verifier oidc.Verifier
	audience string
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

func (r *receiver) Receive(ctx context.Context, req *http.Request, kind envelope.Kind) (Upload, error) {
	start := time.Now()
	var f facts
	up, err := r.receive(ctx, req, kind, &f)
	f.log(ctx, kind, up.Problems, err, time.Since(start))
	return up, err
}

// receive runs the step table in order. Every error return carries the zero Upload.
func (r *receiver) receive(ctx context.Context, req *http.Request, kind envelope.Kind, f *facts) (Upload, error) {
	if req.Method != http.MethodPost {
		return Upload{}, reject(http.StatusBadRequest, codeMethodNotAllowed)
	}
	body, err := readBody(req)
	if err != nil {
		return Upload{}, err
	}
	env, err := decodeEnvelope(req, kind)
	if err != nil {
		return Upload{}, err
	}
	claims, err := r.verify(ctx, req)
	if err != nil {
		return Upload{}, err
	}
	f.repositoryID, f.runID, f.runAttempt = claims.RepositoryID, claims.RunID, claims.RunAttempt
	f.runnerEnvironment = claims.RunnerEnvironment

	repo, run, err := r.tenant(ctx, env, claims)
	if err != nil {
		return Upload{}, err
	}
	f.installationID = repo.InstallationID

	binding, problems, err := r.bind(ctx, kind, env, claims, repo, run)
	if err != nil {
		return Upload{}, err
	}
	digest := envelope.PayloadDigest(body)
	f.digest = digest
	if digest != env.PayloadDigest {
		return Upload{}, reject(http.StatusBadRequest, codeDigestMismatch)
	}

	up := Upload{
		Binding:        binding,
		Identity:       manifest.Identity{EngineVersion: env.EngineVersion, ImageDigest: env.ImageDigest, Platform: env.Platform},
		Digest:         digest,
		BaselineDigest: env.BaselineDigest,
		LabelsDigest:   env.LabelsDigest,
		Payload:        body,
	}
	if kind == envelope.KindReport {
		// The key set is the one of the engine release the envelope claims; a release the decoder does not read
		// is an invalid report. The decode error may quote payload content; only the fact is kept.
		if rep, err := report.Decode(body, env.EngineVersion); err != nil {
			problems.add(ProblemReportInvalid)
		} else {
			up.Report = &rep
		}
	}
	up.Problems = problems.list()
	return up, nil
}

// readBody reads the body once, capped at report.MaxBytes for both kinds.
func readBody(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, req.Body, report.MaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, reject(http.StatusRequestEntityTooLarge, codePayloadTooLarge)
		}
		return nil, fmt.Errorf("ingest: read body: %w", err)
	}
	return body, nil
}

func decodeEnvelope(req *http.Request, kind envelope.Kind) (envelope.Envelope, error) {
	raw := req.Header.Get(envelopeHeader)
	if raw == "" {
		return envelope.Envelope{}, reject(http.StatusBadRequest, codeEnvelopeMissing)
	}
	env, err := envelope.Decode([]byte(raw))
	if err != nil {
		return envelope.Envelope{}, reject(http.StatusBadRequest, codeEnvelopeInvalid)
	}
	if env.Kind != kind {
		return envelope.Envelope{}, reject(http.StatusBadRequest, codeKindMismatch)
	}
	return env, nil
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
func (r *receiver) tenant(ctx context.Context, env envelope.Envelope, claims oidc.Claims) (api.Repo, api.WorkflowRun, error) {
	fail := func(err error) (api.Repo, api.WorkflowRun, error) { return api.Repo{}, api.WorkflowRun{}, err }

	if claims.Repository != env.Repository {
		return fail(reject(http.StatusForbidden, codeRepositoryMismatch))
	}
	repo, err := r.repos.ResolveRepo(ctx, claims.Repository)
	if err != nil {
		return fail(apiFailure(err, codeNotInstalled))
	}
	if repo.ID != claims.RepositoryID {
		return fail(reject(http.StatusForbidden, codeRepositoryMismatch))
	}
	run, err := r.actions.Run(ctx, repo.RepoRef, claims.RunID)
	if err != nil {
		return fail(apiFailure(err, codeRunNotFound))
	}
	// Claims.SHA is the merge commit for pull_request runs, so it is compared for push only.
	if run.Event != claims.EventName || (run.Event == eventPush && run.HeadSHA != claims.SHA) {
		return fail(reject(http.StatusForbidden, codeRunMismatch))
	}
	if run.Attempt != claims.RunAttempt {
		return fail(reject(http.StatusConflict, codeStaleAttempt))
	}
	return repo, run, nil
}
