package ui

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/control/passkey"
	"github.com/coupling-dev/archfit-app/internal/control/ui/session"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

type confirmationOwners struct{}

func (confirmationOwners) Owners(context.Context, api.Repo) ([]string, bool, error) {
	return []string{"alice"}, true, nil
}

type confirmationIssues struct{}

func (confirmationIssues) Open(context.Context, api.RepoRef, string, string, []string) error {
	return nil
}

func TestConfirmationRealHTTPBindsAuthenticatedBrowser(t *testing.T) {
	e := newEnv(t)
	clock := func() time.Time { return e.now }
	keys, err := passkey.New(passkey.Config{BaseURL: baseURL}, e.store, confirmationOwners{}, confirmationIssues{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.New(session.Config{CookieKey: cookieKey, BaseURL: baseURL}, e.oauth, e.gh, e.gh, clock)
	if err != nil {
		t.Fatal(err)
	}
	e.handler = newHandler(Deps{Account: e.acct, Admissions: e.admit, Passkeys: keys, Confirmations: passkey.NewOperatorConfirmations(e.store, clock), Operators: []int64{operatorID}, Now: clock, BaseURL: baseURL}, github{files: e.gh, pulls: e.gh, users: e.gh, installs: e.gh}, sessions, testWeb)
	owner := e.as("alice", permAdmin)
	wantStatus(t, owner.postJSON(passkeysAPI+"/register", passkeyTokenJSON{}), http.StatusForbidden)
	requested := owner.postJSON(passkeysAPI+"/confirmation", struct{}{})
	wantStatus(t, requested, http.StatusOK)
	pending := decodeAs[confirmationAttemptJSON](t, requested)
	if pending.Installation != installA || pending.Attempt == "" {
		t.Fatal("missing pending metadata")
	}
	body := confirmationRequestBody{Installation: pending.Installation, Attempt: pending.Attempt}
	operator := e.signIn(operatorLogin)
	issued := operator.postJSON("/api/admin/confirmations/issue", body)
	wantStatus(t, issued, http.StatusOK)
	token := decodeAs[confirmationIssueJSON](t, issued).Token
	other := e.signIn("alice")
	in := passkeyTokenJSON{Attempt: pending.Attempt, Confirmation: token}
	wantStatus(t, other.postJSON(passkeysAPI+"/register", in), http.StatusForbidden)
	first := owner.postJSON(passkeysAPI+"/register", in)
	wantStatus(t, first, http.StatusOK)
	options := decodeAs[passkeyOptionsJSON](t, first)
	if len(options.Options) == 0 {
		t.Fatal("verification didn't return WebAuthn prompt options")
	}
	wantStatus(t, owner.postJSON(passkeysAPI+"/register", in), http.StatusForbidden)
}
