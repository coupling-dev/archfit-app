package wording_test

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

const wordingFile = "wording.go.txt"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestVendoredFileMatchesSchemaSource holds the vendored word list to its SCHEMA_SOURCE line, and the line to a file.
func TestVendoredFileMatchesSchemaSource(t *testing.T) {
	header := map[string][]string{}
	listed := map[string]string{}
	for line := range strings.Lines(string(read(t, "SCHEMA_SOURCE"))) {
		line = strings.TrimSpace(line)
		if key, value, ok := strings.Cut(line, ": "); ok {
			header[key] = append(header[key], value)
			continue
		}
		sum, path, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("SCHEMA_SOURCE line %q is neither key: value nor <sha256>  <path>", line)
		}
		listed[path] = sum
	}
	if got := header["repository"]; !slices.Equal(got, []string{"github.com/alexei-led/archfit"}) {
		t.Errorf("repository = %q", got)
	}
	if got := header["commit"]; len(got) != 1 || !commitPattern.MatchString(got[0]) {
		t.Errorf("commit = %q, want one 40-hex commit", got)
	}
	if got := header["from"]; !slices.Equal(got, []string{wordingFile + " <- internal/testutil/wording.go"}) {
		t.Errorf("from = %q", got)
	}
	if got := slices.Sorted(maps.Keys(listed)); !slices.Equal(got, []string{wordingFile}) {
		t.Fatalf("SCHEMA_SOURCE lists %q, want only %s", got, wordingFile)
	}
	sum := sha256.Sum256(read(t, wordingFile))
	if got := hex.EncodeToString(sum[:]); got != listed[wordingFile] {
		t.Errorf("%s: sha256 %s, SCHEMA_SOURCE says %s", wordingFile, got, listed[wordingFile])
	}
}

func TestNotUsedMatchesAsTheEngine(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{text: "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n"},
		{text: "a decision request", want: []string{"decision", "decision request"}},
		{text: "needs\n  attention", want: []string{"needs attention"}},
		{text: "config_file edge_count tasks"},
		{text: "the check archfit/gate failed; gate: required"},
		{text: "the gate is closed", want: []string{"gate"}},
		{text: "A Finding, a WAIVER", want: []string{"waiver", "finding"}},
	} {
		if got := wording.NotUsedIn(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("NotUsedIn(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
