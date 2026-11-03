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
	// Action v3.1.1 runs the engine it pins (v3.4.0) with the digest of the runner's platform: the workflow names the
	// Action commit only, so an arm64 runner never gets the amd64 digest.
	if !strings.Contains(string(raw), "alexei-led/archfit-action@2221f4bfece0725a657414fed751cdc090ba1bd4\n") {
		t.Errorf("new workflow does not pin Action v3.1.1 at 2221f4bfece0725a657414fed751cdc090ba1bd4:\n%s", raw)
	}
	for _, input := range []string{"engine-version:", "image-digest:"} {
		if strings.Contains(string(raw), input) {
			t.Errorf("new workflow passes %s, which the pinned Action supplies itself", input)
		}
	}
}
