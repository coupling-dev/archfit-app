package report

import "fmt"

// Metric ratchet comparison statuses describe measurement, not gate acceptance.
const (
	MetricRatchetEvaluated  = "evaluated"
	MetricRatchetUnmeasured = "unmeasured"
)

// MetricRatchetCheck is presentation evidence for an implicit blocking metric
// check. It never replaces the engine's findings, waivers, or gate decision.
type MetricRatchetCheck struct {
	Metric    string   `json:"metric"`
	Status    string   `json:"status"`
	Threshold string   `json:"threshold,omitempty"`
	Before    *float64 `json:"before,omitempty"`
	After     *float64 `json:"after,omitempty"`
	Delta     *float64 `json:"delta,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

type shadowMetricRatchetCheck struct {
	Metric    *string  `json:"metric"`
	Status    *string  `json:"status"`
	Threshold string   `json:"threshold"`
	Before    *float64 `json:"before"`
	After     *float64 `json:"after"`
	Delta     *float64 `json:"delta"`
	Reason    *string  `json:"reason"`
}

func (c *checker) metricRatchet(s *shadowMetricRatchetCheck) MetricRatchetCheck {
	check := MetricRatchetCheck{
		Metric:    c.str("metric", s.Metric, classIdentifier),
		Status:    c.enum("status", s.Status, MetricRatchetEvaluated, MetricRatchetUnmeasured),
		Threshold: c.opt("threshold", s.Threshold, classText),
	}
	if check.Metric == "" {
		c.fail(phaseRequired, "metric", "must not be empty")
	}
	if check.Status == MetricRatchetEvaluated {
		check.Before = new(c.number("before", s.Before))
		check.After = new(c.number("after", s.After))
		check.Delta = new(c.number("delta", s.Delta))
		if check.Threshold == "" {
			c.fail(phaseRequired, "threshold", "is required for an evaluated comparison")
		}
		if s.Reason != nil {
			c.fail(phaseString, "reason", "must be absent for an evaluated comparison")
		}
	} else {
		check.Reason = c.str("reason", s.Reason, classText)
		if check.Reason == "" {
			c.fail(phaseRequired, "reason", "must explain the unmeasured comparison")
		}
		if s.Before != nil || s.After != nil || s.Delta != nil {
			c.fail(phaseString, "status", "an unmeasured comparison must not carry numbers")
		}
	}
	return check
}

func (c *checker) metricRatchets(s []shadowMetricRatchetCheck, ref *Reference, decision Decision) *[]MetricRatchetCheck {
	if s == nil {
		return nil
	}
	checks := list(c, "metric_ratchets", s, maxSmallList, true, c.metricRatchet)
	seen := make(map[string]bool, len(checks))
	unmeasured := 0
	for _, check := range checks {
		if seen[check.Metric] {
			c.fail(phaseString, "metric_ratchets", "must contain unique metrics")
		}
		seen[check.Metric] = true
		if check.Status == MetricRatchetUnmeasured {
			unmeasured++
		}
		if check.Status == MetricRatchetEvaluated && (ref == nil || ref.Status != "comparable" || !ref.BaselinePresent) {
			c.fail(phaseString, "metric_ratchets", "evaluated comparisons require a comparable accepted reference")
		}
	}
	for _, missing := range decision.UnevaluatedRequiredRules {
		if missing.RuleID == "metric_ratchets" && unmeasured == 0 {
			c.fail(phaseString, "metric_ratchets", "inventory contradicts an unevaluated metric requirement")
		}
	}
	return &checks
}

func metricRatchetEvidence(r Report) string {
	const prefix = "Implicit accepted-reference metric checks: "
	if r.MetricRatchets != nil {
		checks := *r.MetricRatchets
		if len(checks) == 0 {
			return prefix + "none applicable."
		}
		evaluated := 0
		for _, check := range checks {
			if check.Status == MetricRatchetEvaluated {
				evaluated++
			}
		}
		return fmt.Sprintf("%s%d/%d evaluated, %d unmeasured.", prefix, evaluated, len(checks), len(checks)-evaluated)
	}
	for _, missing := range r.Decision.UnevaluatedRequiredRules {
		if missing.RuleID == "metric_ratchets" {
			return prefix + "unavailable — " + missing.Reason + "."
		}
	}
	return prefix + "inventory not reported."
}
