package gate

import (
	"context"
	"errors"
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
	// reactionApprovalWithdrawn is owner_approval_missing published over a receipt that asked for no action.
	reactionApprovalWithdrawn = "approval_withdrawn"
)

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// owner_approval_missing on a review change that withdraws the approval a receipt relied on, re-run on a
// review change when the last receipt was owner_approval_missing, a pusher record and a re-evaluation of the
// pull request on a pushed head, and a one-repository sweep when a default-branch push changes a protected path.
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
		return g.approvalChanged(ctx, eventTarget(e))
	case webhook.BranchPush, webhook.DefaultBranchPush:
		return g.branchPushed(ctx, e)
	}
	return reactionNone, nil
}

// branchPushed records who moved the branch to e.HeadSHA, then re-evaluates the pull request on that head: a report
// may have asked for an owner approval before the record existed. A default-branch push that changes a protected
// path also runs the repository sweep.
func (g *Gate) branchPushed(ctx context.Context, e webhook.Event) (string, error) {
	t := eventTarget(e)
	if err := g.resolver.RecordPusher(ctx, t, e.Pusher); err != nil {
		return reactionNone, fmt.Errorf("gate: record pusher: %w", err)
	}
	reaction, err := g.pullOnHead(ctx, t, e.Branch)
	if err != nil || e.Kind == webhook.BranchPush || !protectedPush(e.ChangedPaths) {
		return reaction, err
	}
	// A rate limit is an error that wraps api.ErrRateLimited: the queue retries the delivery.
	if _, err := g.sweeper.RunRepo(ctx, t.Repo, g.now()); err != nil {
		return reactionRepoSweep, fmt.Errorf("gate: repository sweep: %w", err)
	}
	return reactionRepoSweep, nil
}

// pullOnHead re-evaluates the newest pull request whose head is branch, when that head is the commit just pushed.
// A pull request from a fork never has a head in this repository, so it is never found here.
func (g *Gate) pullOnHead(ctx context.Context, t inputs.Target, branch string) (string, error) {
	pr, err := g.pulls.PullByHead(ctx, t.Repo.RepoRef, branch)
	if errors.Is(err, api.ErrNotFound) {
		return reactionNone, nil
	}
	if err != nil {
		return reactionNone, fmt.Errorf("gate: pull request by head: %w", err)
	}
	if pr.State != "open" || pr.HeadSHA != t.HeadSHA {
		return reactionNone, nil
	}
	t.PullRequest, t.BaseRef = pr.Number, pr.BaseRef
	return g.approvalChanged(ctx, t)
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

// approvalChanged reacts to an event that may withdraw or grant an owner approval: a review, or a push that records
// an owner as the pusher of t's head. It reads the gate check, decides and writes inside the head's publication lease
// (Publisher.Reconcile), so no other publication for the head lands in between. A receipt that asks for no action
// while the approval it needs is gone becomes owner_approval_missing at once, as sweep rule 5a does it. After
// owner_approval_missing, an event once no approval is missing re-runs the workflow; the check stays as it is until
// the new upload, so nothing here can turn it green.
func (g *Gate) approvalChanged(ctx context.Context, t inputs.Target) (string, error) {
	reaction := reactionNone
	err := g.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		rc := current.Receipt
		decided := rc.Conclusion == verdict.Success || rc.Conclusion == verdict.Failure
		if !decided && rc.Reason != verdict.ReasonOwnerApprovalMissing {
			return nil, nil // the check already asks for action, or there is none yet
		}
		trusted, err := g.resolver.Resolve(ctx, t, g.now())
		if err != nil {
			return nil, fmt.Errorf("gate: resolve inputs: %w", err)
		}
		if pub := sweep.Withdrawal(t, current, trusted); pub != nil {
			reaction = reactionApprovalWithdrawn
			return pub, nil
		}
		if rc.Reason != verdict.ReasonOwnerApprovalMissing || verdict.Unapproved(trusted) {
			return nil, nil // no re-run while an owner approval is still missing
		}
		// The sweep owns the guard. A receipt without a run and a run that is gone are skipped, not errors.
		res, err := sweep.Rerun(ctx, g.actions, t.Repo.RepoRef, rc)
		if err != nil {
			return nil, fmt.Errorf("gate: re-run: %w", err)
		}
		if res == sweep.RerunRequested {
			reaction = reactionRerun
		}
		return nil, nil
	})
	if err != nil {
		return reaction, fmt.Errorf("gate: approval: %w", err)
	}
	return reaction, nil
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
