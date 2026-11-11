package result_test

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

const (
	resultDir = "testdata/result"
	recordDir = "testdata/record"
	// waitingGolden is the App's own golden: the engine never emits a waiting result.
	waitingGolden = "testdata/waiting.txt"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestVendoredFilesMatchSchemaSource holds every vendored file to its SCHEMA_SOURCE line, and every line to a file.
func TestVendoredFilesMatchSchemaSource(t *testing.T) {
	raw, err := os.ReadFile("SCHEMA_SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	header := map[string][]string{}
	listed := map[string]string{}
	for line := range strings.Lines(string(raw)) {
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

	vendored := []string{"archfit.result.schema.json"}
	for _, pattern := range []string{resultDir + "/*.json", recordDir + "/*.txt"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		vendored = append(vendored, paths...)
	}
	if got, want := slices.Sorted(maps.Keys(listed)), slices.Sorted(slices.Values(vendored)); !slices.Equal(got, want) {
		t.Fatalf("SCHEMA_SOURCE lists %q, vendored files are %q", got, want)
	}
	for _, path := range vendored {
		raw, err := os.ReadFile(path) //nolint:gosec // paths globbed from this package
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != listed[path] {
			t.Errorf("%s: sha256 %s, SCHEMA_SOURCE says %s", path, got, listed[path])
		}
	}
}

// TestRecordMatchesTheGoldens renders every vendored result and compares it byte for byte with its text: the
// engine's golden, or the App's own for the waiting result.
func TestRecordMatchesTheGoldens(t *testing.T) {
	results, err := filepath.Glob(resultDir + "/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no vendored result")
	}
	rendered := map[string]bool{}
	for _, path := range results {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		golden := filepath.Join(recordDir, name+".txt")
		if name == "waiting" {
			golden = waitingGolden
		}
		rendered[golden] = true
		t.Run(name, func(t *testing.T) {
			r, err := result.Decode(read(t, path))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got, want := result.Record(r), string(read(t, golden)); got != want {
				t.Errorf("record differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
			}
		})
	}
	goldens, err := filepath.Glob(recordDir + "/*.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, golden := range append(goldens, waitingGolden) {
		if !rendered[golden] {
			t.Errorf("%s has no vendored result", golden)
		}
	}
}

func TestWaitingResultNamesTheRequest(t *testing.T) {
	r, err := result.Decode(read(t, resultDir+"/waiting.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != result.StatusWaiting {
		t.Errorf("status = %q, want %q", r.Status, result.StatusWaiting)
	}
	record := result.Record(r)
	for _, line := range []string{"  next: wait for an owner: #431\n", "archfit: waiting, request #431\n"} {
		if !strings.Contains(record, line) {
			t.Errorf("record has no line %q:\n%s", line, record)
		}
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed test data paths
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
