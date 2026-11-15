package answers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
)

type ownerGate struct {
	target       gate.Target
	action       gate.OwnerAction
	in           gate.OwnerInput
	err          error
	view         gate.OwnerView
	viewCalls    int
	beforeRedeem func()
	redeemDigest string
}

func (g *ownerGate) OwnerDigest(_ context.Context, target gate.Target, a gate.OwnerAction) (string, error) {
	g.target, g.action = target, a
	return "sha256:bound", g.err
}
func (g *ownerGate) OwnerAct(ctx context.Context, target gate.Target, in gate.OwnerInput) (gate.OwnerOutcome, error) {
	g.target, g.in = target, in
	if g.err != nil {
		return gate.OwnerOutcome{}, g.err
	}
	if g.beforeRedeem != nil {
		g.beforeRedeem()
	}
	digest := g.redeemDigest
	if digest == "" {
		digest = "sha256:rebound"
	}
	credential, err := in.Redeem(ctx, digest)
	if err != nil {
		return gate.OwnerOutcome{}, err
	}
	if credential != "cred-1" {
		return gate.OwnerOutcome{}, errors.New("missing credential")
	}
	return gate.OwnerOutcome{Approval: "a1", PullRequest: 12, State: gate.AnswerLater, Issue: 70, Check: gate.CheckPending}, nil
}

func TestOwnerActionsBindTheStepUpToTheActionAndGateDigest(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		pull       int
	}{
		{"first onboarding", gate.OwnerFirst, 12}, {"first converted", gate.OwnerFirst, 0},
		{"workflow", gate.OwnerWorkflow, 12}, {"confirm", gate.OwnerConfirm, 0}, {"revert", gate.OwnerRevert, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, p := &ownerGate{}, &fakePasskeys{}
			s := answers.NewOwners(g, p)
			a := answers.OwnerAction{Kind: tt.kind, Pull: tt.pull, Digest: "sha256:seen", Later: true, Token: "tok"}
			d, err := s.OwnerDigest(context.Background(), repo, a)
			if err != nil || d != "sha256:bound" || g.action != (gate.OwnerAction{Kind: tt.kind, Pull: tt.pull}) || g.target.Repo != repo {
				t.Fatalf("OwnerDigest = %s, %v; action %+v, target %+v", d, err, g.action, g.target)
			}
			out, err := s.OwnerAct(context.Background(), repo, gate.OwnerUser{ID: 501, Login: "olivia", Admin: tt.kind == gate.OwnerFirst}, a)
			if err != nil || out.Approval != "a1" || out.PullRequest != 12 || out.Issue != 70 || out.State != gate.AnswerLater || out.Check != gate.CheckPending {
				t.Fatalf("OwnerAct = %+v, %v", out, err)
			}
			if g.in.Action != g.action || g.in.User != (gate.OwnerUser{ID: 501, Login: "olivia", Admin: tt.kind == gate.OwnerFirst}) || g.in.Digest != a.Digest || !g.in.Later || g.target.Repo != repo {
				t.Errorf("gate input %+v, target %+v", g.in, g.target)
			}
			if p.purpose != (passkey.Purpose{Action: tt.kind, Digest: "sha256:rebound"}) || p.userID != 501 || p.token != "tok" {
				t.Errorf("redeem = %+v, user %d, token %q", p.purpose, p.userID, p.token)
			}
		})
	}
}

func TestOwnerActionsPreserveGateAndPasskeyRefusals(t *testing.T) {
	for _, refusal := range []error{gate.ErrOwnerNotAdmin, gate.ErrOwnerNotOwner, gate.ErrOwnerMoved, gate.ErrNotOutsideFlow} {
		g := &ownerGate{err: refusal}
		p := &fakePasskeys{}
		s := answers.NewOwners(g, p)
		if _, err := s.OwnerDigest(context.Background(), repo, answers.OwnerAction{Kind: gate.OwnerFirst}); !errors.Is(err, refusal) {
			t.Errorf("digest = %v, want %v", err, refusal)
		}
		if _, err := s.OwnerAct(context.Background(), repo, gate.OwnerUser{ID: 501}, answers.OwnerAction{Kind: gate.OwnerFirst, Token: "unused"}); !errors.Is(err, refusal) {
			t.Errorf("act = %v, want %v", err, refusal)
		}
		if p.token != "" {
			t.Error("redeemed on refusal")
		}
	}
	g, p := &ownerGate{}, &fakePasskeys{err: passkey.ErrToken}
	if _, err := answers.NewOwners(g, p).OwnerAct(context.Background(), repo, gate.OwnerUser{ID: 501}, answers.OwnerAction{Kind: gate.OwnerFirst, Token: "used"}); !errors.Is(err, passkey.ErrToken) {
		t.Errorf("OwnerAct = %v, want token refusal", err)
	}
}

func (g *ownerGate) OwnerView(_ context.Context, target gate.Target, a gate.OwnerAction) (gate.OwnerView, error) {
	g.target, g.action = target, a
	g.viewCalls++
	return g.view, g.err
}
