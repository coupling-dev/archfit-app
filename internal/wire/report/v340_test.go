package report

import (
	"slices"
	"strings"
	"testing"
)

// The keys and values engine v3.3.0 and later emit that v3.2.3 did not, as the pinned v3.4.0 schema defines them:
// the decoder reads them, and refuses malformed ones like any other key.
func TestEngineV340Values(t *testing.T) {
	rows := []mutation{
		{name: "gate reasons", edit: func(t *testing.T, d map[string]any) {
			d["gate_reasons"] = []any{"coupling gate: 2 seams over the strength limit", "coupling gate: drift unmeasured"}
		}},
		{name: "no gate reasons", edit: func(t *testing.T, d map[string]any) { d["gate_reasons"] = []any{} }},
		{name: "same owner unknown", edit: func(t *testing.T, d map[string]any) {
			set(t, d, nil, "seams", 0, "raw_distance", "same_owner")
		}},
		{name: "same deploy unit unknown", edit: func(t *testing.T, d map[string]any) {
			set(t, d, nil, "seams", 0, "raw_distance", "same_deploy_unit")
		}},
		{name: "same deploy unit known", edit: func(t *testing.T, d map[string]any) {
			set(t, d, true, "seams", 0, "raw_distance", "same_deploy_unit")
		}},
		{name: ".NET and supplied-coverage producers", edit: func(t *testing.T, d map[string]any) {
			producers := at(t, d, "comparison", "measurement_profile", "producers").([]any)
			for _, tool := range []string{"dotnet-msbuild", "roslyn", "supplied-coverage"} {
				producers = append(producers, map[string]any{"tool": tool, "semantics_version": tool + ".v1", "status": "ok", "tool_version": "8.0.100"})
			}
			set(t, d, producers, "comparison", "measurement_profile", "producers")
		}},

		{name: "a gate reason for every seam the report may hold", edit: func(t *testing.T, d map[string]any) {
			reasons := make([]any, maxSeams+1)
			for i := range reasons {
				reasons[i] = "new distributed-monolith seam"
			}
			d["gate_reasons"] = reasons
		}},
		{name: "a gate reason longer than ordinary text", edit: func(t *testing.T, d map[string]any) {
			d["gate_reasons"] = []any{"distributed monolith: " + strings.Repeat("pkg_with_a_long_module_id_", 30) + " crosses deploy units"}
		}},
		{name: "more gate reasons than seams", wantLoc: "gate_reasons", edit: func(t *testing.T, d map[string]any) {
			d["gate_reasons"] = make([]any, maxSeams+2)
			for i := range d["gate_reasons"].([]any) {
				d["gate_reasons"].([]any)[i] = "r"
			}
		}},
		{name: "a gate reason that is not a string", wantLoc: "gate_reasons[0]", edit: func(t *testing.T, d map[string]any) {
			d["gate_reasons"] = []any{7}
		}},
		{name: "a gate reason over two lines", wantLoc: "gate_reasons[0]", edit: func(t *testing.T, d map[string]any) {
			d["gate_reasons"] = []any{"one\ntwo"}
		}},
		{name: "gate reasons not a list", wantLoc: "gate_reasons", edit: func(t *testing.T, d map[string]any) { d["gate_reasons"] = "x" }},
		{name: "same owner not a boolean", wantLoc: "seams[0].raw_distance.same_owner", edit: func(t *testing.T, d map[string]any) {
			set(t, d, "yes", "seams", 0, "raw_distance", "same_owner")
		}},
		{name: "same deploy unit not a boolean", wantLoc: "seams[0].raw_distance.same_deploy_unit", edit: func(t *testing.T, d map[string]any) {
			set(t, d, "yes", "seams", 0, "raw_distance", "same_deploy_unit")
		}},
		{name: "a head tree the engine marks dirty", edit: func(t *testing.T, d map[string]any) {
			set(t, d, strings.Repeat("e", 40)+"+dirty", "identity", "head_tree_digest")
		}},
		{name: "a producer without a tool", wantLoc: "producers[12].tool", edit: func(t *testing.T, d map[string]any) {
			producers := at(t, d, "comparison", "measurement_profile", "producers").([]any)
			set(t, d, append(producers, map[string]any{"semantics_version": "roslyn.v1", "status": "ok"}), "comparison", "measurement_profile", "producers")
		}},
		{name: "a producer tool over two lines", wantLoc: "producers[12].tool", edit: func(t *testing.T, d map[string]any) {
			producers := at(t, d, "comparison", "measurement_profile", "producers").([]any)
			set(t, d, append(producers, map[string]any{"tool": "roslyn\nx", "semantics_version": "roslyn.v1", "status": "ok"}), "comparison", "measurement_profile", "producers")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := baseDoc(t)
			row.edit(t, doc)
			r, err := Decode(encode(t, doc), "v3.4.0")
			if row.wantLoc != "" {
				wantError(t, err, row.wantLoc)
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if row.name == "gate reasons" && !slices.Equal(r.GateReasons, []string{"coupling gate: 2 seams over the strength limit", "coupling gate: drift unmeasured"}) {
				t.Errorf("GateReasons = %q", r.GateReasons)
			}
			if row.name == "no gate reasons" && r.GateReasons != nil {
				t.Errorf("GateReasons = %q, want nil", r.GateReasons)
			}
			if strings.HasPrefix(row.name, ".NET") && len(r.Comparison.MeasurementProfile.Producers) != 15 {
				t.Errorf("producers = %+v", r.Comparison.MeasurementProfile.Producers)
			}
		})
	}
}
