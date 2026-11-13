package answers

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// OwnerAction is an owner action outside a request. Digest is what the owner saw before step-up.
type OwnerAction struct {
	Kind   string
	Pull   int
	Digest string
	Later  bool
	Token  string
}

// Owners binds owner actions to the signed-in user's passkey.
type Owners interface {
	OwnerView(context.Context, api.Repo, OwnerAction) (gate.OwnerView, error)
	OwnerDigest(context.Context, api.Repo, OwnerAction) (string, error)
	OwnerAct(context.Context, api.Repo, gate.OwnerUser, OwnerAction) (gate.OwnerOutcome, error)
}

type owners struct {
	gate     gate.OwnerActions
	passkeys passkey.Passkeys
}

// NewOwners joins the gate's owner actions and the user's passkeys.
func NewOwners(g gate.OwnerActions, p passkey.Passkeys) Owners { return &owners{g, p} }

func (s *owners) OwnerDigest(ctx context.Context, r api.Repo, a OwnerAction) (string, error) {
	return s.gate.OwnerDigest(ctx, gate.Target{Repo: r}, gate.OwnerAction{Kind: a.Kind, Pull: a.Pull})
}

func (s *owners) OwnerAct(ctx context.Context, r api.Repo, u gate.OwnerUser, a OwnerAction) (gate.OwnerOutcome, error) {
	return s.gate.OwnerAct(ctx, gate.Target{Repo: r}, gate.OwnerInput{
		Action: gate.OwnerAction{Kind: a.Kind, Pull: a.Pull}, User: u,
		Digest: a.Digest, Later: a.Later,
		Redeem: func(ctx context.Context, digest string) (string, error) {
			return s.passkeys.Redeem(ctx, r, u.ID, a.Token, passkey.Purpose{Action: a.Kind, Digest: digest})
		},
	})
}

func (s *owners) OwnerView(ctx context.Context, r api.Repo, a OwnerAction) (gate.OwnerView, error) {
	return s.gate.OwnerView(ctx, gate.Target{Repo: r}, gate.OwnerAction{Kind: a.Kind, Pull: a.Pull})
}
