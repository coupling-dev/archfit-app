package onboarding

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

type traceKey struct{}

// trace records each GitHub call of one probed row, as offsets from the request start. It logs one line per call and
// keeps no call's content: only the operation, its timing and a result class.
type trace struct {
	row   int
	start time.Time
}

// WithRowTrace makes the GitHub calls Setup makes under ctx log their timeline for row, offset from start.
func WithRowTrace(ctx context.Context, row int, start time.Time) context.Context {
	return context.WithValue(ctx, traceKey{}, trace{row: row, start: start})
}

// observe starts a call's timer and returns the function that logs it once the call ends.
func observe(ctx context.Context, op string) func(error) {
	t, ok := ctx.Value(traceKey{}).(trace)
	if !ok {
		return func(error) {}
	}
	begin := time.Since(t.start)
	return func(err error) {
		slog.Info("ui probe call", "row", t.row, "op", op,
			"start_ms", float64(begin.Microseconds())/1000, "end_ms", float64(time.Since(t.start).Microseconds())/1000,
			"result", resultClass(err))
	}
}

func resultClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, api.ErrNotFound):
		return "not_found"
	case errors.Is(err, api.ErrRateLimited):
		return "rate_limited"
	default:
		return "error"
	}
}

// tracedGitHub is setupGitHub with each call timed under the row trace. It adds no call and changes no result.
type tracedGitHub struct{ setupGitHub }

func (g tracedGitHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	done := observe(ctx, "File")
	raw, blob, err := g.setupGitHub.File(ctx, r, ref, path)
	done(err)
	return raw, blob, err
}

func (g tracedGitHub) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	done := observe(ctx, "BranchHead")
	sha, err := g.setupGitHub.BranchHead(ctx, r, branch)
	done(err)
	return sha, err
}

func (g tracedGitHub) OpenPulls(ctx context.Context, r api.RepoRef) ([]api.Pull, error) {
	done := observe(ctx, "OpenPulls")
	pulls, err := g.setupGitHub.OpenPulls(ctx, r)
	done(err)
	return pulls, err
}

func (g tracedGitHub) PublicTagCommit(ctx context.Context, repo, tag string) (string, error) {
	done := observe(ctx, "PublicTagCommit")
	sha, err := g.setupGitHub.PublicTagCommit(ctx, repo, tag)
	done(err)
	return sha, err
}

func (g tracedGitHub) BranchPolicy(ctx context.Context, r api.RepoRef, branch string) ([]api.RepositoryRule, error) {
	done := observe(ctx, "BranchPolicy")
	rules, err := g.setupGitHub.BranchPolicy(ctx, r, branch)
	done(err)
	return rules, err
}

func (g tracedGitHub) AppIntegrationID(ctx context.Context) (int64, error) {
	done := observe(ctx, "AppIntegrationID")
	id, err := g.setupGitHub.AppIntegrationID(ctx)
	done(err)
	return id, err
}

func (g tracedGitHub) ListRulesets(ctx context.Context, r api.RepoRef) ([]api.Ruleset, error) {
	done := observe(ctx, "ListRulesets")
	sets, err := g.setupGitHub.ListRulesets(ctx, r)
	done(err)
	return sets, err
}

func (g tracedGitHub) GetRuleset(ctx context.Context, r api.RepoRef, id int64) (api.Ruleset, error) {
	done := observe(ctx, "GetRuleset")
	set, err := g.setupGitHub.GetRuleset(ctx, r, id)
	done(err)
	return set, err
}
