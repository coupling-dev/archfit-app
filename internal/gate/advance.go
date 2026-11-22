package gate

import (
	"context"
	"log/slog"

	"github.com/coupling-dev/archfit-app/internal/gate/proposals"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
)

// proposalClosed voids the App's policy proposal at once when its pull request closed without a merge, so a reopen
// never merges it on its old approvals. Other pull requests are left alone.
func (g *Gate) proposalClosed(ctx context.Context, e webhook.Event) error {
	if g.proposals == nil {
		return nil
	}
	r := eventTarget(e).Repo
	own, err := proposals.IsProposalPull(ctx, g.v4.store, r, e.PullRequest)
	if err != nil || !own {
		return err
	}
	return g.proposals.Closed(ctx, r, e.PullRequest)
}

// advance moves the App's open policy pull request on: up to date with the default branch, then merged when GitHub
// allows (proposals.Advance). The default-branch push and the sweep call it, so an approved change merges with no
// owner step. A failure is logged and never fails the push or the sweep: the next call tries again.
func (g *Gate) advance(ctx context.Context, r api.Repo) {
	if g.proposals == nil {
		return
	}
	if err := g.proposals.Advance(ctx, r); err != nil {
		slog.Default().WarnContext(ctx, "gate: advance the policy pull request", "installation_id", r.InstallationID,
			"repository", r.FullName, "error", err.Error())
	}
}
