package passkey

import (
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

func TestFirstRegistrationRequiresOperatorConfirmation(t *testing.T) {
	e := newEnv(t)
	if _, err := e.pk.BeginRegistration(ctx, repo, alice, ""); err == nil {
		t.Fatal("first registration accepted sign-in alone")
	}
	a := newAuthenticator(t, webauthncose.AlgES256)
	options, err := e.beginRegistration(ctx, repo, alice, "")
	if err != nil {
		t.Fatal(err)
	}
	credential := a.create(options)
	if _, err = e.pk.FinishRegistration(ctx, repo, alice, credential); err == nil {
		t.Fatal("finish accepted missing session binding")
	}
	wrong := alice
	wrong.SessionBinding = "another session"
	if _, err = e.pk.FinishRegistration(ctx, repo, wrong, credential); err == nil {
		t.Fatal("finish accepted wrong session binding")
	}
	replacement := repo
	replacement.ID++
	bound := alice
	bound.SessionBinding = "test-enrolment-session"
	if _, err = e.pk.FinishRegistration(ctx, replacement, bound, credential); err == nil {
		t.Fatal("finish accepted replacement repository")
	}
	if _, err = e.finishRegistration(ctx, repo, alice, credential); err != nil {
		t.Fatal(err)
	}
}
