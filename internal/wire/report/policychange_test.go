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
		{"an empty direction", edit("widen", func(d map[string]any) { d["direction"] = "" }), "policy_change.direction"},
		{"an empty coverage kind", edit("exclude", func(d map[string]any) {
			d["coverage_lost"].([]any)[0].(map[string]any)["kind"] = ""
		}), "policy_change.coverage_lost[0].kind"},
		{"a rule field the engine never writes", edit("widen", func(d map[string]any) {
			d["direction"] = "loosening"
			d["rules_loosened"] = []any{map[string]any{"id": "r", "field": "selector", "from": "a", "to": "b"}}
		}), "policy_change.rules_loosened[0].field"},
		{"a control character in engine text", edit("widen", func(d map[string]any) {
			d["declarations_changed"] = []any{map[string]any{"key": "layers", "from": "a\x00b", "to": "c"}}
		}), "policy_change.declarations_changed[0].from"},
		{"engine text over the cap", edit("widen", func(d map[string]any) {
			d["declarations_changed"] = []any{map[string]any{"key": "layers", "from": strings.Repeat("x", 9000), "to": "c"}}
		}), "policy_change.declarations_changed[0].from"},
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

// The shapes the engine writes for an ordinary change decode: a moved selector is a "declaration" change whose from and
// to are the whole rule's canonical JSON, possibly several lines for a duplicated rule ID; a metric ratchet changes its
// max_new or min_delta; a changed top-level section is a declaration with its canonical JSON.
func TestPolicyChangeEngineShapes(t *testing.T) {
	big := `{"layers":[` + strings.Repeat(`{"name":"core","paths":["internal/core/**","pkg/core/**"]},`, 40) + `{"name":"edge"}]}`
	doc := policyChangeDoc(t, "widen")
	doc["rules_loosened"] = []any{
		map[string]any{"id": "no_db", "field": "declaration", "from": "fail  {\"type\":\"forbidden_dependency\"}\nfail  {\"type\":\"forbidden_dependency\",\"guard\":true}", "to": "fail  {}"},
		map[string]any{"id": "metric/coupling", "field": "max_new", "from": "0", "to": "3"},
		map[string]any{"id": "metric/churn", "field": "min_delta", "from": "0", "to": "0.5"},
		map[string]any{"id": "no_db", "field": "gate", "from": "fail", "to": "warn"},
		map[string]any{"id": "size", "field": "max", "from": "10", "to": "20"},
	}
	doc["declarations_changed"] = []any{map[string]any{"key": "layers", "from": big, "to": big + " "}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(big) <= maxTextLen {
		t.Fatal("the section must be longer than ordinary text to test the cap")
	}
	p, err := DecodePolicyChange(raw)
	if err != nil {
		t.Fatalf("DecodePolicyChange: %v", err)
	}
	if p.Direction != DirectionLoosening || len(p.RulesLoosened) != 5 || p.DeclarationsChanged[0].From != big {
		t.Errorf("got %+v", p)
	}
}
