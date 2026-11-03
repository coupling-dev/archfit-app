package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func ratchetPayload(t *testing.T, rows any) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(loadFixture(t, baseFixture), &root); err != nil {
		t.Fatal(err)
	}
	root["metric_ratchets"] = rows
	root["gate_reference"] = map[string]any{
		"status": "comparable", "baseline_present": true, "reasons": []string{},
	}
	payload, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestMetricRatchetInventoryDecodesAndReportsMeasurement(t *testing.T) {
	rows := []map[string]any{
		{"metric": "cycle", "status": "evaluated", "before": 0, "after": 1, "delta": 1, "threshold": "max_new 0"},
		{"metric": "coverage", "status": "unmeasured", "reason": "current metric has no comparable delta", "threshold": "min_delta 0"},
	}
	r, err := Decode(ratchetPayload(t, rows), fixtureEngine)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(EvidenceSummary(r), "1/2 evaluated, 1 unmeasured") {
		t.Fatalf("missing explicit inventory: %s", EvidenceSummary(r))
	}
	if r.Decision.HardGates != mustDecodeFixture(t, baseFixture).Decision.HardGates {
		t.Fatal("reporting inventory changed the engine gate")
	}
}

func TestMetricRatchetEmptyInventoryIsNotLegacyUnknown(t *testing.T) {
	r, err := Decode(ratchetPayload(t, []any{}), fixtureEngine)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(EvidenceSummary(r), "none applicable") {
		t.Fatalf("empty inventory was lost: %s", EvidenceSummary(r))
	}
	legacy := mustDecodeFixture(t, baseFixture)
	if !strings.Contains(EvidenceSummary(legacy), "inventory not reported") {
		t.Fatalf("legacy report invented an inventory: %s", EvidenceSummary(legacy))
	}
}

func TestMetricRatchetInventoryRejectsIncompleteOrContradictoryRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows any
	}{
		{"null", nil},
		{"missing status", []any{map[string]any{"metric": "cycle"}}},
		{"unknown status", []any{map[string]any{"metric": "cycle", "status": "pass"}}},
		{"missing values", []any{map[string]any{"metric": "cycle", "status": "evaluated", "threshold": "max_new 0"}}},
		{"missing reason", []any{map[string]any{"metric": "cycle", "status": "unmeasured"}}},
		{"unmeasured with values", []any{map[string]any{"metric": "cycle", "status": "unmeasured", "reason": "reference drift", "before": 0}}},
		{"duplicate metrics", []any{
			map[string]any{"metric": "cycle", "status": "unmeasured", "reason": "reference drift"},
			map[string]any{"metric": "cycle", "status": "unmeasured", "reason": "reference drift"},
		}},
		{"multiline reason", []any{map[string]any{"metric": "cycle", "status": "unmeasured", "reason": "line one\nline two"}}},
		{"unknown field", []any{map[string]any{"metric": "cycle", "status": "unmeasured", "reason": "reference drift", "approved": true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(ratchetPayload(t, tc.rows), fixtureEngine); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}
