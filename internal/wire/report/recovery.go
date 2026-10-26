package report

import (
	"strconv"
	"strings"
)

// ReferenceCoversMetricGap matches the producer's exact structured reference cause. Free-text similarity is not causality.
func ReferenceCoversMetricGap(r Report, u UnevaluatedRule) bool {
	ref := r.GateReference
	if u.RuleID != "metric_ratchets" || ref == nil || ref.Status != "non_comparable" || !ref.BaselinePresent || len(ref.Drift) == 0 {
		return false
	}
	prefix := "reference not comparable (drift: " + strings.Join(ref.Drift, ", ") + "): "
	count, ok := strings.CutPrefix(u.Reason, prefix)
	if !ok {
		return false
	}
	count, ok = strings.CutSuffix(count, " metric ratchets cannot be evaluated")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(count)
	return err == nil && n > 0 && strconv.Itoa(n) == count
}

// ReferenceCoversAllGaps permits a shared recovery step only when no independent required-rule gap is present.
func ReferenceCoversAllGaps(r Report) bool {
	if len(r.Decision.UnevaluatedRequiredRules) == 0 {
		return false
	}
	for _, u := range r.Decision.UnevaluatedRequiredRules {
		if !ReferenceCoversMetricGap(r, u) {
			return false
		}
	}
	return true
}
