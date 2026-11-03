package report

import (
	"encoding/json"
	"testing"
)

func TestMetricRatchetInventoryCannotHideAnExistingRequiredGap(t *testing.T) {
	for _, rows := range []any{
		[]any{},
		[]any{map[string]any{"metric": "cycle", "status": "evaluated", "before": 0, "after": 0, "delta": 0, "threshold": "max_new 0"}},
	} {
		var root map[string]any
		if err := json.Unmarshal(ratchetPayload(t, rows), &root); err != nil {
			t.Fatal(err)
		}
		root["decision"].(map[string]any)["unevaluated_required_rules"] = []any{
			map[string]any{"rule_id": "metric_ratchets", "reason": "reference not comparable"},
		}
		payload, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(payload, fixtureEngine); err == nil {
			t.Fatal("inventory hid an existing required metric gap")
		}
	}
}
