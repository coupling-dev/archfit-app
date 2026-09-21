package gate

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// Reactions, as logged.
const (
	reactionNone         = "none"
	reactionPending      = "pending"
	reactionForkRefused  = "fork_refused"
	reactionRerun        = "rerun"
	reactionRepoSweep    = "repo_sweep"
	reactionNotOnboarded = "not_onboarded"
)

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// re-run on a review change when the last receipt was owner_approval_missing, and a one-repository
// sweep when a default-branch push changes a protected path.
func (g *Gate) HandleEvent(ctx context.Context, e webhook.Event) error {
	reaction, err := g.react(ctx, e)
	slog.Default().InfoContext(ctx, "gate event",
		"kind", string(e.Kind), "delivery_id", e.DeliveryID, "installation_id", e.InstallationID,
		"pr", e.PullRequest, "head_sha", e.HeadSHA, "reaction", reaction, "failed", err != nil)
	return err
}

func (g *Gate) react(ctx context.Context, e webhook.Event) (string, error) {
	switch e.Kind {
	case webhook.PullOpened, webhook.PullSynchronized:
		return g.newHead(ctx, eventTarget(e))
	case webhook.ReviewChanged:
		return g.reviewChanged(ctx, eventTarget(e))
	case webhook.DefaultBranchPush:
		if !protectedPush(e.ChangedPaths) {
			return reactionNone, nil
		}
		// A rate-limited pass is a nil error: the hourly sweep continues it.
		if _, err := g.sweeper.RunRepo(ctx, eventTarget(e).Repo, g.now()); err != nil {
			return reactionRepoSweep, fmt.Errorf("gate: repository sweep: %w", err)
		}
		return reactionRepoSweep, nil
	}
	return reactionNone, nil
}

// eventTarget builds the target from the signed payload. ID and DefaultBranch stay zero:
// pull request targets and RunRepo do not need them, and that saves one API call.
func eventTarget(e webhook.Event) inputs.Target {
	return inputs.Target{
		Repo: api.Repo{
			RepoRef: api.RepoRef{InstallationID: e.InstallationID, FullName: e.Repo},
			Private: e.RepoPrivate,
		},
		PullRequest: e.PullRequest,
		HeadSHA:     e.HeadSHA,
		BaseRef:     e.BaseRef,
		Fork:        e.HeadRepo != e.Repo, // a deleted fork has HeadRepo "" and still reads as a fork
	}
}

func (g *Gate) newHead(ctx context.Context, t inputs.Target) (string, error) {
	trusted, err := g.resolver.Resolve(ctx, t, g.now())
	if err != nil {
		return reactionNone, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	switch {
	case !trusted.SettingsFound:
		return reactionNotOnboarded, nil
	case t.Fork:
		// The zero receipt: no workflow run reported for this head, so any later upload replaces it.
		if err := g.publisher.Publish(ctx, sweep.Without(verdict.ReasonForkUnsupported, t, trusted, feedback.Receipt{})); err != nil {
			return reactionForkRefused, fmt.Errorf("gate: publish fork refusal: %w", err)
		}
		return reactionForkRefused, nil
	case trusted.Settings.Gate == settings.GateRequired:
		if err := g.publisher.Pending(ctx, t); err != nil {
			return reactionPending, fmt.Errorf("gate: pending check: %w", err)
		}
		return reactionPending, nil
	}
	return reactionNone, nil
}

// reviewChanged re-runs the workflow once an owner's review arrives after owner_approval_missing.
// The check stays as it is until the new upload; nothing here can turn it green.
func (g *Gate) reviewChanged(ctx context.Context, t inputs.Target) (string, error) {
	state, err := g.publisher.State(ctx, t)
	if err != nil {
		return reactionNone, fmt.Errorf("gate: check state: %w", err)
	}
	if state.Receipt.Reason != verdict.ReasonOwnerApprovalMissing {
		return reactionNone, nil
	}
	trusted, err := g.resolver.Resolve(ctx, t, g.now())
	if err != nil {
		return reactionNone, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	if trusted.Approval != inputs.ApprovalGranted {
		return reactionNone, nil // no re-run for a review that grants nothing
	}

	// The sweep owns the guard. A receipt without a run and a run that is gone are skipped, not errors.
	res, err := sweep.Rerun(ctx, g.actions, t.Repo.RepoRef, state.Receipt)
	if err != nil {
		return reactionNone, fmt.Errorf("gate: re-run: %w", err)
	}
	if res != sweep.RerunRequested {
		return reactionNone, nil
	}
	return reactionRerun, nil
}

// protectedPush reports whether a default-branch push may have changed a trusted input.
// No paths means "unknown", which fails towards the repository pass: the pass is idempotent.
func protectedPush(paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	return slices.ContainsFunc(paths, inputs.MayBeProtected)
}

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error) {
	return g.sweeper.Run(ctx, g.now())
}
