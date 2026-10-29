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

// pusherTypeUser is the pusher_type of a push made by a user account.
const pusherTypeUser = "user"

// Reactions, as logged.
const (
	reactionNone         = "none"
	reactionPending      = "pending"
	reactionForkRefused  = "fork_refused"
	reactionRerun        = "rerun"
	reactionRepoSweep    = "repo_sweep"
	reactionNotOnboarded = "not_onboarded"
	reactionNotAdmitted  = "not_admitted" // no operator approved the installation: nothing is read or written
	reactionOtherBase    = "other_base"   // a pull request into a branch other than the default branch: nothing is read or written
	// reactionApprovalWithdrawn is owner_approval_missing published over a receipt that asked for no action.
	reactionApprovalWithdrawn = "approval_withdrawn"
	reactionDecisionStale     = "decision_stale"   // pending decision requests went stale with a new head or a closed pull request
	reactionDecisionIgnored   = "decision_ignored" // a request on a pull request the gate does not decide: nothing is recorded or written
)

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// owner_approval_missing on a review change that withdraws the approval a receipt relied on, re-run on a
// review change when the last receipt was owner_approval_missing, a pusher record (attribution only, never an
// approval) and a re-evaluation of the pull request on a pushed head, and a one-repository sweep when a
// default-branch push changes a protected path.
func (g *Gate) HandleEvent(ctx context.Context, e webhook.Event) error {
	reaction, err := g.react(ctx, e)
	slog.Default().InfoContext(ctx, "gate event",
		"kind", string(e.Kind), "delivery_id", e.DeliveryID, "installation_id", e.InstallationID,
		"pr", e.PullRequest, "head_sha", e.HeadSHA, "reaction", reaction, "failed", err != nil)
	return err
}

func (g *Gate) react(ctx context.Context, e webhook.Event) (string, error) {
	if e.Repo != "" {
		// An unreadable admission is an error, so the delivery is retried rather than dropped.
		admitted, err := g.admissions.Admitted(ctx, e.InstallationID, inputs.Owner(e.Repo))
		if err != nil {
			return reactionNotAdmitted, fmt.Errorf("gate: read admission: %w", err)
		}
		if !admitted {
			return reactionNotAdmitted, nil
		}
	}
	switch e.Kind {
	case webhook.PullOpened, webhook.PullSynchronized:
		if otherBase(e.DefaultBranch, e.BaseRef) {
			return reactionOtherBase, nil
		}
		reaction, err := g.newHead(ctx, eventTarget(e))
		if e.Kind == webhook.PullSynchronized {
			err = errors.Join(err, g.requests.HeadMoved(ctx, eventTarget(e), g.now()))
		}
		return reaction, err
	case webhook.PullClosed:
		if err := g.requests.Closed(ctx, eventTarget(e), g.now()); err != nil {
			return reactionDecisionStale, fmt.Errorf("gate: close decision requests: %w", err)
		}
		return reactionDecisionStale, nil
	case webhook.PullCommented:
		return g.commented(ctx, e)
	case webhook.ReviewChanged:
		if otherBase(e.DefaultBranch, e.BaseRef) {
			return reactionOtherBase, nil
		}
		return g.approvalChanged(ctx, eventTarget(e))
	case webhook.BranchPush, webhook.DefaultBranchPush:
		return g.branchPushed(ctx, e)
	}
	return reactionNone, nil
}

// branchPushed records who moved the branch to e.HeadSHA, then re-evaluates the pull request on that head as a review
// event does. The record approves nothing: a local coding agent pushes with its owner's credentials. A default-branch
// push that changes a protected path also runs the repository sweep.
func (g *Gate) branchPushed(ctx context.Context, e webhook.Event) (string, error) {
	t := eventTarget(e)
	if err := g.recordPusher(ctx, t, e); err != nil {
		return reactionNone, err
	}
	reaction, err := g.pullOnHead(ctx, t, e.Branch, e.DefaultBranch)
	if err != nil || e.Kind == webhook.BranchPush || !protectedPush(e.ChangedPaths) {
		return reaction, err
	}
	// A rate limit is an error that wraps api.ErrRateLimited: the queue retries the delivery.
	if _, err := g.sweeper.RunRepo(ctx, t.Repo, g.now()); err != nil {
		return reactionRepoSweep, fmt.Errorf("gate: repository sweep: %w", err)
	}
	return reactionRepoSweep, nil
}

// recordPusher records e.Pusher as a pusher of the head, for attribution. A push whose pusher_type is present and not
// "user" (a deploy key) records nothing: GitHub does not say whose login sender carries then. An absent pusher_type
// still trusts sender.
func (g *Gate) recordPusher(ctx context.Context, t inputs.Target, e webhook.Event) error {
	if e.PusherType != "" && e.PusherType != pusherTypeUser {
		slog.Default().InfoContext(ctx, "gate pusher not recorded",
			"delivery_id", e.DeliveryID, "installation_id", e.InstallationID, "head_sha", e.HeadSHA)
		return nil
	}
	if err := g.resolver.RecordPusher(ctx, t, e.Pusher); err != nil {
		return fmt.Errorf("gate: record pusher: %w", err)
	}
	return nil
}

// pullOnHead re-evaluates the newest pull request whose head is branch, when that head is the commit just pushed.
// A pull request from a fork never has a head in this repository, so it is never found here.
func (g *Gate) pullOnHead(ctx context.Context, t inputs.Target, branch, defaultBranch string) (string, error) {
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
	if otherBase(defaultBranch, pr.BaseRef) {
		return reactionOtherBase, nil
	}
	t.PullRequest, t.BaseRef = pr.Number, pr.BaseRef
	return g.approvalChanged(ctx, t)
}

// otherBase reports a pull request into a branch other than the default branch. The App decides only against the
// default branch (the upload does not accept another base), so nothing is read or written for such a pull request. An
// event whose payload names no default branch is not skipped: no event ever publishes a success.
func otherBase(defaultBranch, base string) bool {
	return defaultBranch != "" && base != defaultBranch
}

// eventTarget builds the target from the signed payload. ID stays zero: pull request targets do not need it, and
// that saves one API call. The default branch comes from the payload: the repository sweep a push starts decides
// only the pull requests into it.
func eventTarget(e webhook.Event) inputs.Target {
	return inputs.Target{
		Repo: api.Repo{
			RepoRef:       api.RepoRef{InstallationID: e.InstallationID, FullName: e.Repo},
			Private:       e.RepoPrivate,
			DefaultBranch: e.DefaultBranch, // the repository sweep a push starts decides only pulls into it
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

// approvalChanged reacts to an event that may withdraw or grant an owner approval: a review, an approval recorded in
// the App (Reevaluate), or a push of t's head. It reads the gate check, decides and writes inside the head's
// publication lease (Publisher.Reconcile), so no other publication for the head lands in between. A receipt that asks for no action
// while the approval it needs is gone becomes owner_approval_missing at once, as sweep rule 5a does it. After
// owner_approval_missing, an event once no approval is missing re-runs the workflow; the check stays as it is until
// the new upload, so nothing here can turn it green.
func (g *Gate) approvalChanged(ctx context.Context, t inputs.Target) (string, error) {
	reaction := reactionNone
	err := g.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		rc := current.Receipt
		if !sweep.Decided(rc) && rc.Reason != verdict.ReasonOwnerApprovalMissing {
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
