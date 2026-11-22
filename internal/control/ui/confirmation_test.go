package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/control/passkey"
)

func TestOperatorConfirmationHTTPAuthorization(t *testing.T) {
	e := newEnv(t)
	owner := e.as("alice", permAdmin)
	svc := passkey.NewOperatorConfirmations(e.store, func() time.Time { return e.now })
	id, err := svc.Request(t.Context(), passkey.ConfirmationScope{InstallationID: 1, RepositoryID: 10, UserID: e.user("alice").UserID, SessionBinding: "test-binding"})
	if err != nil {
		t.Fatal(err)
	}
	body := confirmationRequestBody{Installation: 1, Attempt: id}
	for _, action := range []string{"lookup", "issue", "revoke"} {
		wantStatus(t, owner.postJSON("/api/admin/confirmations/"+action, body), http.StatusNotFound)
		wantStatus(t, e.anonymous().postJSON("/api/admin/confirmations/"+action, body), http.StatusUnauthorized)
	}
	ops := e.signIn(operatorLogin)
	wantStatus(t, ops.postJSONWith("/api/admin/confirmations/issue", body, ""), http.StatusForbidden)
	res := ops.postJSON("/api/admin/confirmations/lookup", body)
	wantStatus(t, res, http.StatusOK)
	if strings.Contains(res.Body.String(), "Binding") || strings.Contains(res.Body.String(), "Hash") {
		t.Fatal("private metadata exposed")
	}
	res = ops.postJSON("/api/admin/confirmations/issue", body)
	wantStatus(t, res, http.StatusOK)
	out := decodeAs[confirmationIssueJSON](t, res)
	if len(out.Token) != 22 || res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Content-Encoding") != "" {
		t.Fatal("unsafe issue response")
	}
	lookup := ops.postJSON("/api/admin/confirmations/lookup", body)
	if strings.Contains(lookup.Body.String(), out.Token) {
		t.Fatal("repeat read returned plaintext token")
	}
	wantStatus(t, ops.postJSON("/api/admin/confirmations/revoke", body), http.StatusOK)
	if err = svc.Verify(t.Context(), passkey.ConfirmationScope{InstallationID: 1, RepositoryID: 10, UserID: e.user("alice").UserID, SessionBinding: "test-binding", AttemptID: id}, out.Token); err == nil {
		t.Fatal("revoked token accepted")
	}
}
