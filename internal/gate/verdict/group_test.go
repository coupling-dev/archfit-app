package verdict

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
)

func TestMergeGroupFacts(t *testing.T) {
	group := func(g inputs.Group) func(*Facts) { return func(f *Facts) { f.Trusted.Group = &g } }
	tests := []struct {
		name       string
		facts      Facts
		conclusion Conclusion
		first      ReasonCode
		detail     string
	}{
		{"healthy group", with(group(inputs.Group{})), Success, ReasonHealthy, ""},
		{"a protected file changed by no member", with(group(inputs.Group{Problem: inputs.GroupConflict, Detail: ".archfit.yaml changed by no queued pull request"})),
			ActionRequired, ReasonMergeGroupConflict, ".archfit.yaml changed by no queued pull request"},
		{"a member that changes a protected path", with(group(inputs.Group{Unapproved: "#3 .archfit.yaml"}),
			func(f *Facts) { f.Trusted.ProtectedPaths = []string{".archfit.yaml"} }), ActionRequired, ReasonOwnerApprovalMissing, "#3 .archfit.yaml"},
		{"fork member", with(group(inputs.Group{Problem: inputs.GroupFork, Detail: "#1"})), ActionRequired, ReasonForkUnsupported, ""},
		{"unverified group", with(group(inputs.Group{Problem: inputs.GroupUnverified, Detail: "#2 is not the queued pull request"})),
			ActionRequired, ReasonMergeGroupUnverified, "#2 is not the queued pull request"},
		{"unknown problem fails closed", with(group(inputs.Group{Problem: "skipped"})), ActionRequired, ReasonMergeGroupUnverified, ""},
		{"unverified without settings", with(group(inputs.Group{Problem: inputs.GroupUnverified}), settingsMissing),
			ActionRequired, ReasonSettingsMissing, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := decide(t, tt.facts)
			if o.Conclusion != tt.conclusion || o.Reasons[0].Code != tt.first {
				t.Fatalf("got %s %v, want %s first %s", o.Conclusion, codes(o), tt.conclusion, tt.first)
			}
			if r, _ := find(o, tt.first); r.Detail != tt.detail {
				t.Errorf("detail %q, want %q", r.Detail, tt.detail)
			}
		})
	}
	if o := Without(ReasonMergeGroupUnverified); o.Conclusion != ActionRequired || o.Reasons[0].Code != ReasonMergeGroupUnverified {
		t.Errorf("Without(merge_group_unverified) = %v", codes(o))
	}
}
