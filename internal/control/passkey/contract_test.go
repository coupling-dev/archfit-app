package passkey

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

func TestRegisterStepUpAndRedeemEachAlgorithm(t *testing.T) {
	for name, alg := range map[string]webauthncose.COSEAlgorithmIdentifier{
		"ES256": webauthncose.AlgES256, "Ed25519": webauthncose.AlgEd25519, "EdDSA": webauthncose.AlgEdDSA,
		"RS256": webauthncose.AlgRS256,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			a := newAuthenticator(t, alg)
			p := e.mustRegister(alice, a, "")
			if p.ID != b64(a.id) || !p.Created.Equal(e.now) {
				t.Fatalf("registered %+v, want id %s created %v", p, b64(a.id), e.now)
			}

			e.now = e.now.Add(time.Minute)
			token := e.mustStepUp(alice, a, approve)
			got, err := e.pk.Redeem(ctx, repo, alice.ID, token, approve)
			if err != nil {
				t.Fatalf("Redeem: %v", err)
			}
			if got != p.ID {
				t.Errorf("Redeem = %q, want the passkey %q", got, p.ID)
			}
			list := e.list(alice)
			if len(list) != 1 || !list[0].LastUsed.Equal(e.now) {
				t.Errorf("List = %+v, want one passkey last used at %v", list, e.now)
			}
		})
	}
}

func TestCreationOptionsFollowThePasskeyParameters(t *testing.T) {
	e := newEnv(t)
	raw, err := e.pk.BeginRegistration(ctx, repo, alice, "")
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		PublicKey struct {
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
			User struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"user"`
			Params []struct {
				Alg int `json:"alg"`
			} `json:"pubKeyCredParams"`
			Timeout   int `json:"timeout"`
			Selection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
			Attestation string `json:"attestation"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	pk := o.PublicKey
	var algs []int
	for _, p := range pk.Params {
		algs = append(algs, p.Alg)
	}
	switch {
	case pk.RP.ID != appHost:
		t.Errorf("rp.id = %q, want %q", pk.RP.ID, appHost)
	case pk.User.Name != "alice" || len(pk.User.ID) < 40:
		t.Errorf("user = %+v, want the login and a 32-byte handle", pk.User)
	case !slices.Equal(algs, []int{-7, -19, -8, -257}):
		t.Errorf("algorithms = %v, want ES256, Ed25519, EdDSA, RS256", algs)
	case pk.Timeout != int(TTL.Milliseconds()):
		t.Errorf("timeout = %d, want %d", pk.Timeout, TTL.Milliseconds())
	case pk.Selection.ResidentKey != "preferred" || pk.Selection.UserVerification != "required":
		t.Errorf("authenticatorSelection = %+v, want resident key preferred, user verification required", pk.Selection)
	case pk.Attestation != "none":
		t.Errorf("attestation = %q, want none", pk.Attestation)
	}
}

func TestStepUpOptionsAllowOnlyTheUsersPasskeys(t *testing.T) {
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, a, "")
	raw, err := e.pk.BeginStepUp(ctx, repo, alice, approve)
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		PublicKey struct {
			RPID  string `json:"rpId"`
			Allow []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
			UserVerification string `json:"userVerification"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if o.PublicKey.RPID != appHost || o.PublicKey.UserVerification != "required" ||
		len(o.PublicKey.Allow) != 1 || o.PublicKey.Allow[0].ID != b64(a.id) {
		t.Errorf("request options = %+v, want rpId %s, user verification required, only %s", o.PublicKey, appHost, b64(a.id))
	}
}

func TestSecondRegistrationNeedsAnExistingPasskey(t *testing.T) {
	e := newEnv(t)
	first := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, first, "")
	second := newAuthenticator(t, webauthncose.AlgES256)

	_, err := e.register(alice, second, "")
	wantErr(t, err, ErrNeedsPasskey)

	other := e.mustStepUp(alice, first, approve)
	_, err = e.register(alice, second, other)
	wantErr(t, err, ErrToken)

	token := e.mustStepUp(alice, first, Purpose{Action: ActionRegister})
	options, err := e.pk.BeginRegistration(ctx, repo, alice, token)
	if err != nil {
		t.Fatalf("BeginRegistration with a register token: %v", err)
	}
	var o struct {
		PublicKey struct {
			Exclude []struct {
				ID string `json:"id"`
			} `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	if len(o.PublicKey.Exclude) != 1 || o.PublicKey.Exclude[0].ID != b64(first.id) {
		t.Errorf("excludeCredentials = %+v, want the first passkey", o.PublicKey.Exclude)
	}
	if _, err := e.pk.FinishRegistration(ctx, repo, alice, second.create(options)); err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}
	if !slices.Equal(second.handle, first.handle) {
		t.Error("the second passkey got another user handle; one handle per user is required")
	}
	if got := e.list(alice); len(got) != 2 {
		t.Errorf("List = %d passkeys, want 2", len(got))
	}

	_, err = e.pk.BeginRegistration(ctx, repo, alice, token)
	wantErr(t, err, ErrToken) // the register token was consumed at begin
}

func TestAtMostMaxPasskeys(t *testing.T) {
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, a, "")
	for range MaxPasskeys - 1 {
		e.mustRegister(alice, newAuthenticator(t, webauthncose.AlgES256), e.mustStepUp(alice, a, Purpose{Action: ActionRegister}))
	}
	_, err := e.register(alice, newAuthenticator(t, webauthncose.AlgES256), e.mustStepUp(alice, a, Purpose{Action: ActionRegister}))
	wantErr(t, err, ErrTooMany)
}
