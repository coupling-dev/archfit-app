package gate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

// policyPushed judges a default-branch push for the policy it brings (design 7.1, 9.2; M2 task 20, gate/approvals):
// the merge of a proposal with the policy its approvals wrote consumes them, once; in a repository with an approved
// policy, any other change of the policy puts it outside the flow, where every upload is unknown (upload4.go) and one
// issue is open to the owners. A repository without an approved policy is not under change control yet: the push is
// recorded, and its uploads say first_approval. A consume hands the pull requests waiting on its requests to the
// re-check after an answer (sweep.AfterAnswer). A failed read or write is returned, so the delivery is retried; a
// retry finds the judgement and only finishes it.
func (g *Gate) policyPushed(ctx context.Context, e webhook.Event) error {
	t := eventTarget(e)
	out, err := g.approvals.Pushed(ctx, approvals.Push{Repo: t.Repo, Before: e.Before, After: e.HeadSHA, ChangedPaths: e.ChangedPaths})
	if err != nil {
		return fmt.Errorf("gate: judge the policy push: %w", err)
	}
	if out.Judged == approvals.JudgedNone {
		return nil
	}
	log := slog.Default()
	log.InfoContext(ctx, "gate policy push", "delivery_id", e.DeliveryID, "installation_id", e.InstallationID,
		"head_sha", e.HeadSHA, "judged", out.Judged, "proposal", out.Proposal, "issue", out.Issue, "voided", out.Voided)
	if out.Judged != approvals.JudgedConsumed || len(out.Waiting) == 0 {
		return nil
	}
	// The pull requests waiting on the consumed requests get a fresh result without their authors (design 10.5). A
	// retried delivery finds the judgement again, and the re-check skips the pull requests it already recorded.
	consumed := sweep.Consumed{Proposal: out.Proposal, Requests: out.Requests, Pulls: out.Waiting}
	if err := sweep.AfterAnswer(ctx, g.recheckGH, g.store, t.Repo, consumed, g.now()); err != nil {
		return fmt.Errorf("gate: re-check after the answer: %w", err)
	}
	return nil
}
