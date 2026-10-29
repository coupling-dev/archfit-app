package sweep

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type sweeper struct {
	repos     api.Repos
	pulls     api.Pulls
	actions   api.Actions
	resolver  inputs.Resolver
	publisher feedback.Publisher
	admit     Admitter
	decisions Decisions
	cfg       Config
	graphs    *edgeCache // module graphs of verified reports, shared by the passes of this instance
}

// pass is one traversal. Nothing of it outlives Run or RunRepo.
//
// Its methods return a non-nil error only when the whole pass must stop (rate limit or cancelled ctx);
// every other failure is counted, logged and skipped where it happens.
type pass struct {
	*sweeper
	now     time.Time
	sum     Summary
	recheck bool                     // only rule 5c runs
	tips    map[api.RepoRef]*tipView // the default-branch tip of each repository, read at most once
}

func (s *sweeper) Run(ctx context.Context, now time.Time) (Summary, error) {
	p := &pass{sweeper: s, now: now}
	installations, err := s.repos.ListInstallations(ctx)
	if err != nil {
		if errors.Is(err, api.ErrRateLimited) {
			return p.finishPartial(err)
		}
		return Summary{}, fmt.Errorf("sweep: list installations: %w", err)
	}
	if len(installations) == 0 {
		return p.finish(nil)
	}
	installations = slices.Clone(installations)
	slices.SortFunc(installations, func(a, b api.Installation) int { return cmp.Compare(a.ID, b.ID) })
	// Rotating the start hourly keeps an early stop from starving the same tail; no cursor is stored.
	n := int64(len(installations))
	start := int(((now.Unix()/3600)%n + n) % n)
	for i := range installations {
		if err := p.installation(ctx, installations[(start+i)%len(installations)]); err != nil {
			return p.finishPartial(err)
		}
	}
	return p.finish(nil)
}

// RunRepo returns a rate limit as an error: its one repository is the whole pass, so a stop leaves the work
// undone and the caller retries it.
func (s *sweeper) RunRepo(ctx context.Context, r api.Repo, now time.Time) (Summary, error) {
	p := &pass{sweeper: s, now: now}
	return p.finish(p.repo(ctx, r))
}

// Recheck is rule 5c alone for r, after a push moved its default branch to tip. A branch that has moved on since is
// left to the push that moved it: that push starts its own recheck.
func (s *sweeper) Recheck(ctx context.Context, r api.Repo, tip string, now time.Time) (Summary, error) {
	p := &pass{sweeper: s, now: now, recheck: true}
	head, err := s.repos.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if errors.Is(err, api.ErrNotFound) || (err == nil && head != tip) {
		return p.finish(nil)
	}
	if err != nil {
		return p.finish(err)
	}
	p.remember(r.RepoRef, &tipView{sha: head})
	return p.finish(p.repo(ctx, r))
}

// finishPartial ends a full pass. A rate limit is not an error there: the next hourly pass starts elsewhere
// and continues the work.
func (p *pass) finishPartial(err error) (Summary, error) {
	if errors.Is(err, api.ErrRateLimited) {
		p.sum.RateLimited = true
		return p.finish(nil)
	}
	return p.finish(err)
}

func (p *pass) finish(err error) (Summary, error) {
	if errors.Is(err, api.ErrRateLimited) {
		p.sum.RateLimited = true
	}
	slog.Info("sweep: pass finished",
		"repos", p.sum.Repos, "pulls", p.sum.Pulls, "pended", p.sum.Pended, "timed_out", p.sum.TimedOut,
		"rerun", p.sum.Rerun, "warned", p.sum.Warned, "suspended", p.sum.Suspended, "not_admitted", p.sum.NotAdmitted,
		"errors", p.sum.Errors,
		"rate_limited", p.sum.RateLimited, "cancelled", err != nil && !p.sum.RateLimited)
	return p.sum, err
}

func (p *pass) installation(ctx context.Context, inst api.Installation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	admitted, err := p.admit.Admitted(ctx, inst.ID, inst.Account)
	if err != nil {
		return p.skip(ctx, err, "installation", "installation_id", inst.ID)
	}
	if !admitted {
		p.sum.NotAdmitted++
		return nil
	}
	repos, err := p.repos.ListRepos(ctx, inst.ID)
	if err != nil {
		return p.skip(ctx, err, "installation", "installation_id", inst.ID)
	}
	for _, r := range repos {
		if err := p.repo(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (p *pass) repo(ctx context.Context, r api.Repo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.sum.Repos++
	pulls, err := p.pulls.OpenPulls(ctx, r.RepoRef)
	if err != nil {
		return p.skip(ctx, err, "repository", "installation_id", r.InstallationID, "repo_id", r.ID)
	}
	notOnboarded := map[string]bool{} // base refs of this repository without a settings file
	for _, pl := range pulls {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Only the default branch is decided against; the upload refuses any other base, so a receipt there would
		// never match the inputs read at that base and the pass would re-run it forever.
		// An unknown default branch decides nothing: no pull equals it.
		if notOnboarded[pl.BaseRef] || pl.BaseRef != r.DefaultBranch {
			continue
		}
		t := inputs.Target{
			Repo: r, PullRequest: pl.Number, HeadSHA: pl.HeadSHA, BaseRef: pl.BaseRef,
			Fork: pl.HeadRepo != r.FullName,
		}
		if err := p.pull(ctx, t, SharedWith(pulls, t.PullRequest, t.HeadSHA, r.DefaultBranch), notOnboarded); err != nil {
			if err := p.skip(ctx, err, "pull", targetAttrs(t)...); err != nil {
				return err
			}
		}
	}
	return nil
}

// pull returns every error; the caller decides whether it stops the pass.
func (p *pass) pull(ctx context.Context, t inputs.Target, shared []verdict.SharedPull, notOnboarded map[string]bool) error {
	tr, err := p.resolver.Resolve(ctx, t, p.now)
	if err != nil {
		return err
	}
	if !tr.SettingsFound {
		notOnboarded[t.BaseRef] = true
		return nil
	}
	p.sum.Pulls++
	if p.recheck {
		return p.recheckPull(ctx, t, tr)
	}
	// Rule 8 is independent of the check rules: a failure of one never skips the other.
	return errors.Join(p.evaluate(ctx, t, tr, shared), p.decisions.Revalidate(ctx, t, tr, p.now))
}

// skip counts and logs a failed unit. It returns an error only when the failure stops the pass.
// The error text is not logged: GitHub errors quote repository names and paths.
func (p *pass) skip(ctx context.Context, err error, unit string, ids ...any) error {
	if errors.Is(err, api.ErrRateLimited) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p.sum.Errors++
	slog.Warn("sweep: unit skipped", append([]any{"unit", unit}, ids...)...)
	return nil
}

func targetAttrs(t inputs.Target) []any {
	return []any{
		"installation_id", t.Repo.InstallationID, "repo_id", t.Repo.ID,
		"pr", t.PullRequest, "head_sha", t.HeadSHA,
	}
}
