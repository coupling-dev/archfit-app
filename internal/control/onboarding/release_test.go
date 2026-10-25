package onboarding

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

func TestNewWorkflowUsesReleasedV322Pins(t *testing.T) {
	raw, err := renderWorkflow(manifest.Current(), "main", testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"e0a1ef9473d68b426e9c56b2da5986bcee2fc769",
		"v3.2.2",
		"sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35",
	} {
		if !strings.Contains(string(raw), pin) {
			t.Errorf("new workflow lacks released pin %q", pin)
		}
	}
	if strings.Contains(string(raw), "v3.2.0") {
		t.Error("new workflow uses the transition release")
	}
}
