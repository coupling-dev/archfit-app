package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

const policyCardHeading = "### Policy change"

func policyChangeEnvelope(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "wire", "testdata", "policy-change", "widen.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The Action's classification is shown only where the App's own diff of the protected ref says the policy changed.
func TestPolicyChangeCardNeedsTheAppsOwnDiff(t *testing.T) {
	tests := []struct {
		name          string
		appSeesPolicy bool
		envelopeCard  bool
		wantCard      bool
	}{
		{"the diff and the Action agree", true, true, true},
		{"the Action sends a card the diff does not back", false, true, false},
		{"the diff changes the policy and the Action sends none", true, false, false},
		{"neither", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			if tt.appSeesPolicy {
				w.editsPolicy(p)
			} else {
				w.changes(p, "pkg/a/a.go")
			}
			w.handle(w.event(webhook.PullOpened, p))
			spec := w.spec(p, w.report(w.shop, fixtureHealthy))
			if tt.envelopeCard {
				spec.env.PolicyChange = policyChangeEnvelope(t)
			}
			w.send(spec)
			if got := strings.Contains(w.feedbackText(p), policyCardHeading); got != tt.wantCard {
				t.Errorf("card shown = %v, want %v", got, tt.wantCard)
			}
		})
	}
}
