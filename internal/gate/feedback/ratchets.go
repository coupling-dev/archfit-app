package feedback

import (
	"fmt"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func metricRatchetLines(r *report.Report, limit int) []string {
	if r.MetricRatchets == nil || len(*r.MetricRatchets) == 0 {
		return nil
	}
	lines := []string{"", "### Metric ratchets"}
	for i, check := range *r.MetricRatchets {
		if limit > 0 && i == limit {
			lines = append(lines, fmt.Sprintf("%d more metric checks: read the exact check.", len(*r.MetricRatchets)-i))
			break
		}
		line := "- " + code(check.Metric) + ": " + text(check.Status)
		if check.Status == report.MetricRatchetEvaluated {
			line += fmt.Sprintf("; %s → %s (delta %s)", number(*check.Before), number(*check.After), signed(*check.Delta))
		}
		if check.Threshold != "" {
			line += "; threshold " + code(check.Threshold)
		}
		if check.Reason != "" {
			line += "; " + text(check.Reason)
		}
		lines = append(lines, line)
	}
	return lines
}
