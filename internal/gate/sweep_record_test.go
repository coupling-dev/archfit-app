package gate

import (
	"context"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestPullRecordKeepsOnlyAnUnambiguousPolicyChangedRefusal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reasons []reason4
		want    string
	}{
		{"policy changed", []reason4{{Code: reasonPolicyChanged}}, reasonPolicyChanged},
		{"also refused", []reason4{{Code: reasonPolicyChanged}, {Code: reasonWorkflowChanged}}, ""},
		{"another refusal", []reason4{{Code: reasonWorkflowChanged}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			u := ingest.Upload4{Binding: ingest.Binding4{Repo: w.shop, PullRequest: 7, HeadSHA: p.head, RunID: p.runID, RunAttempt: 1},
				Envelope: envelope.EnvelopeV2{PolicyDigest: "untrusted", PayloadDigest: "sha256:result"}}
			if err := w.gate.v4.storePull(context.Background(), u, decision4{Status: result.StatusUnknown, Reasons: tc.reasons}, true); err != nil {
				t.Fatal(err)
			}
			rec, _ := w.pullRecord(p)
			if rec.Reason != tc.want || rec.PolicyDigest != "" {
				t.Errorf("record = %+v, want reason %q and no accepted policy digest", rec, tc.want)
			}
		})
	}
}
