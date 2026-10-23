package feedback

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestDimensionResultsDiscloseIncompleteEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, status, result, want string
	}{
		{"measured pass", "measured", "pass", "pass"},
		{"partial pass", "partial", "pass", "pass (incomplete evidence)"},
		{"unmeasured pass", "unmeasured", "pass", "pass (incomplete evidence)"},
		{"partial warning", "partial", "warn", "**warn**"},
		{"partial failure", "partial", "fail", "**fail**"},
		{"not applicable", "unmeasured", "not_applicable", "not applicable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &report.Report{Dimensions: map[string]report.Dimension{
				"intent": {Status: tc.status, Gate: tc.result, Confidence: "medium"},
			}}
			got := dimensionsBlock(r)
			want := "| `intent` | " + tc.status + " | 0/0 | medium | " + tc.want + " |"
			if !strings.Contains(got, want) {
				t.Fatalf("missing truthful result row %q:\n%s", want, got)
			}
		})
	}
}
