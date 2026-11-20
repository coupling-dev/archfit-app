package ui

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/control/answers"
	"github.com/coupling-dev/archfit-app/internal/control/onboarding"
	"github.com/coupling-dev/archfit-app/internal/gate"
)

func TestHomeDoesNotOfferAnotherSignatureWhilePendingOrUnknown(t *testing.T) {
	for _, status := range []string{"approved_pending_merge", "unknown"} {
		t.Run(status, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			e.setups.out.State, e.setups.out.OpenPull = "not_admitted", 203
			e.setups.out.Approval = gate.ApprovalSummary{Status: gate.ApprovalPending, Kind: gate.OwnerWorkflow, Pull: 203, CurrentHead: sha(3), ApprovedHead: sha(3)}
			if status == "unknown" {
				e.setups.out.Approval.Status = gate.ApprovalUnknown
			}
			e.owners.review = func(answers.OwnerAction, gate.OwnerUser) error {
				t.Fatal("attempted to offer a second signature")
				return nil
			}
			response := e.as("alice", permAdmin).get(homeAPI)
			wantStatus(t, response, http.StatusOK)
			var body homeJSON
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Setup == nil || body.Setup.Review != nil || body.Setup.Approval.Status != status {
				t.Fatalf("setup = %+v", body.Setup)
			}
		})
	}
}

func TestHomeKeepsIncompleteRulesAfterApproval(t *testing.T) {
	for _, state := range []string{"approved", "outside_flow"} {
		t.Run(state, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			e.setups.out.State = state
			e.setups.out.Rules = onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{
				{Code: "gate_check", State: "warning", Text: "Require the gate."},
			}}
			response := e.as("alice", permAdmin).get(homeAPI)
			wantStatus(t, response, http.StatusOK)
			var body homeJSON
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Setup == nil {
				t.Fatal("incomplete gate setup disappeared after owner approval")
			}
		})
	}
}
