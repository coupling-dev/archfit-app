package passkey

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/alexei-led/archfit-app/internal/store"
)

func TestRecordsAreReadStrictly(t *testing.T) {
	good := `{"schema":"archfit-app.passkey.v1","handle":"aGFuZGxl","passkeys":[{"id":"aWQ=","public_key":"a2V5","flags":5,"sign_count":0,"created":"2026-11-12T10:00:00Z"}]}`
	if rec, err := readRecord([]byte(good)); err != nil || len(rec.Passkeys) != 1 {
		t.Fatalf("readRecord(good) = %+v, %v", rec, err)
	}
	for name, raw := range map[string]string{
		"another schema":   strings.Replace(good, "passkey.v1", "passkey.v2", 1),
		"no schema":        `{"handle":"aGFuZGxl","passkeys":[]}`,
		"an unknown field": strings.Replace(good, `"handle"`, `"admin":true,"handle"`, 1),
		"trailing data":    good + `{}`,
		"no passkey":       `{"schema":"archfit-app.passkey.v1","handle":"aGFuZGxl","passkeys":[]}`,
		"no handle":        `{"schema":"archfit-app.passkey.v1","passkeys":[{"id":"aWQ"}]}`,
		"not JSON":         `{`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := readRecord([]byte(raw))
			if err == nil {
				t.Fatal("readRecord accepted it")
			}
			if strings.Contains(err.Error(), "aGFuZGxl") {
				t.Errorf("the error quotes the value: %v", err)
			}
		})
	}
}

func TestAChallengeThatCannotBeReadIsReplacedNotTrusted(t *testing.T) {
	e, a := registered(t)
	tenant := store.Tenant(repo.InstallationID)
	if err := e.store.Put(ctx, tenant, challengeKey(alice.ID), []byte(`{"schema":"archfit-app.challenge.v1","kind":"token","x":1}`)); err != nil {
		t.Fatal(err)
	}
	_, err := e.pk.Redeem(ctx, repo, alice.ID, "anything", approve)
	wantErr(t, err, ErrToken)
	e.mustStepUp(alice, a, approve) // a new ceremony replaces it
}

func TestARecordThatCannotBeReadRefusesEverything(t *testing.T) {
	e, a := registered(t)
	tenant := store.Tenant(repo.InstallationID)
	if err := e.store.Put(ctx, tenant, passkeyKey(alice.ID), []byte(`{"schema":"other"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.register(alice, newAuthenticator(t, webauthncose.AlgES256), ""); err == nil {
		t.Error("registration over an unreadable record succeeded; it must not overwrite it")
	}
	if _, err := e.stepUp(alice, a, approve); err == nil {
		t.Error("step-up over an unreadable record succeeded")
	}
	if _, err := e.pk.List(ctx, repo, alice.ID); err == nil {
		t.Error("List of an unreadable record succeeded")
	}
}

func TestAssignees(t *testing.T) {
	many := []string{"o1", "o2", "o3", "o4", "o5", "o6", "o7", "o8", "o9", "o10", "o11"}
	for _, tc := range []struct {
		name   string
		owners []string
		want   []string
	}{
		{"no owners", nil, []string{"alice"}},
		{"the user is an owner", []string{"Alice", "carol"}, []string{"alice", "carol"}},
		{"repeats", []string{"carol", "CAROL"}, []string{"alice", "carol"}},
		{"more than GitHub allows", many, append([]string{"alice"}, many[:9]...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := assignees("alice", tc.owners); !slices.Equal(got, tc.want) {
				t.Errorf("assignees = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewDerivesTheRelyingPartyFromBaseURL(t *testing.T) {
	for _, tc := range []struct {
		baseURL, id, origin string
	}{
		{"https://app.example", "app.example", "https://app.example"},
		{"https://app.example/", "app.example", "https://app.example"},
		{"https://app.example:8443", "app.example", "https://app.example:8443"},
		{"http://localhost:8080", "localhost", "http://localhost:8080"},
		{"http://127.0.0.1:8080", "127.0.0.1", "http://127.0.0.1:8080"},
	} {
		id, origin, err := relyingParty(tc.baseURL)
		if err != nil || id != tc.id || origin != tc.origin {
			t.Errorf("relyingParty(%q) = %q, %q, %v; want %q, %q", tc.baseURL, id, origin, err, tc.id, tc.origin)
		}
	}
	for _, bad := range []string{"", "app.example", "http://app.example", "ftp://app.example", "https://user@app.example", "//app.example"} {
		if _, err := New(Config{BaseURL: bad}, store.NewMemory(), &fakeOwners{}, &fakeIssues{}, time.Now); err == nil {
			t.Errorf("New accepted BaseURL %q", bad)
		}
	}
	if _, err := New(Config{BaseURL: appURL}, nil, &fakeOwners{}, &fakeIssues{}, time.Now); err == nil {
		t.Error("New accepted a nil Store")
	}
}

func TestSecretsNeverReachALogLine(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	options, err := e.pk.BeginRegistration(ctx, repo, alice, "")
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pk.FinishRegistration(ctx, repo, alice, a.create(options)); err != nil {
		t.Fatal(err)
	}
	token := e.mustStepUp(alice, a, approve)
	if _, err := e.pk.Redeem(ctx, repo, alice.ID, token, approve); err != nil {
		t.Fatal(err)
	}
	_, _ = e.pk.Redeem(ctx, repo, alice.ID, token, approve)

	out := logs.String()
	if !strings.Contains(out, `"outcome":"token"`) || !strings.Contains(out, `"outcome":"ok"`) {
		t.Fatalf("the outcomes were not logged:\n%s", out)
	}
	for name, secret := range map[string]string{
		"token": token, "challenge": o.PublicKey.Challenge, "user handle": b64(a.handle), "passkey id": b64(a.id),
	} {
		if strings.Contains(out, secret) {
			t.Errorf("a log line holds the %s", name)
		}
	}
}
