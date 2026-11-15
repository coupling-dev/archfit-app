package ui

import (
	"net/http"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestFirstApprovalReadsAdminAuthorityAgainForEachAction(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	wantStatus(t, c.get(setupAPI), http.StatusOK)
	e.grant("alice", repoName, permWrite)
	wantStatus(t, c.postJSON(ownerAPI+"first", map[string]any{"token": "tok", "digest": "seen"}), http.StatusForbidden)
	if len(e.owners.users) != 0 {
		t.Fatal("cached administrator authority approved a policy")
	}
	e.grant("alice", repoName, permAdmin)
	wantStatus(t, c.postJSON(ownerAPI+"first", map[string]any{"token": "tok", "digest": "seen"}), http.StatusOK)
	e.grant("alice", repoName, permRead)
	wantStatus(t, c.postJSON(ownerAPI+"first", map[string]any{"token": "tok", "digest": "seen"}), http.StatusForbidden)
	if len(e.owners.users) != 1 {
		t.Fatalf("actions = %+v", e.owners.users)
	}
}

func TestFirstApprovalRefusesWhenFreshAuthorityIsUnreadable(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	wantStatus(t, c.get(setupAPI), http.StatusOK)
	e.gh.fail["Permission"] = api.ErrRateLimited
	wantStatus(t, c.postJSON(ownerAPI+"first", map[string]any{"token": "tok", "digest": "seen"}), http.StatusServiceUnavailable)
	if len(e.owners.users) != 0 {
		t.Fatal("approved despite unreadable permissions")
	}
}
