package passkey

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// registered is an env where alice has one ES256 passkey.
func registered(t *testing.T) (*env, *authenticator) {
	t.Helper()
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, a, "")
	return e, a
}

func TestUserVerificationIsRequired(t *testing.T) {
	t.Run("registration", func(t *testing.T) {
		e := newEnv(t)
		a := newAuthenticator(t, webauthncose.AlgES256)
		a.flags = protocol.FlagUserPresent
		_, err := e.register(alice, a, "")
		wantNoUserVerification(t, err)
		if got := e.list(alice); len(got) != 0 {
			t.Errorf("List = %+v, want nothing stored", got)
		}
		if len(e.issues.all()) != 0 {
			t.Error("a refused registration opened an issue")
		}
	})
	t.Run("step-up", func(t *testing.T) {
		e, a := registered(t)
		a.flags = protocol.FlagUserPresent
		token, err := e.stepUp(alice, a, approve)
		wantNoUserVerification(t, err)
		if token != "" {
			t.Error("a refused step-up returned a token")
		}
	})
}

func TestWrongOriginOrRelyingPartyIsRefused(t *testing.T) {
	for name, set := range map[string]func(a *authenticator){
		"another origin":           func(a *authenticator) { a.origin = "https://evil.example" },
		"the origin over http":     func(a *authenticator) { a.origin = "http://app.example" },
		"a sub-domain origin":      func(a *authenticator) { a.origin = "https://x.app.example" },
		"another relying party id": func(a *authenticator) { a.rpID = "evil.example" },
		"a parent relying party":   func(a *authenticator) { a.rpID = "example" },
	} {
		t.Run(name+", registration", func(t *testing.T) {
			e := newEnv(t)
			a := newAuthenticator(t, webauthncose.AlgES256)
			set(a)
			_, err := e.register(alice, a, "")
			wantErr(t, err, ErrCeremony)
		})
		t.Run(name+", step-up", func(t *testing.T) {
			e, a := registered(t)
			set(a)
			_, err := e.stepUp(alice, a, approve)
			wantErr(t, err, ErrCeremony)
		})
	}
}

func TestAChallengeIsUsedOnce(t *testing.T) {
	t.Run("registration", func(t *testing.T) {
		e := newEnv(t)
		a := newAuthenticator(t, webauthncose.AlgES256)
		options, err := e.pk.BeginRegistration(ctx, repo, alice, "")
		if err != nil {
			t.Fatal(err)
		}
		response := a.create(options)
		if _, err := e.pk.FinishRegistration(ctx, repo, alice, response); err != nil {
			t.Fatal(err)
		}
		_, err = e.pk.FinishRegistration(ctx, repo, alice, response)
		wantErr(t, err, ErrCeremony)
		if n := len(e.issues.all()); n != 1 {
			t.Errorf("issues = %d, want 1", n)
		}
	})
	t.Run("step-up", func(t *testing.T) {
		e, a := registered(t)
		options, err := e.pk.BeginStepUp(ctx, repo, alice, approve)
		if err != nil {
			t.Fatal(err)
		}
		response := a.get(options)
		if _, err := e.pk.FinishStepUp(ctx, repo, alice, response); err != nil {
			t.Fatal(err)
		}
		_, err = e.pk.FinishStepUp(ctx, repo, alice, response)
		wantErr(t, err, ErrCeremony)
	})
}

func TestAChallengeExpiresAfterFiveMinutes(t *testing.T) {
	for _, tc := range []struct {
		after time.Duration
		want  error
	}{
		{TTL - time.Second, nil},
		{TTL, ErrCeremony},
		{TTL + time.Second, ErrCeremony},
	} {
		t.Run(tc.after.String(), func(t *testing.T) {
			e := newEnv(t)
			a := newAuthenticator(t, webauthncose.AlgES256)
			options, err := e.pk.BeginRegistration(ctx, repo, alice, "")
			if err != nil {
				t.Fatal(err)
			}
			e.now = e.now.Add(tc.after)
			_, err = e.pk.FinishRegistration(ctx, repo, alice, a.create(options))
			if !errors.Is(err, tc.want) {
				t.Fatalf("registration after %v: error = %v, want %v", tc.after, err, tc.want)
			}

			e, a = registered(t)
			options, err = e.pk.BeginStepUp(ctx, repo, alice, approve)
			if err != nil {
				t.Fatal(err)
			}
			e.now = e.now.Add(tc.after)
			_, err = e.pk.FinishStepUp(ctx, repo, alice, a.get(options))
			if !errors.Is(err, tc.want) {
				t.Fatalf("step-up after %v: error = %v, want %v", tc.after, err, tc.want)
			}
		})
	}
}

func TestANewCeremonyReplacesTheOlderOne(t *testing.T) {
	e, a := registered(t)
	older, err := e.pk.BeginStepUp(ctx, repo, alice, approve)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := e.pk.BeginStepUp(ctx, repo, alice, approve)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pk.FinishStepUp(ctx, repo, alice, a.get(older))
	wantErr(t, err, ErrCeremony)
	if _, err := e.pk.FinishStepUp(ctx, repo, alice, a.get(newer)); err != nil {
		t.Fatalf("the newer ceremony: %v", err)
	}
}

func TestACeremonyFinishesInTheRepositoryItBeganIn(t *testing.T) {
	e, a := registered(t)
	other := api.Repo{RepoRef: api.RepoRef{InstallationID: repo.InstallationID, FullName: "acme/other"}, ID: 71}
	options, err := e.pk.BeginStepUp(ctx, repo, alice, approve)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pk.FinishStepUp(ctx, other, alice, a.get(options))
	wantErr(t, err, ErrCeremony)
}

func TestTheSignCounterMustGrow(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stored, sent   uint32
		want           error
		wantSignCount  uint32
		wantTokenGiven bool
	}{
		{"grows", 5, 6, nil, 6, true},
		{"equal", 5, 5, ErrCounter, 5, false},
		{"goes back", 5, 4, ErrCounter, 5, false},
		{"goes back to zero", 5, 0, ErrCounter, 5, false},
		{"an authenticator that does not count", 0, 0, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			a := newAuthenticator(t, webauthncose.AlgES256)
			a.count = tc.stored
			e.mustRegister(alice, a, "")
			a.count, a.step = tc.sent, 0
			token, err := e.stepUp(alice, a, approve)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if (token != "") != tc.wantTokenGiven {
				t.Errorf("token given = %v, want %v", token != "", tc.wantTokenGiven)
			}
			raw, err := e.store.Get(ctx, store.Tenant(repo.InstallationID), passkeyKey(alice.ID))
			if err != nil {
				t.Fatal(err)
			}
			rec, err := readRecord(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := rec.Passkeys[0].SignCount; got != tc.wantSignCount {
				t.Errorf("stored sign count = %d, want %d", got, tc.wantSignCount)
			}
		})
	}
}

func TestATokenIsUsedOnceForItsPurpose(t *testing.T) {
	e, a := registered(t)
	token := e.mustStepUp(alice, a, approve)
	if _, err := e.pk.Redeem(ctx, repo, alice.ID, token, approve); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	_, err := e.pk.Redeem(ctx, repo, alice.ID, token, approve)
	wantErr(t, err, ErrToken)
}

func TestATokenIsBoundToItsPurposeUserAndTime(t *testing.T) {
	carolUser := User{ID: 103, Login: "carol"}
	other := api.Repo{RepoRef: api.RepoRef{InstallationID: repo.InstallationID, FullName: "acme/other"}, ID: 71}
	for _, tc := range []struct {
		name   string
		redeem func(e *env, token string) error
	}{
		{"another request", func(e *env, token string) error {
			p := approve
			p.Request = 432
			_, err := e.pk.Redeem(ctx, repo, alice.ID, token, p)
			return err
		}},
		{"another answer", func(e *env, token string) error {
			p := approve
			p.Action = "approve-until"
			_, err := e.pk.Redeem(ctx, repo, alice.ID, token, p)
			return err
		}},
		{"another policy change", func(e *env, token string) error {
			p := approve
			p.Digest = "sha256:" + strings.Repeat("b", 64)
			_, err := e.pk.Redeem(ctx, repo, alice.ID, token, p)
			return err
		}},
		{"another repository", func(e *env, token string) error {
			_, err := e.pk.Redeem(ctx, other, alice.ID, token, approve)
			return err
		}},
		{"another user", func(e *env, token string) error {
			_, err := e.pk.Redeem(ctx, repo, carolUser.ID, token, approve)
			return err
		}},
		{"another token", func(e *env, token string) error {
			_, err := e.pk.Redeem(ctx, repo, alice.ID, token+"x", approve)
			return err
		}},
		{"no token", func(e *env, _ string) error {
			_, err := e.pk.Redeem(ctx, repo, alice.ID, "", approve)
			return err
		}},
		{"after five minutes", func(e *env, token string) error {
			e.now = e.now.Add(TTL)
			_, err := e.pk.Redeem(ctx, repo, alice.ID, token, approve)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, a := registered(t)
			token := e.mustStepUp(alice, a, approve)
			wantErr(t, tc.redeem(e, token), ErrToken)
		})
	}
}

func TestTwoRedeemsAtOnceOneWins(t *testing.T) {
	e, a := registered(t)
	token := e.mustStepUp(alice, a, approve)
	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() { _, errs[i] = e.pk.Redeem(ctx, repo, alice.ID, token, approve) })
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrToken) && !errors.Is(err, store.ErrConflict):
			t.Errorf("a losing redeem returned %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d redeems won, want exactly 1", won)
	}
}

func TestStepUpRefusesAMalformedPurpose(t *testing.T) {
	e, _ := registered(t)
	for name, p := range map[string]Purpose{
		"no action":            {},
		"an upper-case action": {Action: "Approve"},
		"a long action":        {Action: strings.Repeat("a", 33)},
		"a negative request":   {Action: "approve", Request: -1},
		"a digest with spaces": {Action: "approve", Digest: "sha256: x"},
		"a long digest":        {Action: "approve", Digest: strings.Repeat("a", 129)},
		"a passkey not base64": {Action: ActionRemove, Passkey: "a/b"},
		"a long passkey":       {Action: ActionRemove, Passkey: strings.Repeat("a", maxPasskeyID+1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.pk.BeginStepUp(ctx, repo, alice, p)
			wantErr(t, err, ErrInvalid)
		})
	}
}

func TestStepUpNeedsAPasskey(t *testing.T) {
	e := newEnv(t)
	_, err := e.pk.BeginStepUp(ctx, repo, alice, approve)
	wantErr(t, err, ErrNoPasskey)
}

func TestMalformedCredentialsAreRefused(t *testing.T) {
	e, _ := registered(t)
	for name, body := range map[string][]byte{
		"empty":    nil,
		"not JSON": []byte("{"),
		"no rawId": []byte(`{"id":"AA","type":"public-key","response":{}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.pk.BeginStepUp(ctx, repo, alice, approve); err != nil {
				t.Fatal(err)
			}
			_, err := e.pk.FinishStepUp(ctx, repo, alice, body)
			wantErr(t, err, ErrCeremony)
			_, err = e.pk.FinishRegistration(ctx, repo, alice, body)
			wantErr(t, err, ErrCeremony)
		})
	}
}
