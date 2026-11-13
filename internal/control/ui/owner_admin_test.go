package ui

import (
	"net/http"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
)

func TestFirstApprovalRequiresAuthenticatedRepositoryAdministrator(t *testing.T) {
	for _, permission := range []string{permRead, permWrite, permAdmin} {
		t.Run(permission, func(t *testing.T) {
			e := newEnv(t)
			res := e.as("alice", permission).postJSON(ownerAPI+"first", map[string]any{"digest": "seen", "token": "tok"})
			if permission != permAdmin {
				wantStatus(t, res, http.StatusForbidden)
				if len(e.owners.users) != 0 {
					t.Fatal("a non-administrator's approval reached the gate")
				}
				return
			}
			wantStatus(t, res, http.StatusOK)
			if len(e.owners.users) != 1 || !e.owners.users[0].Admin {
				t.Fatalf("gate user = %+v", e.owners.users)
			}
		})
	}
}

func TestAdministratorCannotClaimToBeAnApprovedPolicyOwner(t *testing.T) {
	for _, kind := range []string{gate.OwnerWorkflow, gate.OwnerConfirm, gate.OwnerRevert} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			e.owners.err = gate.ErrOwnerNotOwner
			body := map[string]any{"digest": "seen", "token": "tok"}
			if kind == gate.OwnerWorkflow {
				body["pull_request"] = 12
			}
			wantStatus(t, e.as("mallory", permAdmin).postJSON(ownerAPI+kind, body), http.StatusForbidden)
			if len(e.owners.users) != 1 || e.owners.users[0].Login != "mallory" || e.owners.users[0].Admin {
				t.Fatalf("unexpected authority: %+v", e.owners.users)
			}
		})
	}
}
