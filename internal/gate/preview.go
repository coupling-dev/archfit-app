package gate

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
)

// RequestViews is what the App's request page reads (design 10.4, 12.3; M2 task 17): a request with its record,
// balance, two modules and earlier answers, and the preview of each answer. It writes nothing. *Gate implements it.
type RequestViews interface {
	RequestView(ctx context.Context, t Target, number int) (RequestView, error)
	RequestPreview(ctx context.Context, t Target, number int, a RequestAnswer) (RequestPreview, error)
}

// The request page's types, owned by gate/requests.
type (
	RequestView       = asks.View
	RequestModule     = asks.Module
	RequestEarlier    = asks.Earlier
	RequestPreview    = asks.Preview
	RequestPullEffect = asks.PullEffect
	RequestAnswer     = asks.Answer
	RequestNumbers    = records.Numbers // verified, or "as reported" by the CLI
	RequestBalance    = records.Balance
	// AnswerChange is the policy edit of one answer, kept by an approval to be written again on a new tip.
	AnswerChange = asks.Change
)

// The kinds of a RequestEarlier.
const (
	EarlierDeny      = asks.EarlierDeny
	EarlierException = asks.EarlierException
)

// The refusals of the request page.
var (
	ErrNoRequest     = asks.ErrNoRequest
	ErrClosedRequest = asks.ErrClosedRequest
	ErrAnswer        = asks.ErrAnswer
	ErrNoPolicy      = asks.ErrNoPolicy
	ErrByHand        = asks.ErrByHand
)

// RequestView reads request number of t's repository.
func (g *Gate) RequestView(ctx context.Context, t Target, number int) (RequestView, error) {
	if err := g.admitted(ctx, t); err != nil {
		return RequestView{}, err
	}
	return g.asks.View(ctx, t.Repo, number)
}

// RequestPreview is what answer a to request number would write and change.
func (g *Gate) RequestPreview(ctx context.Context, t Target, number int, a RequestAnswer) (RequestPreview, error) {
	if err := g.admitted(ctx, t); err != nil {
		return RequestPreview{}, err
	}
	return g.asks.Preview(ctx, t.Repo, number, a)
}

// PolicyEdit writes an answer's change into a policy file, the first line of the entry it writes ending with
// "# comment" when comment is set: the edit the preview shows is the edit the answer writes.
func PolicyEdit(file []byte, c AnswerChange, comment string) ([]byte, error) {
	return asks.Edit(file, c, comment)
}
