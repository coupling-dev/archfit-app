package answers

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// EditAnswer is the whole-file edit answer. Every edit requires a passkey.
type EditAnswer struct {
	YAML, Base, Target, Token string
	Later                     bool
}

// Edits validates an edit before prompting, then redeems the existing one-use token.
type Edits interface {
	Editor(context.Context, api.Repo) (gate.EditBase, error)
	ReloadEditor(context.Context, api.Repo, passkey.User) (gate.EditBase, error)
	PreviewEdit(context.Context, api.Repo, passkey.User, EditAnswer) (gate.EditView, error)
	Edit(context.Context, api.Repo, passkey.User, EditAnswer) (gate.AnswerOutcome, error)
}

func (s *service) Editor(ctx context.Context, r api.Repo) (gate.EditBase, error) {
	return s.gate.Editor(ctx, gate.Target{Repo: r})
}

func (s *service) ReloadEditor(ctx context.Context, r api.Repo, u passkey.User) (gate.EditBase, error) {
	return s.gate.ReloadEditor(ctx, gate.Target{Repo: r}, gate.AnswerOwner{ID: u.ID, Login: u.Login})
}

func editInput(u passkey.User, a EditAnswer) gate.EditInput {
	return gate.EditInput{File: []byte(a.YAML), Base: a.Base, Target: a.Target,
		Owner: gate.AnswerOwner{ID: u.ID, Login: u.Login}, Later: a.Later}
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
