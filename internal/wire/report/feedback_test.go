package report

import "testing"

func TestReferenceRecoveryRequiresTheExactStructuredCause(t *testing.T) {
	const reason = "reference not comparable (drift: measurement_profile): 4 metric ratchets cannot be evaluated"
	base := Report{GateReference: &Reference{Status: "non_comparable", BaselinePresent: true, Drift: []string{"measurement_profile"}}}
	for _, tt := range []struct {
		name, rule, reason string
		ref                *Reference
		want               bool
	}{
		{"same cause", "metric_ratchets", reason, base.GateReference, true},
		{"independent rule", "layers", reason, base.GateReference, false},
		{"missing reference", "metric_ratchets", reason, nil, false},
		{"different drift", "metric_ratchets", "reference not comparable (drift: model_hash): 4 metric ratchets cannot be evaluated", base.GateReference, false},
		{"generic prose", "metric_ratchets", "reference not comparable (its inputs changed): 4 metric ratchets cannot be evaluated", base.GateReference, false},
		{"no metrics", "metric_ratchets", "reference not comparable (drift: measurement_profile): 0 metric ratchets cannot be evaluated", base.GateReference, false},
		{"extra prose", "metric_ratchets", reason + " tool failed", base.GateReference, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			r.GateReference = tt.ref
			if got := ReferenceCoversMetricGap(r, UnevaluatedRule{RuleID: tt.rule, Reason: tt.reason}); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	base.Decision.UnevaluatedRequiredRules = []UnevaluatedRule{{RuleID: "metric_ratchets", Reason: reason}}
	if !ReferenceCoversAllGaps(base) {
		t.Fatal("same cause lost")
	}
	base.Decision.UnevaluatedRequiredRules = append(base.Decision.UnevaluatedRequiredRules, UnevaluatedRule{RuleID: "layers", Reason: "required tool absent"})
	if ReferenceCoversAllGaps(base) {
		t.Fatal("independent gap collapsed")
	}
}

func TestPairAggregateRetainsMixedAndCycleFindingEvidence(t *testing.T) {
	pair := Finding{ID: "cycle", Origin: "introduced", Edge: Edge{From: Endpoint{Module: "a"}, To: Endpoint{Module: "b"}}}
	old := pair
	old.ID = "old"
	old.Origin = "pre_existing"
	fixed := pair
	fixed.Status = "fixed"
	for _, tt := range []struct {
		name, status string
		findings     []Finding
		want         string
	}{
		{"mixed", "comparable", []Finding{old, pair}, PairAdded},
		{"cycle only", "comparable", []Finding{pair}, PairAdded},
		{"unknown comparison", "unknown", []Finding{pair}, ""},
		{"fixed only", "comparable", []Finding{fixed}, ""},
		{"observed pair without finding", "comparable", nil, ""},
		{"pre-existing only", "comparable", []Finding{old}, PairExisting},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := Group(Report{Comparison: Comparison{OriginStatus: tt.status}, Findings: tt.findings}, 0)
			if got := PairOrigin(pair, g.All); got != tt.want {
				t.Fatalf("origin=%q want %q", got, tt.want)
			}
		})
	}
}

func TestFeedbackInventoryAndFindingOriginRemainDistinct(t *testing.T) {
	r := Report{Dimensions: map[string]Dimension{"intent": {Coverage: DimensionCoverage{Basis: "declared rules evaluated", Observed: 41, Total: 41}}}, Comparison: Comparison{OriginStatus: "unknown"}, Decision: Decision{UnevaluatedRequiredRules: []UnevaluatedRule{{RuleID: "metric_ratchets", Reason: "reference unavailable"}}}}
	want := "Declared rule checks: 41/41 evaluated. Implicit accepted-reference metric checks: unavailable — reference unavailable. Accepted reference: not reported."
	if EvidenceSummary(r) != want {
		t.Fatalf("%s", EvidenceSummary(r))
	}
	if PRImpact(r) != "PR impact: unknown — no comparable Git-base finding-origin evidence." {
		t.Fatalf("%s", PRImpact(r))
	}
	if NoRequiredChange(r) {
		t.Fatal("unknown gate is not positive")
	}
}
