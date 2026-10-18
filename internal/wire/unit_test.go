package wire_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	schemaFile       = "archfit.state.schema.json"
	schemaSourceFile = "SCHEMA_SOURCE"
	schemaSHA256     = "8b5e67ff291570cd8be72b31effd66cf480dc5071747a68ac274e4d80508198c" // engine v3.0.1, same bytes as v3.0.0

	survivingLeakageFixture = "declarations-dropped"
)

func TestFixtureCorpusIsComplete(t *testing.T) {
	all := loadCorpus(t)

	reports := map[string]bool{}
	for _, f := range inFolder(all, folderReport) {
		reports[f.Name] = true
	}
	for _, name := range requiredReportFixtures {
		if !reports[name] {
			t.Errorf("required report fixture %q is missing", name)
		}
	}
	for _, folder := range fixtureFolders {
		if len(inFolder(all, folder)) == 0 {
			t.Errorf("fixture folder %q is empty", folder)
		}
	}

	for _, f := range all {
		e := f.Expect
		if len(e.Producer) == 0 {
			t.Errorf("%s: no producer", f.id())
		}
		if e.Decode != decodeOK && e.Decode != decodeError {
			t.Errorf("%s: decode = %q, want %q or %q", f.id(), e.Decode, decodeOK, decodeError)
		}
		if e.Decode == decodeOK && len(e.Fields) == 0 {
			t.Errorf("%s: decode ok without fields", f.id())
		}
		if e.Decode == decodeError && len(e.Fields) != 0 {
			t.Errorf("%s: decode error with fields", f.id())
		}
		for key := range e.Facts {
			if !factKeys[key] {
				t.Errorf("%s: unknown facts key %q", f.id(), key)
			}
		}
		if f.Folder != folderReport {
			if e.Gate != nil || e.Facts != nil {
				t.Errorf("%s: gate and facts belong to report fixtures only", f.id())
			}
			continue
		}
		if e.Gate == nil || e.Gate.Conclusion == "" || len(e.Gate.Reasons) == 0 {
			t.Errorf("%s: report fixture needs gate.conclusion and a non-empty gate.reasons", f.id())
			continue
		}
		for _, reason := range e.Gate.Reasons {
			if !reasonPattern.MatchString(reason) {
				t.Errorf("%s: reason %q is not a lower snake case code", f.id(), reason)
			}
		}
	}
}

func TestFixturesRecordPinnedProducers(t *testing.T) {
	captured := 0
	for _, f := range loadCorpus(t) {
		p, reason, err := f.Expect.engineProducer()
		if err != nil {
			t.Errorf("%s: %v", f.id(), err)
			continue
		}
		if reason != "" {
			continue
		}
		captured++
		if pinned, ok := pinnedRevisions[p.EngineVersion]; !ok || p.Revision != pinned {
			t.Errorf("%s: release %q at revision %q, want a pinned release at its pinned revision", f.id(), p.EngineVersion, p.Revision)
		}
		if !revisionPattern.MatchString(p.Revision) {
			t.Errorf("%s: revision is not a 40-hex commit, a branch name is not allowed", f.id())
		}
		if p.Repository == "" || p.EngineVersion == "" || p.Command == "" {
			t.Errorf("%s: producer needs repository, engine_version and command", f.id())
		}
	}
	if captured == 0 {
		t.Error("no engine-captured fixture in the corpus")
	}
}

func TestLeakageFixturesCarryTheMarker(t *testing.T) {
	fixtures := inFolder(loadCorpus(t), folderLeakage)
	surviving := 0
	for _, f := range fixtures {
		if !bytes.Contains(f.bytes(t), []byte(leakMarker)) {
			t.Errorf("%s: marker is missing", f.id())
		}
		want := decodeError
		if f.Name == survivingLeakageFixture {
			want = decodeOK
			surviving++
		}
		if f.Expect.Decode != want {
			t.Errorf("%s: decode = %q, want %q", f.id(), f.Expect.Decode, want)
		}
	}
	if surviving != 1 {
		t.Errorf("surviving leakage fixtures = %d, want exactly 1 (%s)", surviving, survivingLeakageFixture)
	}
	if len(fixtures) < 6 {
		t.Errorf("leakage fixtures = %d, want the five rejecting groups and the surviving one", len(fixtures))
	}
}

func TestPinnedEngineSchemaIsRecorded(t *testing.T) {
	dir := filepath.Join(corpusRoot, folderSchema)
	raw, err := os.ReadFile(filepath.Join(dir, schemaFile))
	if err != nil {
		t.Fatalf("pinned engine schema is missing: %v", err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != schemaSHA256 {
		t.Fatalf("schema SHA-256 = %s, want the pinned %s", got, schemaSHA256)
	}

	note, err := os.ReadFile(filepath.Join(dir, schemaSourceFile))
	if err != nil {
		t.Fatalf("schema source note is missing: %v", err)
	}
	source := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(note)), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("source note line %q is not `key: value`", line)
		}
		source[key] = value
	}
	want := map[string]string{"tag": "v3.0.1", "revision": v301Revision, "sha256": schemaSHA256, "path": schemaFile}
	for key, value := range want {
		if source[key] != value {
			t.Errorf("source note %s = %q, want %q", key, source[key], value)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("schema folder holds %d files, want the schema and its source note", len(entries))
	}
}
