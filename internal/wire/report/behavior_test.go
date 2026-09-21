package report

import (
	"reflect"
	"slices"
	"testing"
)

func TestBlockerKeepsUnevaluatedRules(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "report/blocker-plus-unevaluated.json"))
	if r.Decision.HardGates != "fail" || r.Decision.ActiveBlockers != 1 {
		t.Errorf("decision %+v, want failing hard gates with one blocker", r.Decision)
	}
	want := []UnevaluatedRule{{RuleID: "layers/no_upward_imports", Reason: "required tool `dependency-cruiser` was absent"}}
	if !slices.Equal(r.Decision.UnevaluatedRequiredRules, want) {
		t.Errorf("unevaluated rules %+v, want %+v", r.Decision.UnevaluatedRequiredRules, want)
	}
}

func TestUnknownIdentityIsRepresentable(t *testing.T) {
	t.Run("without measurement_profile", func(t *testing.T) {
		r := mustDecode(t, loadFixture(t, "report/unknown-identity.json"))
		if r.Comparison.MeasurementProfile != nil {
			t.Errorf("profile %+v, want nil", r.Comparison.MeasurementProfile)
		}
	})
	t.Run("without gate_reference", func(t *testing.T) {
		doc := baseDoc(t)
		del(t, doc, "gate_reference")
		if r := mustDecode(t, encode(t, doc)); r.GateReference != nil {
			t.Errorf("gate reference %+v, want nil", r.GateReference)
		}
	})
	t.Run("without config_hash", func(t *testing.T) {
		doc := baseDoc(t)
		del(t, doc, "comparison", "config_hash")
		if r := mustDecode(t, encode(t, doc)); r.Comparison.ConfigHash != "" {
			t.Errorf("config hash %q, want empty", r.Comparison.ConfigHash)
		}
	})
}

func TestOriginNeverFilters(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "report/blocked.json"))
	if len(r.Findings) != 2 || r.Findings[0].Kind != "gate" || r.Findings[0].Status != "baseline" {
		t.Errorf("findings %+v, want the blocking finding kept with status baseline", r.Findings)
	}
	if len(r.AgentTasks) != 2 {
		t.Fatalf("%d tasks, want 2", len(r.AgentTasks))
	}
	if task := r.AgentTasks[1]; task.RuleID != "bc/coupling_gate" || task.Origin != "unknown" || task.FindingID != "coupling-gate" {
		t.Errorf("coupling-gate task %+v", task)
	}
}

func TestRepairKindsSurvive(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "report/both-repair-kinds.json"))
	want := []AgentTask{
		{
			FindingID:  "ba3803eca947bf3c1b7efa8f37854d5e",
			RuleID:     "no_direct_b_dependency",
			RepairKind: "code_change",
			Goal: "Remove the forbidden dependency from pkg/a/a.go on pkg/b; move shared behavior to a location " +
				"permitted by the existing dependency rules.",
			Constraints: []string{"Remove the dependency or move the code", `public surface of module "b": [pkg/b/b.go]`},
			Files:       []string{"pkg/a/a.go", "pkg/b"},
			Validation:  []string{"archfit check -c .archfit.yaml"},
			Origin:      "introduced",
		},
		{
			FindingID:   "coupling-gate",
			RuleID:      "bc/coupling_gate",
			RepairKind:  "needs_owner_decision",
			Goal:        "Decide whether the coupling between modules a and b is intended; declare volatility or restructure.",
			Constraints: []string{"Do not baseline the seam without an owner decision"},
			Files:       []string{"pkg/a/**", "pkg/b"},
			Validation:  []string{"archfit check -c .archfit.yaml --base main --lang go --require-tools"},
			Origin:      "pre_existing",
		},
	}
	if !reflect.DeepEqual(r.AgentTasks, want) {
		t.Errorf("tasks\n%+v\nwant\n%+v", r.AgentTasks, want)
	}
}

func TestWaiverStatusesSurvive(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "report/waiver-expiry-and-order.json"))
	var got []string
	for _, f := range r.Findings {
		got = append(got, f.ID+" "+f.Status)
	}
	want := []string{
		"ba3803eca947bf3c1b7efa8f37854d5e waived",
		"c4b1a2e39d8f47a6b5c4d3e2f1a0b9c8 expired_waiver",
		"07af466df8bfd6e77ee485ac01c50a17 new",
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings %q, want %q", got, want)
	}
}
