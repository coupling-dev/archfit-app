package onboarding

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

func TestNewWorkflowUsesReleasedV323Pins(t *testing.T) {
	raw, err := renderWorkflow(manifest.Current(), "main", testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"e168b961824b6801e74f0f0938b49be7e1286819",
		"v3.2.3",
		"sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772",
	} {
		if !strings.Contains(string(raw), pin) {
			t.Errorf("new workflow lacks released pin %q", pin)
		}
	}
	if strings.Contains(string(raw), "v3.2.0") || strings.Contains(string(raw), "v3.2.2") {
		t.Error("new workflow uses the transition release")
	}
}
