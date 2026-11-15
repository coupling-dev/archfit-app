package gate

import (
	"context"

	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

// requestCommented takes in the request line of a pull-request comment (M2 task 16): it opens or joins request issues
// and sets the pull request's check to waiting when open requests cover every error.
func (g *Gate) requestCommented(ctx context.Context, e webhook.Event) (string, error) {
	return g.asks.Comment(ctx, asks.Comment{Repo: eventTarget(e).Repo, Pull: e.PullRequest, Author: e.Author,
		Body: e.Body, Today: g.now().UTC()})
}

// requestFiled takes in an issue the CLI opened from a `can` answer before a pull request exists.
func (g *Gate) requestFiled(ctx context.Context, e webhook.Event) (string, error) {
	return g.asks.Filed(ctx, asks.Issue{Repo: eventTarget(e).Repo, Number: e.Issue, Author: e.Author, Body: e.Body,
		Today: g.now().UTC()})
}

// pullClosed drops a closed pull request from the pull index of Archfit 4.
func (g *Gate) pullClosed(ctx context.Context, e webhook.Event) error {
	return asks.PullClosed(ctx, g.v4.store, eventTarget(e).Repo, e.PullRequest)
}
