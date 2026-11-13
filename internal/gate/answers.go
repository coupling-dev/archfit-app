package gate

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
)

// Answers takes in an owner's answer to a request (design 9.2, 10.4; M2 task 19): approve, approve until a date, or
// reject with instead. It records the approval, carries it to the default branch in the App's policy pull request on
// archfit/policy with the other open answers, sets archfit/gate on that pull request when M2 D13 holds, and merges it
// unless the owner chose to merge later. *Gate implements it.
type Answers interface {
	// AnswerDigest is the change digest of answer a to request number: the owner's passkey step-up is bound to it.
	AnswerDigest(ctx context.Context, t Target, number int, a RequestAnswer) (string, error)
	// Answer records and carries in. An error means the answer was not recorded; what the answer found on its way (a
	// policy pull request closed without a merge, an approval that no longer applies) may have been recorded void.
	Answer(ctx context.Context, t Target, in AnswerInput) (AnswerOutcome, error)
}

// The answer types, owned by gate/proposals.
type (
	AnswerInput   = proposals.Input
	AnswerOwner   = proposals.Owner
	AnswerOutcome = proposals.Outcome
)

// The states of the policy pull request in an AnswerOutcome, owned by gate/requests.
const (
	AnswerMerged  = asks.PullMerged
	AnswerLater   = asks.PullLater
	AnswerRefused = asks.PullRefused
)

// The refusals of an answer, besides the request page's (ErrNoRequest, ErrClosedRequest, ErrAnswer, ErrNoPolicy,
// ErrByHand) and the passkey's, which Redeem returns as they are. ErrOutsideFlow: the policy changed outside the flow,
// and an owner confirms or reverts it first (M2 task 23).
var (
	ErrNotPolicyOwner = proposals.ErrNotOwner
	ErrNeedsPasskey   = proposals.ErrNeedsPasskey
	ErrAnswerBusy     = proposals.ErrBusy
	ErrProposalFull   = proposals.ErrFull
	ErrOutsideFlow    = proposals.ErrOutsideFlow
)

// AnswerDigest is the change digest of answer a to request number of t's repository.
func (g *Gate) AnswerDigest(ctx context.Context, t Target, number int, a RequestAnswer) (string, error) {
	if err := g.admitted(ctx, t); err != nil {
		return "", err
	}
	return g.proposals.Digest(ctx, t.Repo, number, a)
}

// Answer records an owner's answer to a request of t's repository and carries it to the default branch.
func (g *Gate) Answer(ctx context.Context, t Target, in AnswerInput) (AnswerOutcome, error) {
	if err := g.admitted(ctx, t); err != nil {
		return AnswerOutcome{}, err
	}
	return g.proposals.Answer(ctx, t.Repo, in)
}
