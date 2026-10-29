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
	// Action v3.1.0 runs the engine it pins (v3.4.0) with the digest of the runner's platform: the workflow names the
	// Action commit only, so an arm64 runner never gets the amd64 digest.
	if !strings.Contains(string(raw), "alexei-led/archfit-action@7ade3b8fc989ec416ea5d570e7e236f47f7845fc\n") {
		t.Errorf("new workflow does not pin Action v3.1.0 at 7ade3b8fc989ec416ea5d570e7e236f47f7845fc:\n%s", raw)
	}
	for _, input := range []string{"engine-version:", "image-digest:"} {
		if strings.Contains(string(raw), input) {
			t.Errorf("new workflow passes %s, which the pinned Action supplies itself", input)
		}
	}
}
