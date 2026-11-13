package gate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alexei-led/archfit-app/internal/gate/drift"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

// driftPushed hands the repository block of a push run to the drift issue (design 7.1, M2 task 22, gate/drift). Only
// a push to the default branch (the event as GitHub states it) that the App accepted as it is, whose head is the tip
// every rule read, counts: a late upload of an older push, a pull request, a merge group, and a result with the App's
// own records change nothing. It runs after the check is published, which read the tip again inside its lease. A
// failed read or write is returned, so the Action retries the upload; the retry publishes the same check and finishes
// the issue.
func (g *Gate) driftPushed(ctx context.Context, u ingest.Upload4, d decision4) error {
	b := u.Binding
	if b.Event != envelope.EventPush || b.PullRequest != 0 || len(d.Reasons) > 0 || u.Result == nil || b.HeadSHA != d.Tip {
		return nil
	}
	out, err := g.drift.Pushed(ctx, drift.Run{Repo: b.Repo, Head: b.HeadSHA, State: u.Result.Repository})
	if err != nil {
		return fmt.Errorf("gate: drift issue: %w", err)
	}
	if out != drift.None {
		slog.Default().InfoContext(ctx, "gate drift issue", "installation_id", b.Repo.InstallationID,
			"repository_id", b.Repo.ID, "head_sha", b.HeadSHA, "drift", out)
	}
	return nil
}
