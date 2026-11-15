package ui

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
)

func TestRepoSetupStage(t *testing.T) {
	tests := []struct {
		name      string
		setup     onboarding.Setup
		stage     string
		href      string
		attention string
		reason    bool
	}{
		{"empty", onboarding.Setup{}, "not_started", "/r/acme/app", "", false},
		{"workflow only", onboarding.Setup{Workflow: true}, "not_started", "/r/acme/app", "", false},
		{"policy needs workflow", onboarding.Setup{Policy: true}, "not_started", "/r/acme/app", "", true},
		{"first PR without default policy", onboarding.Setup{State: "first_approval", FirstPull: 8}, "first_approval", "/r/acme/app/owner/first?pull=8", "PR #8", false},
		{"default first approval", onboarding.Setup{State: "first_approval", Policy: true, Workflow: true}, "first_approval", "/r/acme/app/owner/first", "First approval", false},
		{"first approval needs workflow", onboarding.Setup{State: "first_approval", Policy: true}, "first_approval", "/r/acme/app", "", false},
		{"workflow PR before first approval", onboarding.Setup{Policy: true, OpenPull: 203, State: "first_approval"}, "workflow_review", "/r/acme/app/pull/203", "PR #203", false},
		{"workflow PR with stored approval", onboarding.Setup{Policy: true, OpenPull: 203, State: "not_admitted"}, "workflow_review", "/r/acme/app/pull/203", "PR #203", false},
		{"approved", onboarding.Setup{Policy: true, Workflow: true, State: "approved"}, "policy_approved", "/r/acme/app", "", false},
		{"approved ignores obsolete open pull", onboarding.Setup{Policy: true, Workflow: true, State: "approved", OpenPull: 203}, "policy_approved", "/r/acme/app", "", false},
		{"approval without policy", onboarding.Setup{Workflow: true, State: "approved"}, "", "/r/acme/app", "", true},
		{"approval without workflow", onboarding.Setup{Policy: true, State: "approved"}, "", "/r/acme/app", "", true},
		{"outside flow before PR", onboarding.Setup{Policy: true, State: "outside_flow", OpenPull: 203, FirstPull: 8}, "", "/r/acme/app", "", true},
		{"future state", onboarding.Setup{Policy: true, Workflow: true, State: "new"}, "", "/r/acme/app", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := uncheckedOverviewRow("acme/app", "unknown", "Not checked.")
			applyOverviewSetup(&row, tt.setup)
			if row.SetupStage != tt.stage || row.NextStep.Href != tt.href || row.Attention != tt.attention || (row.SetupReason != "") != tt.reason {
				t.Fatalf("row = %+v; want stage %q, href %q, attention %q, reason %t", row, tt.stage, tt.href, tt.attention, tt.reason)
			}
			if row.State != "unknown" {
				t.Fatalf("stored evidence was overwritten: %+v", row)
			}
		})
	}
}

func TestRepoEnforcementMatrix(t *testing.T) {
	states := []string{"", "ok", "warning", "unknown", "not_applicable", "future"}
	for _, gate := range states {
		for _, pulls := range states {
			t.Run("gate="+gate+"/pulls="+pulls, func(t *testing.T) {
				report := onboarding.RulesetReport{}
				if gate != "" {
					report.Findings = append(report.Findings, onboarding.RulesetFinding{Code: "gate_check", State: gate})
				}
				if pulls != "" {
					report.Findings = append(report.Findings, onboarding.RulesetFinding{Code: "pull_requests", State: pulls})
				}
				report.Findings = append(report.Findings, onboarding.RulesetFinding{Code: "policy_push", State: "not_applicable"})
				want := "check"
				if gate == "ok" {
					want = "required"
				} else if gate == "warning" && pulls == "ok" {
					want = "not_required"
				}
				got, reason := repoEnforcement(report)
				if got != want || (reason != "") != (want == "check") {
					t.Fatalf("enforcement = %q, %q; want %q", got, reason, want)
				}
			})
		}
	}
}
