package gate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

// policyPushed judges a default-branch push for the policy it brings (design 7.1, 9.2; M2 task 20, gate/approvals):
// the merge of a proposal with the policy its approvals wrote consumes them, once; in a repository with an approved
// policy, any other change of the policy puts it outside the flow, where every upload is unknown (upload4.go) and one
// issue is open to the owners. A repository without an approved policy is not under change control yet: the push is
// recorded, and its uploads say first_approval. A failed read or write is returned, so the delivery is retried; a
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
	if out.Judged == approvals.JudgedConsumed {
		// The pull requests waiting on the consumed requests get a fresh result through the re-check after an answer
		// (M2 task 21); until it lands, the hand-off is this line.
		log.InfoContext(ctx, "gate recheck handed", "delivery_id", e.DeliveryID, "installation_id", e.InstallationID,
			"head_sha", e.HeadSHA, "proposal", out.Proposal, "requests", out.Requests, "pulls", out.Waiting)
	}
	return nil
}
