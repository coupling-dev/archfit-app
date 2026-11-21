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

	"github.com/coupling-dev/archfit-app/internal/wire/result"
)

// corpus is the engine's vendored contract: its schema, its results and their records, pinned by its SCHEMA_SOURCE.
type corpus struct {
	dir        string // testdata/<engine version>
	repository string // the repository SCHEMA_SOURCE names
}

var (
	v090    = corpus{dir: "testdata/v0.9.0", repository: "github.com/coupling-dev/archfit"}
	corpora = []corpus{v090}
)

// resultDir is the corpus of the current engine, the one the refusal tables mutate.
var resultDir = v090.dir + "/result"

// sameRecordAs names, for the results whose repository block is the only difference from another golden, that
// golden: the engine has no record golden for them, because the record never shows the block.
var sameRecordAs = map[string]string{
	"repository_clean":   "pass",
	"repository_drift":   "pass",
	"repository_expired": "expired_exception",
}

// waitingGolden is the App's own golden in the corpus, not vendored: the engine never emits a waiting result.
const waitingGolden = "waiting.txt"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestVendoredFilesMatchSchemaSource holds every vendored file to its SCHEMA_SOURCE line, and every line to a file.
func TestVendoredFilesMatchSchemaSource(t *testing.T) {
	for _, c := range corpora {
		t.Run(c.dir, func(t *testing.T) {
			raw := read(t, c.dir+"/SCHEMA_SOURCE")
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
			if got := header["repository"]; !slices.Equal(got, []string{c.repository}) {
				t.Errorf("repository = %q, want %q", got, c.repository)
			}
			if got := header["commit"]; len(got) != 1 || !commitPattern.MatchString(got[0]) {
				t.Errorf("commit = %q, want one 40-hex commit", got)
			}
			vendored := []string{"archfit.result.schema.json"}
			for _, pattern := range []string{"result/*.json", "record/*.txt"} {
				paths, err := filepath.Glob(filepath.Join(c.dir, pattern))
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range paths {
					vendored = append(vendored, strings.TrimPrefix(p, c.dir+"/"))
				}
			}
			if got, want := slices.Sorted(maps.Keys(listed)), slices.Sorted(slices.Values(vendored)); !slices.Equal(got, want) {
				t.Fatalf("SCHEMA_SOURCE lists %q, vendored files are %q", got, want)
			}
			for _, path := range vendored {
				sum := sha256.Sum256(read(t, filepath.Join(c.dir, path)))
				if got := hex.EncodeToString(sum[:]); got != listed[path] {
					t.Errorf("%s: sha256 %s, SCHEMA_SOURCE says %s", path, got, listed[path])
				}
			}
		})
	}
}

// TestRecordMatchesTheGoldens decodes every vendored result of both engines and renders it byte for byte to its text:
// the engine's golden, or the App's own for the waiting result.
func TestRecordMatchesTheGoldens(t *testing.T) {
	for _, c := range corpora {
		results, err := filepath.Glob(c.dir + "/result/*.json")
		if err != nil {
			t.Fatal(err)
		}
		if len(results) == 0 {
			t.Fatalf("%s: no vendored result", c.dir)
		}
		rendered := map[string]bool{}
		for _, path := range results {
			name := strings.TrimSuffix(filepath.Base(path), ".json")
			golden := filepath.Join(c.dir, "record", name+".txt")
			if other, ok := sameRecordAs[name]; ok {
				golden = filepath.Join(c.dir, "record", other+".txt")
			}
			if name == "waiting" {
				golden = filepath.Join(c.dir, waitingGolden)
			}
			rendered[golden] = true
			t.Run(c.dir+"/"+name, func(t *testing.T) {
				r, err := result.Decode(read(t, path))
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if got, want := result.Record(r), string(read(t, golden)); got != want {
					t.Errorf("record differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
				}
			})
		}
		goldens, err := filepath.Glob(c.dir + "/record/*.txt")
		if err != nil {
			t.Fatal(err)
		}
		for _, golden := range append(goldens, filepath.Join(c.dir, waitingGolden)) {
			if !rendered[golden] {
				t.Errorf("%s has no vendored result", golden)
			}
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
