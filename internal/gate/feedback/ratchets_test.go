package feedback

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestMetricRatchetFeedbackRetainsEvidenceWithoutInventingPass(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	checks := []report.MetricRatchetCheck{
		{Metric: "cycle", Status: report.MetricRatchetEvaluated, Before: new(0.0), After: new(1.0), Delta: new(1.0), Threshold: "max_new 0"},
		{Metric: "coverage", Status: report.MetricRatchetUnmeasured, Threshold: "min_delta 0", Reason: "reference not comparable"},
	}
	p.Report.MetricRatchets = &checks
	for _, body := range []string{compactEvidence(p), gapsBlock(p)} {
		mustContain(t, body, "1/2 evaluated, 1 unmeasured", "Metric ratchets", "cycle", "0 → 1 (delta +1)", "max_new 0", "coverage", "reference not comparable")
		if strings.Contains(body, "2/2 passed") {
			t.Fatal("measurement was misrepresented as gate success")
		}
	}
	if p.Outcome.Conclusion != verdict.Failure {
		t.Fatal("presentation changed the gate conclusion")
	}
}
