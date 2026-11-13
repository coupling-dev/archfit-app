package answers_test

import (
	"context"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
)

type editGate struct {
	fakeGate
	edit gate.EditInput
}

func (g *editGate) EditPreview(_ context.Context, _ gate.Target, in gate.EditInput) (gate.EditView, error) {
	g.edit = in
	return gate.EditView{Digest: g.digest, Target: in.Target}, nil
}

func (g *editGate) Edit(ctx context.Context, _ gate.Target, in gate.EditInput) (gate.AnswerOutcome, error) {
	g.edit = in
	if in.Redeem != nil {
		if _, err := in.Redeem(ctx, g.digest); err != nil {
			return gate.AnswerOutcome{}, err
		}
	}
	return gate.AnswerOutcome{PullRequest: 12}, nil
}

func TestEditTokenBindsNewDigestAndPreviewInputs(t *testing.T) {
	g := &editGate{fakeGate: fakeGate{digest: "sha256:new"}}
	p := &fakePasskeys{}
	a := answers.EditAnswer{YAML: "whole file", Base: "sha256:old", Target: "tip", Token: "token"}
	u := passkey.User{ID: 501, Login: "olivia"}
	svc := answers.New(g, p)
	_, err := svc.PreviewEdit(context.Background(), repo, u, a)
	if err != nil || p.token != "" {
		t.Fatalf("preview redeemed: %v %q", err, p.token)
	}
	_, err = svc.Edit(context.Background(), repo, u, a)
	if err != nil {
		t.Fatal(err)
	}
	want := passkey.Purpose{Action: "edit", Digest: "sha256:new", Target: "tip", BaseDigest: "sha256:old"}
	if p.purpose != want || p.token != "token" || string(g.edit.File) != a.YAML {
		t.Fatalf("purpose %+v, input %+v", p.purpose, g.edit)
	}
}
