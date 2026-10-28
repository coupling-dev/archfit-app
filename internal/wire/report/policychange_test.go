package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func policyChangeDoc(t *testing.T, name string) map[string]any {
	t.Helper()
	return parseDoc(t, loadFixture(t, "engine-next/policy-change-"+name+".json"))
}

// The engine's own output for the three cases the plan names decodes to the classification it states.
func TestEnginePolicyChangesDecode(t *testing.T) {
	tests := []struct {
		name                 string
		direction            string
		loosens, tightens    int
		widened, added, lost int
	}{
		{"widen", DirectionLoosening, 1, 0, 1, 0, 0},
		{"addrule", DirectionTightening, 0, 1, 0, 1, 0},
		{"exclude", DirectionLoosening, 1, 0, 0, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := DecodePolicyChange(loadFixture(t, "engine-next/policy-change-"+tt.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if p.Direction != tt.direction || p.Loosens() != tt.loosens || p.Tightens() != tt.tightens ||
				len(p.AllowlistsWidened) != tt.widened || len(p.RulesAdded) != tt.added || len(p.CoverageLost) != tt.lost {
				t.Errorf("got %+v", p)
			}
		})
	}
}

func TestPolicyChangeRules(t *testing.T) {
	edit := func(name string, f func(d map[string]any)) []byte {
		d := policyChangeDoc(t, name)
		f(d)
		out, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	tests := []struct {
		name    string
		raw     []byte
		wantLoc string
	}{
		// The direction decides who may approve, so the App derives it and refuses a document that states another.
		{"a loosening stated as a tightening", edit("widen", func(d map[string]any) { d["direction"] = "tightening" }), "policy_change.direction"},
		{"a loosening stated as none", edit("exclude", func(d map[string]any) { d["direction"] = "none" }), "policy_change.direction"},
		{"a tightening stated as mixed", edit("addrule", func(d map[string]any) { d["direction"] = "mixed" }), "policy_change.direction"},
		{"an unknown direction", edit("widen", func(d map[string]any) { d["direction"] = "neutral" }), "policy_change.direction"},
		{"a missing list", edit("widen", func(d map[string]any) { delete(d, "coverage_lost") }), "policy_change.coverage_lost"},
		{"a null list", edit("widen", func(d map[string]any) { d["rules_removed"] = nil }), "policy_change.rules_removed"},
		{"an unknown key", edit("widen", func(d map[string]any) { d["approved"] = true }), "policy_change"},
		{"an unknown coverage kind", edit("exclude", func(d map[string]any) {
			d["coverage_lost"].([]any)[0].(map[string]any)["kind"] = "everything_excluded"
		}), "policy_change.coverage_lost[0].kind"},
		{"a gained kind under lost", edit("exclude", func(d map[string]any) {
			d["coverage_lost"].([]any)[0].(map[string]any)["kind"] = "exclude_removed"
		}), "policy_change.coverage_lost[0].kind"},
		{"an unknown allowlist field", edit("widen", func(d map[string]any) {
			d["allowlists_widened"].([]any)[0].(map[string]any)["field"] = "imports"
		}), "policy_change.allowlists_widened[0].field"},
		{"an allowlist without a module", edit("widen", func(d map[string]any) {
			delete(d["allowlists_widened"].([]any)[0].(map[string]any), "module")
		}), "policy_change.allowlists_widened[0].module"},
		{"a multi-line detail", edit("exclude", func(d map[string]any) {
			d["coverage_lost"].([]any)[0].(map[string]any)["detail"] = "pkg/**\nrm -rf /"
		}), "policy_change.coverage_lost[0].detail"},
		{"a rule id that is not a string", edit("addrule", func(d map[string]any) {
			d["rules_added"].([]any)[0].(map[string]any)["id"] = 3
		}), "policy_change.rules_added[0].id"},
		{"not an object", []byte(`[]`), "policy_change"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodePolicyChange(tt.raw)
			wantError(t, err, tt.wantLoc)
			for _, leak := range []string{"everything_excluded", "rm -rf", "neutral", "imports"} {
				if err != nil && strings.Contains(err.Error(), leak) {
					t.Errorf("error echoes the value %q: %v", leak, err)
				}
			}
		})
	}
}
