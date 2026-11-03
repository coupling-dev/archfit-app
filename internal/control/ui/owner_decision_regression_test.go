package ui

import (
	"net/http"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
)

func TestNonOverrideOwnerActionsRejectObjectionIDs(t *testing.T) {
	for _, action := range []string{"approve", "request-changes", "withdraw"} {
		t.Run(action, func(t *testing.T) {
			e := newEnv(t)
			e.sameRepoHead()
			res := e.as("alice", permWrite).postJSON(pullAPI+"/"+action, approveJSON{
				HeadSHA: sha(4), BaseSHA: sha(10), Digests: shownBaseline.Digests,
				Reason: "A reason.", ObjectionIDs: []string{"app:unexpected"},
			})
			wantStatus(t, res, http.StatusUnprocessableEntity)
			if len(e.approvals.decisions) != 0 || len(e.approvals.reevaluated) != 0 {
				t.Fatal("invalid action reached the decision store or refresh")
			}
		})
	}
}

func TestGrantedApprovalDoesNotHideAnEligibleOwnersDecisionAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, permission string
		owner, want      bool
	}{
		{"owner with write access", permWrite, true, true},
		{"owner with read access", permRead, true, false},
		{"writer who is not an owner", permWrite, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.sameRepoHead()
			e.approvals.approval = gate.PullApproval{Candidate: shownBaseline, Granted: true, ApprovedBy: "bob"}
			if !tc.owner {
				e.approvals.approval.Refusal = gate.ErrNotOwner
			}
			res := e.as("alice", tc.permission).get(pullAPI)
			wantStatus(t, res, http.StatusOK)
			body := decodeAs[map[string]any](t, res)
			a := body["approval"].(map[string]any)
			if a["can_decide"] != tc.want {
				t.Fatalf("can_decide=%v, want %v", a["can_decide"], tc.want)
			}
			if a["can_approve"] != false || a["state"] != "granted" {
				t.Fatalf("changed effective approval state: %v", a)
			}
		})
	}
}
