package ui

import (
	"testing"

	"github.com/coupling-dev/archfit-app/internal/control/onboarding"
)

func TestRepoSetupStage(t *testing.T) {
	tests := []struct {
		name  string
		setup onboarding.Setup
		stage string
		href  string
	}{
		{"empty", onboarding.Setup{}, "not_started", "/r/acme/app"},
		{"workflow only", onboarding.Setup{Workflow: true}, "not_started", "/r/acme/app"},
		{"policy needs workflow", onboarding.Setup{Policy: true}, "not_started", "/r/acme/app"},
		{"first PR without default policy", onboarding.Setup{State: "first_approval", FirstPull: 8}, "first_approval", "/r/acme/app/pull/8"},
		{"default first approval", onboarding.Setup{State: "first_approval", Policy: true, Workflow: true}, "first_approval", "/r/acme/app"},
		{"first approval needs workflow", onboarding.Setup{State: "first_approval", Policy: true}, "first_approval", "/r/acme/app"},
		{"workflow PR before first approval", onboarding.Setup{Policy: true, OpenPull: 203, State: "first_approval"}, "workflow_review", "/r/acme/app/pull/203"},
		{"workflow PR with stored approval", onboarding.Setup{Policy: true, OpenPull: 203, State: "not_admitted"}, "workflow_review", "/r/acme/app/pull/203"},
		{"approved", onboarding.Setup{Policy: true, Workflow: true, State: "approved"}, "policy_approved", ""},
		{"approved ignores obsolete open pull", onboarding.Setup{Policy: true, Workflow: true, State: "approved", OpenPull: 203}, "policy_approved", ""},
		{"approval without policy", onboarding.Setup{Workflow: true, State: "approved"}, "", ""},
		{"approval without workflow", onboarding.Setup{Policy: true, State: "approved"}, "", ""},
		{"outside flow before PR", onboarding.Setup{Policy: true, State: "outside_flow", OpenPull: 203, FirstPull: 8}, "", ""},
		{"future state", onboarding.Setup{Policy: true, Workflow: true, State: "new"}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := uncheckedOverviewRow("acme/app", "unknown")
			applyOverviewSetup(&row, tt.setup)
			if row.SetupStage != tt.stage || row.NextStep.Href != tt.href {
				t.Fatalf("row = %+v; want stage %q, href %q", row, tt.stage, tt.href)
			}
			if row.State != "unknown" {
				t.Fatalf("stored evidence was overwritten: %+v", row)
			}
		})
	}
}

func TestRepoEnforcementMatrix(t *testing.T) {
	// "" is an absent finding, as production sends it after a successful read of that rule.
	states := []string{"", "ok", "warning", "unknown", "not_applicable", "future"}
	for _, gate := range states {
		for _, pulls := range states {
			for _, rulesets := range states {
				for _, branch := range states {
					name := "gate=" + gate + "/pulls=" + pulls + "/rulesets=" + rulesets + "/branch=" + branch
					t.Run(name, func(t *testing.T) {
						report := onboarding.RulesetReport{Findings: rulesFinding("gate_check", gate, "pull_requests", pulls, "rulesets", rulesets, "branch_rules", branch)}
						want := "check"
						switch {
						case gate == "ok" && pulls == "ok" && readableRules(rulesets) && readableRules(branch):
							want = "required"
						case gate == "warning" || pulls == "warning":
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
	}
}

// readableRules is Home's rule: an absent finding is readable, and any other state except ok is not.
func readableRules(state string) bool { return state == "" || state == "ok" }

// rulesFinding builds the findings a report holds: a code whose state is "" is absent, as a successful read leaves it.
func rulesFinding(pairs ...string) []onboarding.RulesetFinding {
	var out []onboarding.RulesetFinding
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			out = append(out, onboarding.RulesetFinding{Code: pairs[i], State: pairs[i+1]})
		}
	}
	return out
}

// Production sends no rulesets or branch_rules finding after a successful read; Home calls that repository done.
func TestRepoEnforcementReadsAbsentRulesFindingsAsReadable(t *testing.T) {
	report := onboarding.RulesetReport{Findings: rulesFinding("gate_check", "ok", "pull_requests", "ok")}
	if got, _ := repoEnforcement(report); got != "required" {
		t.Fatalf("enforcement = %q, want required", got)
	}
}

// pumba: gate on, pull requests off. Home step 4 is not done, so the list must not say required.
func TestRepoEnforcementDoesNotCallGateOnlyRequired(t *testing.T) {
	report := onboarding.RulesetReport{Findings: rulesFinding("gate_check", "ok", "pull_requests", "warning")}
	if got, _ := repoEnforcement(report); got != "not_required" {
		t.Fatalf("enforcement = %q, want not_required", got)
	}
}
