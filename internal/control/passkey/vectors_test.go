package passkey

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/alexei-led/archfit-app/internal/store"
)

// vector is one ceremony of a W3C test vector, hex as published.
type vector struct {
	AttestationObject string `json:"attestationObject"`
	AuthenticatorData string `json:"authenticatorData"`
	ClientDataJSON    string `json:"clientDataJSON"`
	Signature         string `json:"signature"`
	UserHandle        string `json:"userHandle"`
	CredentialID      string `json:"credentialID"`
	Challenge         string `json:"challenge"`
}

type vectorCase struct {
	Spec           string  `json:"spec"`
	Registration   vector  `json:"registration"`
	Authentication *vector `json:"authentication"`
}

func loadVectors(t *testing.T) map[string]vectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/w3c-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]vectorCase{}
	for name, v := range doc {
		if name == "source" {
			continue
		}
		var c vectorCase
		if err := json.Unmarshal(v, &c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = c
	}
	return out
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pinChallenge replaces the challenge of the user's open ceremony with the vector's, as if the App had made it.
func (e *env) pinChallenge(u User, challengeHex string) {
	e.t.Helper()
	tenant := store.Tenant(repo.InstallationID)
	raw, err := e.store.Get(ctx, tenant, challengeKey(u.ID))
	if err != nil {
		e.t.Fatal(err)
	}
	c := readChallenge(raw)
	var session webauthn.SessionData
	if err := json.Unmarshal(c.Session, &session); err != nil {
		e.t.Fatal(err)
	}
	session.Challenge = b64(unhex(e.t, challengeHex))
	if c.Session, err = json.Marshal(session); err != nil {
		e.t.Fatal(err)
	}
	next, err := c.encode()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.Put(ctx, tenant, challengeKey(u.ID), next); err != nil {
		e.t.Fatal(err)
	}
}

func credentialJSON(t *testing.T, v vector, response map[string]string) []byte {
	t.Helper()
	id := b64(unhex(t, v.CredentialID))
	b, err := json.Marshal(map[string]any{
		"id": id, "rawId": id, "type": "public-key", "response": response, "clientExtensionResults": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) registerVector(v vector) (Passkey, error) {
	e.t.Helper()
	if _, err := e.pk.BeginRegistration(ctx, repo, alice, ""); err != nil {
		e.t.Fatal(err)
	}
	e.pinChallenge(alice, v.Challenge)
	return e.pk.FinishRegistration(ctx, repo, alice, credentialJSON(e.t, v, map[string]string{
		"clientDataJSON":    b64(unhex(e.t, v.ClientDataJSON)),
		"attestationObject": b64(unhex(e.t, v.AttestationObject)),
	}))
}

// The W3C vectors run through the App's own ceremonies at the vectors' relying party: bytes a real authenticator
// made are accepted with user verification and refused without it.
func TestW3CVectors(t *testing.T) {
	vectors := loadVectors(t)

	t.Run("packed self attestation with user verification registers", func(t *testing.T) {
		v := vectors["packed-self-es256"]
		e := newEnvAt(t, "https://example.org")
		p, err := e.registerVector(v.Registration)
		if err != nil {
			t.Fatalf("registration (%s): %v", v.Spec, err)
		}
		if p.ID != b64(unhex(t, v.Registration.CredentialID)) {
			t.Errorf("registered %s, want the vector's credential", p.ID)
		}

		a := v.Authentication
		if _, err := e.pk.BeginStepUp(ctx, repo, alice, approve); err != nil {
			t.Fatal(err)
		}
		e.pinChallenge(alice, a.Challenge)
		_, err = e.pk.FinishStepUp(ctx, repo, alice, credentialJSON(t, *a, map[string]string{
			"clientDataJSON":    b64(unhex(t, a.ClientDataJSON)),
			"authenticatorData": b64(unhex(t, a.AuthenticatorData)),
			"signature":         b64(unhex(t, a.Signature)),
		}))
		wantNoUserVerification(t, err) // its authentication flags are 0x09: present, not verified
	})

	t.Run("none attestation without user verification is refused", func(t *testing.T) {
		v := vectors["none-es256"]
		e := newEnvAt(t, "https://example.org")
		_, err := e.registerVector(v.Registration) // flags 0x59: present, not verified
		wantNoUserVerification(t, err)
	})

	t.Run("the vectors are bound to their relying party", func(t *testing.T) {
		v := vectors["packed-self-es256"]
		e := newEnv(t) // the App at app.example, not example.org
		_, err := e.registerVector(v.Registration)
		wantErr(t, err, ErrCeremony)
	})
}
