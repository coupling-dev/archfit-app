package gate

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/gate/decisions"
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
)

// ScreenInput, ScreenReason and ScreenLine are owned by gate/feedback: the pull request page words a published
// result with the code that words the comment.
type (
	ScreenInput  = feedback.ScreenInput
	ScreenReason = feedback.ScreenReason
	ScreenLine   = feedback.Line
)

// FirstScreen is the comment's first screen for a published result, as lines of plain text; nil when it would show
// no report.
func FirstScreen(in ScreenInput) []ScreenLine { return feedback.FirstScreen(in) }

// RecoveryStep is the next step of a recorded reason, worded for its role as the comment words it.
func RecoveryStep(code, next string) string { return feedback.RecoveryStep(code, next) }

// DecisionQueue is what the App's pages read of the decision requests of unattended agents: the requests that wait for
// an owner. It grants and changes nothing. *Gate implements it.
type DecisionQueue interface {
	PendingDecisions(ctx context.Context, t Target, limit int) (PendingDecisions, error)
}

// PendingDecisions is owned by gate/decisions.
type PendingDecisions = decisions.Queue

// PendingDecisions reads the repository's pending decision requests: their number and the records of the first limit.
// t names the repository.
func (g *Gate) PendingDecisions(ctx context.Context, t Target, limit int) (PendingDecisions, error) {
	if err := g.admitted(ctx, t); err != nil {
		return PendingDecisions{}, err
	}
	return g.requests.PendingQueue(ctx, t, limit)
}
