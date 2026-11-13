package onboarding

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestPolicyPushBypassIsRequiredNotRemoved(t *testing.T) {
	for _, tt := range []struct{ name, canBypass, want string }{
		{"allowed", "always", "ok"}, {"missing", "never", "warning"}, {"PR-only bypass", "pull_requests_only", "warning"}, {"unreadable", "", "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			path := rule("file_path_restriction")
			path.Parameters.Paths = []string{".archfit.yaml"}
			h.sets = []api.Ruleset{{ID: 2, Name: "policy", Target: "push", Enforcement: "active", CanBypass: tt.canBypass, Rules: []api.RepositoryRule{path}}}
			fs := findingsFor(h, setupRepo)
			if fs["policy_push_bypass"].State != tt.want {
				t.Fatalf("push bypass = %+v, want %q", fs["policy_push_bypass"], tt.want)
			}
			if _, ok := fs["app_bypass"]; ok {
				t.Fatalf("push rule must not advise removing the App bypass: %+v", fs["app_bypass"])
			}
		})
	}
}

func TestAdministratorEmergencyBypassMustCoverEveryBranchRuleset(t *testing.T) {
	admin := []api.BypassActor{{ID: 5, Type: "RepositoryRole", Mode: "always"}}
	for _, tt := range []struct {
		name   string
		second *[]api.BypassActor
		want   string
	}{
		{"both allow", &admin, "ok"}, {"only one allows", &[]api.BypassActor{}, "warning"}, {"one unreadable", nil, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			pull, check := rule("pull_request"), rule("required_status_checks")
			check.RulesetID = 2
			h.branch = []api.RepositoryRule{pull, check}
			h.sets = []api.Ruleset{
				{ID: 1, Target: "branch", Enforcement: "active", CanBypass: "never", BypassActors: &admin},
				{ID: 2, Target: "branch", Enforcement: "active", CanBypass: "never", BypassActors: tt.second},
			}
			f := findingsFor(h, setupRepo)["administrator_bypass"]
			if f.State != tt.want {
				t.Fatalf("administrator bypass = %+v, want %q", f, tt.want)
			}
		})
	}
}
