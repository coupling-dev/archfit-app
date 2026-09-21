package gate

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	// workflowsDir stands in for Settings.Workflow, which lives on the protected ref: wire/settings only
	// accepts a workflow under it, so matching the directory is a superset that needs no file read.
	workflowsDir = ".github/workflows/"

	runCompleted = "completed"
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
		if err := g.publisher.Publish(ctx, forkRefusal(t, trusted)); err != nil {
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

// forkRefusal has a zero run: no workflow run reported for this head, so any later upload replaces it.
func forkRefusal(t inputs.Target, trusted inputs.Trusted) feedback.Publication {
	outcome := verdict.Without(verdict.ReasonForkUnsupported)
	return feedback.Publication{
		Target:  t,
		Outcome: outcome,
		Receipt: feedback.Receipt{
			PolicyDigest:         acceptedDigest(trusted.PolicyDigests, ""),
			BaselineDigest:       acceptedDigest(trusted.BaselineDigests, ""),
			LabelsDigest:         acceptedDigest(trusted.LabelsDigests, ""),
			EarliestWaiverExpiry: trusted.EarliestWaiverExpiry,
			Conclusion:           outcome.Conclusion,
			Reason:               verdict.ReasonForkUnsupported,
		},
		Trusted: trusted,
	}
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

	// The guard of the sweep: GitHub advances the attempt on every accepted re-run, so a receipt's
	// (run, attempt) can be re-run only while it is still the latest and completed.
	rc := state.Receipt
	if rc.RunID == 0 {
		return reactionNone, nil
	}
	run, err := g.actions.Run(ctx, t.Repo.RepoRef, rc.RunID)
	if err != nil {
		return reactionNone, fmt.Errorf("gate: read run: %w", err)
	}
	if run.Status != runCompleted || run.Attempt != rc.RunAttempt {
		return reactionNone, nil
	}
	if err := g.actions.Rerun(ctx, t.Repo.RepoRef, rc.RunID); err != nil {
		return reactionNone, fmt.Errorf("gate: re-run: %w", err)
	}
	return reactionRerun, nil
}

// protectedPush reports whether a default-branch push may have changed a trusted input.
// No paths means "unknown", which fails towards the repository pass: the pass is idempotent.
func protectedPush(paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	return slices.ContainsFunc(paths, func(p string) bool {
		switch p {
		case policy.PolicyPath, policy.BaselinePath, policy.LabelsPath, settings.Path:
			return true
		}
		return strings.HasPrefix(p, workflowsDir)
	})
}

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error) {
	return g.sweeper.Run(ctx, g.now())
}
