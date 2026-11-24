package gate

import (
	"context"
	"log/slog"

	"github.com/coupling-dev/archfit-app/internal/gate/ingest"
	asks "github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
)

// requestCommented takes in the request line of a pull-request comment (M2 task 16): it opens or joins request issues
// and sets the pull request's check to waiting when open requests cover every error.
func (g *Gate) requestCommented(ctx context.Context, e webhook.Event) (string, error) {
	return g.asks.Comment(ctx, asks.Comment{Repo: eventTarget(e).Repo, Pull: e.PullRequest, Author: e.Author,
		Body: e.Body, Today: g.now().UTC(), CreatedAt: e.CreatedAt})
}

// requestFiled takes in an issue the CLI opened from a `can` answer before a pull request exists.
func (g *Gate) requestFiled(ctx context.Context, e webhook.Event) (string, error) {
	return g.asks.Filed(ctx, asks.Issue{Repo: eventTarget(e).Repo, Number: e.Issue, Author: e.Author, Body: e.Body,
		Today: g.now().UTC(), CreatedAt: e.CreatedAt})
}

// pullClosed drops a closed pull request from the pull index of Archfit 4.
func (g *Gate) pullClosed(ctx context.Context, e webhook.Event) error {
	return asks.PullClosed(ctx, g.v4.store, eventTarget(e).Repo, e.PullRequest)
}

// releaseHeld files the request line held for the upload's pull request, now that the head has a result
// (gate/requests' Release). It never changes the upload's answer: a failure is logged with identifiers only, and the
// line stays held for the next upload of the head. Stated limit: no other trigger retries it.
func (g *Gate) releaseHeld(ctx context.Context, u ingest.Upload4) {
	b := u.Binding
	reaction, err := g.asks.Release(ctx, b.Repo, b.PullRequest)
	if err != nil {
		slog.Default().WarnContext(ctx, "gate: file the held request", "installation_id", b.Repo.InstallationID,
			"repository", b.Repo.FullName, "pr", b.PullRequest, "error", err.Error())
		return
	}
	if reaction != asks.ReactionNone {
		slog.Default().InfoContext(ctx, "gate: held request", "installation_id", b.Repo.InstallationID,
			"repository", b.Repo.FullName, "pr", b.PullRequest, "reaction", reaction)
	}
}
