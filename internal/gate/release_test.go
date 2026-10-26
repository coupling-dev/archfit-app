package gate

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestPatchTransitionProducerOutcomes(t *testing.T) {
	for _, tc := range []struct {
		version, digest, platform, conclusion, reason string
	}{
		{"v3.2.3", "sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772", "linux/amd64", "success", "healthy"},
		{"v3.2.3", "sha256:54f6bf1ec99be6f4bdc21ca59e16d3dd65fb5b31a2c2af6c932ad556c89a220c", "linux/arm64", "success", "healthy"},
		{"v3.2.2", "sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35", "linux/amd64", "success", "healthy"},
		{"v3.2.0", "sha256:5f2d4a5fff1ace10d7b08937429179631ae0c1d0e1f9e6f37c72b30954cf00db", "linux/amd64", "success", "healthy"},
		{"v3.0.1", "sha256:9aac269fa350e643054cbe5ced58426b4ed46000f30d60f49564aa605ece2408", "linux/amd64", "action_required", "unknown_engine_identity"},
		{"v3.2.3", "sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35", "linux/amd64", "action_required", "unknown_engine_identity"},
		{"v3.2.4", "sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772", "linux/amd64", "action_required", "report_invalid"},
		{"v3.2.3", "sha256:a1ada076a294482ae2b695246dc1cf965ed46be8dae30c8b77a6b94951763137", "linux/amd64", "action_required", "unknown_engine_identity"},
		{"v3.2.3", "sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772", "linux/arm64", "action_required", "unknown_engine_identity"},
		{"v3.2.2", "sha256:47575b70d4602859011fecfc9c01c4d6a653f38c033cc880350f301a1ba5f07b", "linux/amd64", "action_required", "unknown_engine_identity"},
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
			s.env.Platform = tc.platform
			w.wantAnswer(w.send(s), 200, tc.conclusion, tc.reason)
			w.wantGate(p, runCompleted, tc.conclusion)
			if tc.conclusion != "success" {
				w.noSuccess()
			}
		})
	}
}
