package gate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

// Reactions, as logged.
const (
	reactionNone        = "none"
	reactionNotAdmitted = "not_admitted"
	reactionOtherBase   = "other_base"
	reactionPullClosed  = "pull_closed"
)

// HandleEvent reacts to authenticated request, pull and policy/workflow events. No event itself grants approval.
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
		if err := g.retireForeignProposal(ctx, eventTarget(e)); err != nil {
			return reactionNone, err
		}
		return reactionNone, nil
	case webhook.PullClosed:
		return reactionPullClosed, g.pullClosed(ctx, e)
	case webhook.PullCommented:
		if asks.IsRequest(e.Body) {
			return g.requestCommented(ctx, e)
		}
		return reactionNone, nil
	case webhook.IssueOpened:
		return g.requestFiled(ctx, e)
	case webhook.DefaultBranchPush:
		return g.branchPushed(ctx, e)
	}
	return reactionNone, nil
}

// branchPushed judges policy and workflow approvals; the hourly pass reconciles v4 results.
func (g *Gate) branchPushed(ctx context.Context, e webhook.Event) (string, error) {
	// Consume or mark a policy change outside the flow before judging the workflow.
	if err := g.policyPushed(ctx, e); err != nil {
		return reactionNone, err
	}
	if err := g.workflowPushed(ctx, e); err != nil {
		return reactionNone, err
	}
	return reactionNone, nil
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
			DefaultBranch: e.DefaultBranch,
		},
		PullRequest: e.PullRequest,
		HeadSHA:     e.HeadSHA,
		BaseRef:     e.BaseRef,
		Fork:        e.HeadRepo != e.Repo, // a deleted fork has HeadRepo "" and still reads as a fork
	}
}

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error) {
	return g.sweep4(ctx, Summary{})
}
