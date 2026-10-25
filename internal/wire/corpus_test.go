package wire_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	corpusRoot     = "testdata"
	v300Revision   = "bbd658f008bcc4bc83e063cf305d20dc4508da2e"
	v301Revision   = "0649e504a6761f565086b342d315110feeb27d77"
	v320Revision   = "faa4eb4e513859567fd6c380230c91ea22fdaab6"
	leakMarker     = "LEAK-7f3a"
	syntheticLabel = "synthetic: "

	// fixtureEngine is the engine release every synthetic report and leakage fixture is a document of.
	fixtureEngine = "v3.2.0"

	folderEnvelope = "envelope"
	folderReport   = "report"
	folderPolicy   = "policy"
	folderLeakage  = "leakage"
	folderSchema   = "schema"

	suffixExpect   = ".expect.json"
	suffixPolicy   = ".policy.yaml"
	suffixBaseline = ".baseline.json"
	suffixLabels   = ".labels.yaml"
	suffixEnvelope = ".envelope.json"
	suffixPayload  = ".payload"

	decodeOK    = "ok"
	decodeError = "error"
)

var (
	fixtureFolders  = []string{folderEnvelope, folderReport, folderPolicy, folderLeakage}
	siblingSuffixes = []string{suffixExpect, suffixPolicy, suffixBaseline, suffixLabels, suffixEnvelope, suffixPayload}

	requiredReportFixtures = []string{
		"healthy", "attention-pass", "blocked", "missing-evidence", "unknown-identity", "profile-comparable",
		"profile-incompatible", "invalid-input", "blocker-and-unevaluated", "non-applicable-rule", "locations-empty",
		"matched-by-populated", "repair-kinds", "waiver-expiry-order", "real-blocked-no-baseline",
		"real-v320-blocked-no-baseline", "real-v320-baselined-comparable", "real-v320-pr-origin", "real-v320-missing-compiler",
	}

	factKeys = map[string]bool{
		"identity_approved": true, "workflow_approved": true, "problems": true,
		"baseline_digest": true, "labels_digest": true,
		"trusted_policy_digests": true, "trusted_baseline_digests": true, "trusted_labels_digests": true,
	}

	// pinnedRevisions maps every engine release the corpus holds captures of to that release's source revision.
	pinnedRevisions = map[string]string{"v3.0.0": v300Revision, "v3.0.1": v301Revision, "v3.2.0": v320Revision}

	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	reasonPattern   = regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
)

type producer struct {
	Repository    string `json:"repository"`
	Revision      string `json:"revision"`
	EngineVersion string `json:"engine_version"`
	Command       string `json:"command"`
}

type gateExpectation struct {
	Conclusion string   `json:"conclusion"`
	Reasons    []string `json:"reasons"`
}

type expectation struct {
	Producer json.RawMessage            `json:"producer"`
	Decode   string                     `json:"decode"`
	Fields   json.RawMessage            `json:"fields"`
	Gate     *gateExpectation           `json:"gate"`
	Facts    map[string]json.RawMessage `json:"facts"`
}

// engineProducer returns the producer object, or the synthetic reason when the fixture was not captured.
func (e expectation) engineProducer() (p producer, reason string, err error) {
	if err := json.Unmarshal(e.Producer, &reason); err == nil {
		if !strings.HasPrefix(reason, syntheticLabel) || strings.TrimSpace(strings.TrimPrefix(reason, syntheticLabel)) == "" {
			return producer{}, "", fmt.Errorf("producer string must be %q followed by a reason", syntheticLabel)
		}
		return producer{}, reason, nil
	}
	dec := json.NewDecoder(bytes.NewReader(e.Producer))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return producer{}, "", fmt.Errorf("producer: %w", err)
	}
	return p, "", nil
}

func (e expectation) identityApproved(t *testing.T) bool {
	t.Helper()
	raw, ok := e.Facts["identity_approved"]
	if !ok {
		return true
	}
	var approved bool
	if err := json.Unmarshal(raw, &approved); err != nil {
		t.Fatalf("facts.identity_approved: %v", err)
	}
	return approved
}

type fixture struct {
	Folder string
	Name   string
	Path   string // the fixture file itself
	Expect expectation
}

func (f fixture) id() string { return f.Folder + "/" + f.Name }

// engine is the engine release whose state document the fixture is: the capture's own, else fixtureEngine.
func (f fixture) engine(t *testing.T) string {
	t.Helper()
	p, reason, err := f.Expect.engineProducer()
	if err != nil {
		t.Fatalf("%s: %v", f.id(), err)
	}
	if reason != "" {
		return fixtureEngine
	}
	return p.EngineVersion
}

func (f fixture) bytes(t *testing.T) []byte {
	t.Helper()
	return readFile(t, f.Path)
}

// sibling returns the bytes of an optional sibling file.
func (f fixture) sibling(t *testing.T, suffix string) ([]byte, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(corpusRoot, f.Folder, f.Name+suffix))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("%s: read sibling %s: %v", f.id(), suffix, err)
	}
	return raw, true
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed corpus paths
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func siblingSuffix(file string) string {
	for _, s := range siblingSuffixes {
		if strings.HasSuffix(file, s) {
			return s
		}
	}
	return ""
}

func decodeExpectation(raw []byte) (expectation, error) {
	var e expectation
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return expectation{}, err
	}
	return e, nil
}

// loadCorpus lists every fixture of the four fixture folders and fails on a layout defect: a missing or malformed
// expectation, or a sibling file without its fixture.
func loadCorpus(t *testing.T) []fixture {
	t.Helper()
	var all []fixture
	for _, folder := range fixtureFolders {
		entries, err := os.ReadDir(filepath.Join(corpusRoot, folder))
		if err != nil {
			t.Fatalf("read corpus folder: %v", err)
		}
		names := map[string]bool{}
		var siblings []string
		for _, entry := range entries {
			file := entry.Name()
			if entry.IsDir() {
				t.Errorf("%s/%s: folders are not allowed inside a fixture folder", folder, file)
				continue
			}
			if siblingSuffix(file) != "" {
				siblings = append(siblings, file)
				continue
			}
			name := strings.TrimSuffix(file, filepath.Ext(file))
			if strings.Contains(name, ".") || names[name] {
				t.Errorf("%s/%s: fixture names are unique and carry one extension", folder, file)
				continue
			}
			names[name] = true
			path := filepath.Join(corpusRoot, folder, file)
			raw, err := os.ReadFile(filepath.Join(corpusRoot, folder, name+suffixExpect))
			if err != nil {
				t.Errorf("%s/%s: no expectation file: %v", folder, name, err)
				continue
			}
			expect, err := decodeExpectation(raw)
			if err != nil {
				t.Errorf("%s/%s: expectation: %v", folder, name, err)
				continue
			}
			all = append(all, fixture{Folder: folder, Name: name, Path: path, Expect: expect})
		}
		for _, file := range siblings {
			if name := strings.TrimSuffix(file, siblingSuffix(file)); !names[name] {
				t.Errorf("%s/%s: orphan sibling, no fixture named %q", folder, file, name)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].id() < all[j].id() })
	return all
}

func inFolder(all []fixture, folders ...string) []fixture {
	var out []fixture
	for _, f := range all {
		for _, folder := range folders {
			if f.Folder == folder {
				out = append(out, f)
			}
		}
	}
	return out
}
