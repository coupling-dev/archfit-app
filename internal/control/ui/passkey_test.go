package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/passkey"
)

const passkeysAPI = "/api/repos/" + repoName + "/passkeys"

func TestPasskeyRegistrationGetsTheUserAndWhetherTheyAreAnAdministrator(t *testing.T) {
	for _, tc := range []struct {
		perm      string
		wantAdmin bool
	}{
		{permRead, false},
		{permWrite, false},
		{permAdmin, true},
	} {
		t.Run(tc.perm, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", tc.perm)
			wantStatus(t, c.postJSON(passkeysAPI+"/register", passkeyTokenJSON{Token: fakeToken}), http.StatusOK)
			wantStatus(t, c.postJSON(passkeysAPI+"/register/finish", passkeyCredentialJSON{Credential: json.RawMessage(`{"id":"x"}`)}), http.StatusOK)

			calls := e.passkeys.all()
			if len(calls) != 2 {
				t.Fatalf("calls = %+v, want begin and finish", calls)
			}
			want := passkey.User{ID: e.user("alice").UserID, Login: "alice", Admin: tc.wantAdmin}
			for _, call := range calls {
				if call.user != want || call.repo.InstallationID != installA || call.repo.FullName != repoName {
					t.Errorf("%s got user %+v in %s/%d; want %+v in %s/%d", call.method, call.user, call.repo.FullName,
						call.repo.InstallationID, want, repoName, installA)
				}
			}
			if calls[0].token != fakeToken || calls[1].credential != `{"id":"x"}` {
				t.Errorf("token %q, credential %q; want the body's", calls[0].token, calls[1].credential)
			}
		})
	}
}

func TestPasskeyAnswersCarryTheOptionsTokenAndList(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)

	var options passkeyOptionsJSON
	res := c.postJSON(passkeysAPI+"/step-up", stepUpJSON{Action: "approve", Request: 431, Digest: "sha256:ab"})
	wantStatus(t, res, http.StatusOK)
	if err := json.Unmarshal(res.Body.Bytes(), &options); err != nil || string(options.Options) != fakeOptions {
		t.Errorf("step-up options = %s, %v; want %s", options.Options, err, fakeOptions)
	}
	if got := e.passkeys.all()[0].purpose; got != (passkey.Purpose{Action: "approve", Request: 431, Digest: "sha256:ab"}) {
		t.Errorf("purpose = %+v, want the body's, and no repository from the client", got)
	}

	var token stepUpTokenJSON
	res = c.postJSON(passkeysAPI+"/step-up/finish", passkeyCredentialJSON{Credential: json.RawMessage(`{}`)})
	wantStatus(t, res, http.StatusOK)
	if err := json.Unmarshal(res.Body.Bytes(), &token); err != nil || token.Token != fakeToken {
		t.Errorf("token = %+v, %v; want %s", token, err, fakeToken)
	}

	var list passkeysJSON
	c.getJSON(passkeysAPI, &list)
	if len(list.Passkeys) != 1 || list.Passkeys[0].ID != fakePasskeyID || !list.Passkeys[0].Created.Equal(fakeCreated) {
		t.Errorf("list = %+v", list)
	}

	wantStatus(t, c.postJSON(passkeysAPI+"/"+fakePasskeyID+"/remove", passkeyTokenJSON{Token: fakeToken}), http.StatusOK)
	calls := e.passkeys.all()
	if last := calls[len(calls)-1]; last.method != "Remove" || last.id != fakePasskeyID || last.token != fakeToken {
		t.Errorf("remove got %+v, want the path's passkey and the body's token", last)
	}
}

func TestPasskeyBodiesNameNoUserAndNoRepository(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	for _, path := range []string{"/register", "/register/finish", "/step-up", "/step-up/finish", "/" + fakePasskeyID + "/remove"} {
		res := c.postJSON(passkeysAPI+path, map[string]any{"user_id": 1, "repo": otherRepo})
		wantStatus(t, res, http.StatusUnprocessableEntity)
	}
	if calls := e.passkeys.all(); len(calls) != 0 {
		t.Errorf("a refused body reached control/passkey: %+v", calls)
	}
}

func TestPasskeyErrorsAreWorded(t *testing.T) {
	for _, tc := range []struct {
		err      error
		status   int
		sentence string
	}{
		{passkey.ErrNotOwner, http.StatusForbidden, "Only an owner named in the policy"},
		{passkey.ErrNeedsPasskey, http.StatusConflict, "a new passkey needs an existing one"},
		{passkey.ErrNoPasskey, http.StatusConflict, "Register a passkey first."},
		{passkey.ErrCeremony, http.StatusBadRequest, "The passkey check failed"},
		{passkey.ErrCounter, http.StatusForbidden, "may have been copied"},
		{passkey.ErrToken, http.StatusForbidden, "Confirm again."},
		{passkey.ErrTooMany, http.StatusConflict, "You have 10 passkeys"},
		{passkey.ErrUnknown, http.StatusNotFound, "Not found."},
		{passkey.ErrInvalid, http.StatusUnprocessableEntity, "The request is not valid."},
		{passkey.ErrPolicy, http.StatusConflict, "Fix .archfit.yaml first."},
		{errors.New("github down"), http.StatusBadGateway, "Nothing was changed."},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			e := newEnv(t)
			e.passkeys.err = tc.err
			res := e.as("alice", permRead).postJSON(passkeysAPI+"/step-up/finish", passkeyCredentialJSON{Credential: json.RawMessage(`{}`)})
			wantStatus(t, res, tc.status)
			var body errorJSON
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || !strings.Contains(body.Error, tc.sentence) {
				t.Errorf("body = %s, want %q", res.Body.String(), tc.sentence)
			}
			if strings.Contains(res.Body.String(), "passkey: ") {
				t.Errorf("an error's own text reached the answer: %s", res.Body.String())
			}
		})
	}
}
