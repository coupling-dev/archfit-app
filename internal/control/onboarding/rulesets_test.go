package onboarding

import (
	"context"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func rule(kind string) api.RepositoryRule { return api.RepositoryRule{Type: kind, RulesetID: 1} }

func findingsFor(h *setupHub, r api.Repo) map[string]RulesetFinding {
	out := map[string]RulesetFinding{}
	for _, f := range (&setups{gh: h}).rules(context.Background(), r).Findings {
		out[f.Code] = f
	}
	return out
}

func TestRulesetBranchRequirements(t *testing.T) {
	app, other := int64(42), int64(99)
	tests := []struct {
		name                              string
		pulls                             bool
		approvals                         int
		check                             string
		integration                       *int64
		wantPull, wantGate, wantApprovals string
	}{
		{"missing", false, 0, "", nil, "warning", "warning", ""},
		{"pull requests only", true, 0, "archfit/gate", &app, "ok", "ok", ""},
		{"reviews are separate", true, 1, "archfit/gate", &app, "ok", "ok", "warning"},
		{"wrong producer", true, 0, "archfit/gate", &other, "ok", "warning", ""},
		{"any producer", true, 0, "archfit/gate", nil, "ok", "warning", ""},
		{"wrong context", true, 0, "archfit/other", &app, "ok", "warning", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			if tt.pulls {
				r := rule("pull_request")
				r.Parameters.Approvals = tt.approvals
				h.branch = append(h.branch, r)
			}
			if tt.check != "" {
				r := rule("required_status_checks")
				r.Parameters.Checks = []api.RequiredCheck{{Context: tt.check, IntegrationID: tt.integration}}
				h.branch = append(h.branch, r)
			}
			fs := findingsFor(h, setupRepo)
			for code, want := range map[string]string{"pull_requests": tt.wantPull, "gate_check": tt.wantGate, "approvals": tt.wantApprovals} {
				if fs[code].State != want {
					t.Errorf("%s = %+v, want %q", code, fs[code], want)
				}
			}
		})
	}
}

func TestRulesetBypassReadBack(t *testing.T) {
	tests := []struct {
		name, canBypass           string
		actors                    *[]api.BypassActor
		wantApp, wantAdmin, words string
	}{
		{"app cannot bypass", "never", &[]api.BypassActor{}, "", "warning", "No administrator bypass"},
		{"app always bypasses", "always", &[]api.BypassActor{}, "warning", "warning", "No administrator bypass"},
		{"app bypasses on pulls", "pull_requests_only", &[]api.BypassActor{}, "warning", "warning", "No administrator bypass"},
		{"unknown app bypass", "", &[]api.BypassActor{}, "unknown", "warning", "No administrator bypass"},
		{"unreadable administrator", "never", nil, "", "unknown", "if the ruleset lists one"},
		{"repository administrator", "never", &[]api.BypassActor{{ID: 5, Type: "RepositoryRole", Mode: "always"}}, "", "ok", "emergency path"},
		{"organization administrator", "never", &[]api.BypassActor{{Type: "OrganizationAdmin", Mode: "always"}}, "", "ok", "emergency path"},
		{"review bypass is not emergency", "never", &[]api.BypassActor{{ID: 5, Type: "RepositoryRole", Mode: "pull_request"}}, "", "warning", "No administrator bypass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			h.branch = []api.RepositoryRule{rule("pull_request")}
			h.sets = []api.Ruleset{{ID: 1, Name: "default branch", Target: "branch", Enforcement: "active", CanBypass: tt.canBypass, BypassActors: tt.actors}}
			fs := findingsFor(h, setupRepo)
			if fs["app_bypass"].State != tt.wantApp || fs["administrator_bypass"].State != tt.wantAdmin || !strings.Contains(fs["administrator_bypass"].Text, tt.words) {
				t.Fatalf("findings = %+v", fs)
			}
		})
	}
}

func TestPolicyPushRuleOnlyAppliesToSupportedRepositories(t *testing.T) {
	tests := []struct{ name, owner, visibility, path, enforcement, want string }{
		{"private organization", "Organization", "private", ".archfit.yaml", "active", "ok"},
		{"internal organization", "Organization", "internal", ".archfit.yaml", "active", "ok"},
		{"public organization", "Organization", "public", ".archfit.yaml", "active", "not_applicable"},
		{"personal private", "User", "private", ".archfit.yaml", "active", "not_applicable"},
		{"personal public", "User", "public", "", "", "not_applicable"},
		{"missing push restriction", "Organization", "private", "", "", "warning"},
		{"wrong file", "Organization", "private", "README.md", "active", "warning"},
		{"disabled", "Organization", "private", ".archfit.yaml", "disabled", "warning"},
		{"evaluate only", "Organization", "private", ".archfit.yaml", "evaluate", "warning"},
		{"all files", "Organization", "private", "**", "active", "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			if tt.path != "" {
				r := rule("file_path_restriction")
				r.Parameters.Paths = []string{tt.path}
				h.sets = []api.Ruleset{{ID: 2, Target: "push", Enforcement: tt.enforcement, CanBypass: "never", Rules: []api.RepositoryRule{r}}}
			}
			repo := setupRepo
			repo.OwnerType = tt.owner
			repo.Visibility = tt.visibility
			f := findingsFor(h, repo)["policy_push"]
			if f.State != tt.want {
				t.Fatalf("policy_push = %+v, want %q", f, tt.want)
			}
		})
	}
}

func TestUnreadableRulesAreUnknownNotOK(t *testing.T) {
	h := newSetupHub()
	h.ruleErr = api.ErrRateLimited
	fs := findingsFor(h, setupRepo)
	if fs["branch_rules"].State != "unknown" || len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
}
