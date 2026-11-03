package report

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCandidateRatchetSchemaProvenanceIsSeparateFromReleasedSchema(t *testing.T) {
	source := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(loadFixture(t, "schema-candidate/SCHEMA_SOURCE"))), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("invalid candidate source note: %q", line)
		}
		source[key] = value
	}
	sum := sha256.Sum256(loadFixture(t, "schema-candidate/archfit.state.schema.json"))
	for key, expected := range map[string]string{
		"repository": "github.com/alexei-led/archfit",
		"status":     "unreleased-candidate",
		"revision":   "31f29e3dde67c467690257ef17d10ae03db134bd",
		"path":       "archfit.state.schema.json",
		"sha256":     hex.EncodeToString(sum[:]),
	} {
		if source[key] != expected {
			t.Errorf("candidate source %s=%q, want %q", key, source[key], expected)
		}
	}
}
