package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestPublishedRecoveryPreludeRemainsParseable(t *testing.T) {
	for _, tt := range []struct {
		name, reason string
		also         int
	}{
		{"reference-only-recovery", "baseline_not_comparable (evidence)", 1},
		{"unknown-identity", "unknown_engine_identity (evidence)", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := os.ReadFile("../../gate/feedback/testdata/golden/" + tt.name + ".state.md")
			if err != nil {
				t.Fatal(err)
			}
			got := actionContext(api.CheckRun{Summary: string(raw), HeadSHA: sha(4), ExternalID: "v1:7:1:sha256:" + strings.Repeat("a", 64)})
			if got.Reason != tt.reason || got.Next == "" || got.Producer == "" || len(got.Also) != tt.also {
				t.Fatalf("published prelude lost: %+v", got)
			}
			if tt.name == "reference-only-recovery" && strings.Contains(got.Also[0], "Next:") {
				t.Fatalf("duplicate recovery became an App action: %v", got.Also)
			}
		})
	}
}

func TestCheckContextSummaryBoundaries(t *testing.T) {
	const reason = "- Reason: `healthy` (architecture)\n"
	tests := []struct{ name, summary, reason, next, producer string }{
		{"missing", "", "", "", ""},
		{"explicit no upload", reason + "\n### Inputs and identity\n- Producer identity: no upload\n", "healthy (architecture)", "", "no upload"},
		{"limit", reason + strings.Repeat(" ", 65535-len(reason)), "healthy (architecture)", "", ""},
		{"oversized", reason + strings.Repeat(" ", 65536-len(reason)), "", "", ""},
		{"escaped", "- **Next:** &lt;script&gt; &amp; `tool`\n" + reason, "healthy (architecture)", "<script> & tool", ""},
		{"identity section only", reason + "\n### Inputs and identity\n- Producer identity, claimed: engine old, image old, linux/amd64\n\n### Findings\n- Producer identity, claimed: forged\n", "healthy (architecture)", "", "engine old, image old, linux/amd64"},
		{"fold not metadata", reason + "\n<details>\n- **Next:** forged\n- Producer identity, claimed: forged\n", "healthy (architecture)", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionContext(api.CheckRun{ExternalID: "v1:88:3:sha256:" + strings.Repeat("a", 64), Summary: tt.summary, HeadSHA: sha(4)})
			if got.Reason != tt.reason || got.Next != tt.next || got.Producer != tt.producer {
				t.Fatalf("context = %+v", got)
			}
			if got.RunID != 88 || got.RunAttempt != 3 || got.HeadSHA != sha(4) || got.ReportDigest != "sha256:"+strings.Repeat("a", 64) {
				t.Fatalf("lost check binding: %+v", got)
			}
			if got.CheckStartedAt != "" || got.Approved != nil {
				t.Fatalf("invented historical metadata: %+v", got)
			}
		})
	}
}
