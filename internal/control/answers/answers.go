// Package answers is the owner's side of an answer to a request (design 9.2, 10.4; M2 task 19): an answer that loosens
// the policy is made with the owner's passkey, then the gate records it and carries it to the default branch. The
// passkey belongs to control, the records and the policy pull request to the gate; this package joins the two.
package answers

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// Answer is an owner's answer as the request page sends it.
type Answer struct {
	Kind    string // approve, approve-until or reject
	Until   string // approve-until: YYYY-MM-DD
	Instead string // reject: what to do instead
	Later   bool   // merge later: the policy pull request stays open and later answers join it
	Token   string // the passkey step-up token, for an answer that loosens the policy
}

// Answers takes in owners' answers.
type Answers interface {
	// Digest is the change digest of answer a to request number: the passkey step-up of the answer is bound to it,
	// with the answer kind as its action and the request number.
	Digest(ctx context.Context, r api.Repo, number int, a Answer) (string, error)
	// Answer records answer a of user u to request number and carries it to the default branch. An error means no
	// record changed; a token Redeem already used stays used.
	Answer(ctx context.Context, r api.Repo, u passkey.User, number int, a Answer) (gate.AnswerOutcome, error)
}

type service struct {
	gate     gate.Answers
	passkeys passkey.Passkeys
}

// New returns the Answers of the gate's answers and the users' passkeys.
func New(g gate.Answers, p passkey.Passkeys) Answers { return &service{gate: g, passkeys: p} }

func (s *service) Digest(ctx context.Context, r api.Repo, number int, a Answer) (string, error) {
	return s.gate.AnswerDigest(ctx, gate.Target{Repo: r}, number, requestAnswer(a))
}

// Answer hands the gate a Redeem that consumes the owner's token for this request, this answer kind and the change
// digest the gate computes. The gate calls it only for an answer that loosens the policy, after every other check, so a
// refused answer leaves the token unused.
func (s *service) Answer(ctx context.Context, r api.Repo, u passkey.User, number int, a Answer) (gate.AnswerOutcome, error) {
	in := gate.AnswerInput{Request: number, Answer: requestAnswer(a), Owner: gate.AnswerOwner{ID: u.ID, Login: u.Login},
		Later: a.Later,
		Redeem: func(ctx context.Context, digest string) (string, error) {
			return s.passkeys.Redeem(ctx, r, u.ID, a.Token, passkey.Purpose{Action: a.Kind, Request: number, Digest: digest})
		}}
	return s.gate.Answer(ctx, gate.Target{Repo: r}, in)
}

func requestAnswer(a Answer) gate.RequestAnswer {
	return gate.RequestAnswer{Kind: policy3.Answer(a.Kind), Until: a.Until, Instead: a.Instead}
}
