package gate

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestPatchTransitionProducerOutcomes(t *testing.T) {
	for _, tc := range []struct {
		version, digest, conclusion, reason string
	}{
		{"v3.2.2", "sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35", "success", "healthy"},
		{"v3.2.0", "sha256:5f2d4a5fff1ace10d7b08937429179631ae0c1d0e1f9e6f37c72b30954cf00db", "success", "healthy"},
		{"v3.0.1", "sha256:9aac269fa350e643054cbe5ced58426b4ed46000f30d60f49564aa605ece2408", "action_required", "unknown_engine_identity"},
		{"v3.2.3", "sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35", "action_required", "report_invalid"},
		{"v3.2.2", "sha256:47575b70d4602859011fecfc9c01c4d6a653f38c033cc880350f301a1ba5f07b", "action_required", "unknown_engine_identity"},
	} {
		t.Run(tc.version+"/"+tc.digest, func(t *testing.T) {
			w := newWorld(t)
			w.setFile(w.shop, testBranch, policy.BaselinePath, []byte(`{"baseline":1}`))
			p := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, p))
			s := w.spec(p, w.report(w.shop, fixtureHealthy, gateReference("comparable")))
			s.env.BaselineDigest = w.digestOf(w.shop, testBranch, policy.BaselinePath)
			s.env.EngineVersion = tc.version
			s.env.ImageDigest = tc.digest
			s.env.Platform = "linux/amd64"
			w.wantAnswer(w.send(s), 200, tc.conclusion, tc.reason)
			w.wantGate(p, runCompleted, tc.conclusion)
			if tc.conclusion != "success" {
				w.noSuccess()
			}
		})
	}
}
