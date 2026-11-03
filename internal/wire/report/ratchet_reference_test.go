package report

import (
	"encoding/json"
	"testing"
)

func TestEvaluatedRatchetRequiresALoadedComparableReference(t *testing.T) {
	for _, ref := range []any{
		nil,
		map[string]any{"status": "non_comparable", "baseline_present": true, "reasons": []string{"measurement profile differs"}},
		map[string]any{"status": "comparable", "baseline_present": false, "reasons": []string{}},
	} {
		rows := []any{map[string]any{"metric": "cycle", "status": "evaluated", "before": 0, "after": 0, "delta": 0, "threshold": "max_new 0"}}
		var root map[string]any
		if err := json.Unmarshal(ratchetPayload(t, rows), &root); err != nil {
			t.Fatal(err)
		}
		if ref == nil {
			delete(root, "gate_reference")
		} else {
			root["gate_reference"] = ref
		}
		payload, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(payload, fixtureEngine); err == nil {
			t.Fatal("evaluated ratchet accepted without a loaded comparable reference")
		}
	}
}
