package answers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

var repo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, ID: 9, DefaultBranch: "main"}

// fakeGate is the gate: it remembers the last input and, for an answer that loosens the policy, calls Redeem with its
// digest, as gate/proposals does.
type fakeGate struct {
	target gate.Target
	number int
	answer gate.RequestAnswer
	in     gate.AnswerInput
	digest string
	err    error
}

func (g *fakeGate) AnswerDigest(_ context.Context, t gate.Target, number int, a gate.RequestAnswer) (string, error) {
	g.target, g.number, g.answer = t, number, a
	return g.digest, g.err
}

func (g *fakeGate) Answer(ctx context.Context, t gate.Target, in gate.AnswerInput) (gate.AnswerOutcome, error) {
	g.target, g.in = t, in
	if loosens, _ := policy3.Loosens(in.Answer.Kind); loosens {
		if _, err := in.Redeem(ctx, g.digest); err != nil {
			return gate.AnswerOutcome{}, err
		}
	}
	return gate.AnswerOutcome{Approval: "a1", PullRequest: 12, State: "merged"}, g.err
}

// fakePasskeys knows Redeem only.
type fakePasskeys struct {
	passkey.Passkeys
	userID  int64
	token   string
	purpose passkey.Purpose
	err     error
}

func (p *fakePasskeys) Redeem(_ context.Context, r api.Repo, userID int64, token string, pp passkey.Purpose) (string, error) {
	p.userID, p.token, p.purpose = userID, token, pp
	if r.FullName != repo.FullName {
		return "", errors.New("another repository")
	}
	return "cred-1", p.err
}

func TestAnAnswerRedeemsTheTokenForItsRequestKindAndDigest(t *testing.T) {
	g, p := &fakeGate{digest: "sha256:ab"}, &fakePasskeys{}
	u := passkey.User{ID: 501, Login: "olivia"}
	out, err := answers.New(g, p).Answer(context.Background(), repo, u, 431,
		answers.Answer{Kind: "approve-until", Until: "2026-12-31", Later: true, Token: "tok"})
	if err != nil || out.PullRequest != 12 {
		t.Fatalf("Answer = %+v, %v", out, err)
	}
	want := passkey.Purpose{Action: "approve-until", Request: 431, Digest: "sha256:ab"}
	if p.userID != 501 || p.token != "tok" || p.purpose != want {
		t.Errorf("Redeem(%d, %q, %+v), want (501, tok, %+v)", p.userID, p.token, p.purpose, want)
	}
	if g.target.Repo.FullName != repo.FullName || g.in.Request != 431 || !g.in.Later ||
		g.in.Owner != (gate.AnswerOwner{ID: 501, Login: "olivia"}) ||
		g.in.Answer != (gate.RequestAnswer{Kind: policy3.ApproveUntil, Until: "2026-12-31"}) {
		t.Errorf("the gate got %+v for %+v", g.in, g.target)
	}
}

func TestARejectRedeemsNothing(t *testing.T) {
	g, p := &fakeGate{digest: "sha256:ab"}, &fakePasskeys{}
	_, err := answers.New(g, p).Answer(context.Background(), repo, passkey.User{ID: 501, Login: "olivia"}, 431,
		answers.Answer{Kind: "reject", Instead: "Use the port."})
	if err != nil || p.token != "" || g.in.Answer.Instead != "Use the port." {
		t.Errorf("err %v, redeemed %q, gate answer %+v", err, p.token, g.in.Answer)
	}
}

func TestARefusedTokenRefusesTheAnswer(t *testing.T) {
	g, p := &fakeGate{digest: "sha256:ab"}, &fakePasskeys{err: passkey.ErrToken}
	_, err := answers.New(g, p).Answer(context.Background(), repo, passkey.User{ID: 501, Login: "olivia"}, 431,
		answers.Answer{Kind: "approve", Token: "old"})
	if !errors.Is(err, passkey.ErrToken) {
		t.Errorf("err = %v, want passkey.ErrToken", err)
	}
}

func TestDigestAsksTheGate(t *testing.T) {
	g := &fakeGate{digest: "sha256:cd"}
	d, err := answers.New(g, &fakePasskeys{}).Digest(context.Background(), repo, 7, answers.Answer{Kind: "reject", Instead: "x"})
	if err != nil || d != "sha256:cd" || g.number != 7 || g.answer != (gate.RequestAnswer{Kind: policy3.Reject, Instead: "x"}) {
		t.Errorf("Digest = %q, %v; gate got %d %+v", d, err, g.number, g.answer)
	}
}
