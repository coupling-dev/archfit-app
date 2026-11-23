package answers

import (
	"context"

	"github.com/coupling-dev/archfit-app/internal/control/passkey"
	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// EditAnswer is the whole-file edit answer. Every edit requires a passkey.
type EditAnswer struct {
	YAML, Base, Target, Token string
	Later                     bool
	AsIs                      bool // approve the open proposal's file as it is
}

// Edits validates an edit before prompting, then redeems the existing one-use token.
type Edits interface {
	Editor(context.Context, api.Repo) (gate.EditBase, error)
	ReloadEditor(context.Context, api.Repo, passkey.User) (gate.EditBase, error)
	PreviewEdit(context.Context, api.Repo, passkey.User, EditAnswer) (gate.EditView, error)
	Edit(context.Context, api.Repo, passkey.User, EditAnswer) (gate.AnswerOutcome, error)
	// MakeRequiredPreview and MakeRequired compute the required-gate policy in the gate; the
	// token of MakeRequired binds the digest the App computed, never bytes from the caller.
	MakeRequiredPreview(context.Context, api.Repo, passkey.User) (gate.EditView, error)
	MakeRequired(context.Context, api.Repo, passkey.User, string) (gate.AnswerOutcome, error)
}

func (s *service) Editor(ctx context.Context, r api.Repo) (gate.EditBase, error) {
	return s.gate.Editor(ctx, gate.Target{Repo: r})
}

func (s *service) ReloadEditor(ctx context.Context, r api.Repo, u passkey.User) (gate.EditBase, error) {
	return s.gate.ReloadEditor(ctx, gate.Target{Repo: r}, gate.AnswerOwner{ID: u.ID, Login: u.Login})
}

func editInput(u passkey.User, a EditAnswer) gate.EditInput {
	return gate.EditInput{File: []byte(a.YAML), Base: a.Base, Target: a.Target,
		Owner: gate.AnswerOwner{ID: u.ID, Login: u.Login}, Later: a.Later, AsIs: a.AsIs}
}

func (s *service) PreviewEdit(ctx context.Context, r api.Repo, u passkey.User, a EditAnswer) (gate.EditView, error) {
	return s.gate.EditPreview(ctx, gate.Target{Repo: r}, editInput(u, a))
}

func (s *service) Edit(ctx context.Context, r api.Repo, u passkey.User, a EditAnswer) (gate.AnswerOutcome, error) {
	in := editInput(u, a)
	if a.Token != "" {
		in.Redeem = func(ctx context.Context, digest string) (string, error) {
			return s.passkeys.Redeem(ctx, r, u.ID, a.Token, passkey.Purpose{Action: "edit", Digest: digest, Target: a.Target, BaseDigest: a.Base})
		}
	}
	return s.gate.Edit(ctx, gate.Target{Repo: r}, in)
}

func (s *service) MakeRequiredPreview(ctx context.Context, r api.Repo, u passkey.User) (gate.EditView, error) {
	return s.gate.MakeRequiredPreview(ctx, gate.Target{Repo: r}, gate.AnswerOwner{ID: u.ID, Login: u.Login})
}

// MakeRequired redeems the caller's step-up token for the digest the gate computed; a token made
// for another action, repository or digest stays unused.
func (s *service) MakeRequired(ctx context.Context, r api.Repo, u passkey.User, token string) (gate.AnswerOutcome, error) {
	return s.gate.MakeRequired(ctx, gate.Target{Repo: r}, gate.RequiredInput{
		Owner: gate.AnswerOwner{ID: u.ID, Login: u.Login},
		Redeem: func(ctx context.Context, digest string) (string, error) {
			return s.passkeys.Redeem(ctx, r, u.ID, token, passkey.Purpose{Action: gate.ActionRequired, Digest: digest})
		},
	})
}
