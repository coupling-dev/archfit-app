package passkey

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

func TestEditPurposeCannotBeReboundOrReused(t *testing.T) {
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, a, "")
	purpose := Purpose{Action: "edit", Digest: "sha256:" + strings.Repeat("a", 64), BaseDigest: "sha256:" + strings.Repeat("b", 64), Target: strings.Repeat("c", 40)}
	token := e.mustStepUp(alice, a, purpose)
	for _, field := range []string{"new digest", "base", "target"} {
		other := purpose
		switch field {
		case "new digest":
			other.Digest = "sha256:" + strings.Repeat("d", 64)
		case "base":
			other.BaseDigest = "sha256:" + strings.Repeat("d", 64)
		case "target":
			other.Target = strings.Repeat("d", 40)
		}
		if _, err := e.pk.Redeem(ctx, repo, alice.ID, token, other); !errors.Is(err, ErrToken) {
			t.Fatalf("%s: %v", field, err)
		}
	}
	if _, err := e.pk.Redeem(ctx, repo, alice.ID, token, purpose); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pk.Redeem(ctx, repo, alice.ID, token, purpose); !errors.Is(err, ErrToken) {
		t.Fatal("token reused", err)
	}
}

func TestEditStepUpNeedsCompletePreviewBinding(t *testing.T) {
	e := newEnv(t)
	for _, p := range []Purpose{
		{Action: "edit"},
		{Action: "edit", Digest: "sha256:" + strings.Repeat("a", 64)},
		{Action: "edit", Digest: "sha256:" + strings.Repeat("a", 64), BaseDigest: "sha256:" + strings.Repeat("b", 64), Target: "not a tip"},
	} {
		if _, err := e.pk.BeginStepUp(ctx, repo, alice, p); !errors.Is(err, ErrInvalid) {
			t.Fatalf("incomplete purpose %+v: %v", p, err)
		}
	}
}
