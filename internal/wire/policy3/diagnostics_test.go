package policy3_test

import (
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestPolicyDiagnosticsListEveryProblemByLine(t *testing.T) {
	data := []byte("version: 3\nowners: []\nmodules:\n  api:\n    paths: []\n    volatility: loud\nbuiltin:\n  graph: maybe\nunknown: true\n")
	got := strings.Join(policy3.Diagnostics(data), "\n")
	for _, want := range []string{
		".archfit.yaml:2: policy: owners:",
		".archfit.yaml:5: policy: modules.api.paths:",
		".archfit.yaml:6: policy: modules.api.volatility:",
		".archfit.yaml:8: policy: builtin.graph:",
		".archfit.yaml:9: policy: field unknown not found",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, got)
		}
	}
}

func TestVersionDiagnosticsUseTheVersionLine(t *testing.T) {
	for _, version := range []string{"4", "\"3\"", "2"} {
		data := []byte("# header\nversion: " + version + "\n")
		got := policy3.Diagnostics(data)
		if len(got) != 1 || !strings.HasPrefix(got[0], ".archfit.yaml:2: policy: version") {
			t.Fatalf("version %s: %v", version, got)
		}
	}
}

func TestNamedRuleDiagnosticsUseTheRuleLine(t *testing.T) {
	data, err := os.ReadFile("testdata/invalid/rule-two-forms-no-instead.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := policy3.Diagnostics(data)
	if len(got) != 2 {
		t.Fatalf("errors %v", got)
	}
	for _, line := range got {
		if !strings.HasPrefix(line, ".archfit.yaml:7: policy:") {
			t.Fatal(line)
		}
	}
}

func TestCustomTypeErrorsKeepAllSiblingDiagnostics(t *testing.T) {
	data := []byte("version: 3\nowners: []\nmodules: {}\nlanguages:\n  go:\n    enabled: []\n")
	got := strings.Join(policy3.Diagnostics(data), "\n")
	for _, want := range []string{".archfit.yaml:2: policy: owners:", ".archfit.yaml:3: policy: modules:", ".archfit.yaml:6: policy:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestPolicyRefusesTrailingDocuments(t *testing.T) {
	data := []byte("version: 3\nowners: [olivia]\nmodules:\n  api:\n    paths: [api/**]\n---\nversion: 3\n")
	if _, err := policy3.Decode(data); err == nil {
		t.Fatal("accepted a second YAML document")
	}
	if got := strings.Join(policy3.Diagnostics(data), "\n"); !strings.Contains(got, ".archfit.yaml:6: policy:") {
		t.Errorf("trailing document location: %s", got)
	}
}
