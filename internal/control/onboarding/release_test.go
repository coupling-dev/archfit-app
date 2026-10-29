package onboarding

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

func TestNewWorkflowUsesReleasedV340Pins(t *testing.T) {
	raw, err := renderWorkflow(manifest.Current(), "main", testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"fb2520cb069571d6bdc9274ac97d4df49994b6c7",
		"v3.4.0",
		"sha256:bdcfe29bf9def9a4f0ae544923bc3e755fdec2c2cd6c46ec353c01e60642896b",
	} {
		if !strings.Contains(string(raw), pin) {
			t.Errorf("new workflow lacks released pin %q", pin)
		}
	}
	if strings.Contains(string(raw), "v3.2.0") || strings.Contains(string(raw), "v3.2.2") || strings.Contains(string(raw), "v3.2.3") {
		t.Error("new workflow uses the transition release")
	}
}
