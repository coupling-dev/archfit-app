package ui

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
)

func TestOwnerDecisionEndpointsBindCandidateRequireReasonsAndRefresh(t *testing.T) {
	seen := shownBaseline.Digests
	tests := []struct {
		name       string
		action     string
		kind       gate.DecisionAction
		reason     string
		objections []string
	}{
		{"approve", "approve", gate.DecisionApprove, "", nil},
		{"request changes", "request-changes", gate.DecisionRequestChanges, "Compatibility needs review.", nil},
		{"withdraw", "withdraw", gate.DecisionWithdraw, "I am revisiting this approval.", nil},
		{"override native objection", "override", gate.DecisionOverride, "I accept the remaining GitHub requirement.", []string{"github:77"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.sameRepoHead()
			e.approvals.approval = gate.PullApproval{Candidate: shownBaseline}
			body := approveJSON{HeadSHA: sha(4), BaseSHA: sha(10), Digests: seen, Reason: tc.reason, ObjectionIDs: tc.objections}
			res := e.as("alice", permWrite).postJSON(pullAPI+"/"+tc.action, body)
			wantStatus(t, res, http.StatusOK)
			if len(e.approvals.decisions) != 1 || len(e.approvals.reevaluated) != 1 {
				t.Fatalf("decisions=%+v reevaluations=%+v", e.approvals.decisions, e.approvals.reevaluated)
			}
			got := e.approvals.decisions[0]
			if got.action != tc.kind || got.login != "alice" || got.target.BaseSHA != sha(10) || got.target.HeadSHA != sha(4) ||
				got.reason != tc.reason || !maps.Equal(got.seen, seen) || !slices.Equal(got.overrideIDs, tc.objections) {
				t.Errorf("decision call = %+v", got)
			}
			if e.approvals.reevaluated[0].BaseSHA != sha(10) {
				t.Errorf("Reevaluate target = %+v", e.approvals.reevaluated[0])
			}
		})
	}

	t.Run("required reason is rejected before persistence", func(t *testing.T) {
		e := newEnv(t)
		e.sameRepoHead()
		res := e.as("alice", permWrite).postJSON(pullAPI+"/request-changes", approveJSON{
			HeadSHA: sha(4), BaseSHA: sha(10), Digests: seen,
		})
		wantStatus(t, res, http.StatusUnprocessableEntity)
		wantContains(t, res.Body.String(), "Enter a reason")
		if len(e.approvals.decisions) != 0 || len(e.approvals.reevaluated) != 0 {
			t.Fatal("reasonless request reached the gate")
		}
	})

	t.Run("stale protected source is rejected", func(t *testing.T) {
		e := newEnv(t)
		e.sameRepoHead()
		res := e.as("alice", permWrite).postJSON(approveAPI, approveJSON{HeadSHA: sha(4), BaseSHA: sha(9), Digests: seen})
		wantStatus(t, res, http.StatusConflict)
		wantContains(t, res.Body.String(), "protected settings commit changed")
		e.noWrites(t)
	})
}
