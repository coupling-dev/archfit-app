package wording

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
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
	if got := header["repository"]; !slices.Equal(got, []string{"github.com/coupling-dev/archfit"}) {
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
	var cases []struct {
		Text      string   `json:"text"`
		Want      []string `json:"want"`
		StoryWant []string `json:"storyWant"`
	}
	if err := json.Unmarshal(read(t, "../../../web/e2e/wording-cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		storyWant := tc.StoryWant
		if storyWant == nil {
			storyWant = tc.Want
		}
		if got := StoryNotUsedIn(tc.Text); !slices.Equal(got, storyWant) {
			t.Errorf("StoryNotUsedIn(%q) = %q, want %q", tc.Text, got, storyWant)
		}
		if got := NotUsedIn(tc.Text); !slices.Equal(got, tc.Want) {
			t.Errorf("NotUsedIn(%q) = %q, want %q", tc.Text, got, tc.Want)
		}
	}
}

func TestWebWordsMatchVendoredEngine(t *testing.T) {
	var web struct {
		Words          []string `json:"words"`
		StoryOnlyWords []string `json:"storyOnlyWords"`
		GateNames      string   `json:"gateNames"`
		Source         string   `json:"source"`
	}
	if err := json.Unmarshal(read(t, "../../../web/e2e/not-used.json"), &web); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(web.Words, notUsed.list) {
		t.Errorf("web words = %q, engine words = %q", web.Words, notUsed.list)
	}
	if !slices.Equal(web.StoryOnlyWords, storyOnly.list) {
		t.Errorf("web story words = %q, engine words = %q", web.StoryOnlyWords, storyOnly.list)
	}
	if web.GateNames != notUsed.gateNames.String() {
		t.Errorf("web gate names = %q, engine = %q", web.GateNames, notUsed.gateNames)
	}
	if web.Source != "internal/wire/wording/wording.go.txt" {
		t.Errorf("web source = %q", web.Source)
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

func TestStoryVocabulary(t *testing.T) {
	text := "proposal surface PR reload"
	if got := NotUsedIn(text); len(got) != 0 {
		t.Errorf("legacy = %q", got)
	}
	if got := StoryNotUsedIn(text); !slices.Equal(got, []string{"proposal", "reload", "surface", "PR"}) {
		t.Errorf("story = %q", got)
	}
}
