package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	leakMarker  = "LEAK-7f3a"
	baseFixture = "report/all-keys.json" // carries every key of the state schema, kept and dropped
	decodeOK    = "ok"
	decodeError = "error"

	// fixtureEngine is the engine release a synthetic fixture is a document of. A capture is a document of the
	// release its producer names (fixtureRelease).
	fixtureEngine = "v3.2.0"
)

// fixtureRoot is the shared corpus; its layout and expectation format are defined in internal/wire/module.md.
const fixtureRoot = "../testdata"

// expectation is the .expect.json sibling of a fixture; this module asserts decode and fields only.
type expectation struct {
	Producer json.RawMessage `json:"producer"` // an object for an engine capture, "synthetic: <reason>" otherwise
	Decode   string          `json:"decode"`   // "ok" | "error"
	Fields   expectedFields  `json:"fields"`
}

type expectedFields struct {
	Verdict             string   `json:"verdict"`
	HardGates           string   `json:"hard_gates"`
	UnevaluatedRuleIDs  []string `json:"unevaluated_rule_ids"`
	HasProfile          bool     `json:"has_profile"`
	ProfileVersion      string   `json:"profile_version"`
	GateReferenceStatus string   `json:"gate_reference_status"`
}

func loadFixture(t testing.TB, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureRoot, rel))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// fixtureRelease is the engine release whose document a fixture is: a capture's own, else fixtureEngine.
func fixtureRelease(t testing.TB, rel string) string {
	t.Helper()
	var p struct {
		EngineVersion string `json:"engine_version"`
	}
	if json.Unmarshal(loadExpectation(t, rel).Producer, &p) != nil {
		return fixtureEngine
	}
	return p.EngineVersion
}

func loadExpectation(t testing.TB, rel string) expectation {
	t.Helper()
	var e expectation
	if err := json.Unmarshal(loadFixture(t, strings.TrimSuffix(rel, ".json")+".expect.json"), &e); err != nil {
		t.Fatalf("expectation of %s: %v", rel, err)
	}
	if len(e.Producer) == 0 || (e.Decode != decodeOK && e.Decode != decodeError) {
		t.Fatalf("expectation of %s: producer and decode are mandatory", rel)
	}
	return e
}

// fixtures lists the fixtures of one corpus folder, as paths relative to the corpus. A fixture name has no dot;
// every other file is a sibling (.expect.json, .baseline.json, .envelope.json).
func fixtures(t testing.TB, dir string) []string {
	t.Helper()
	all, err := filepath.Glob(filepath.Join(fixtureRoot, dir, "*.json"))
	if err != nil || len(all) == 0 {
		t.Fatalf("no fixtures in %s: %v", dir, err)
	}
	var out []string
	for _, p := range all {
		if base := filepath.Base(p); !strings.Contains(strings.TrimSuffix(base, ".json"), ".") {
			out = append(out, dir+"/"+base)
		}
	}
	return out
}

func mustDecode(t testing.TB, payload []byte) Report {
	t.Helper()
	return mustDecodeAs(t, payload, fixtureEngine)
}

func mustDecodeAs(t testing.TB, payload []byte, release string) Report {
	t.Helper()
	r, err := Decode(payload, release)
	if err != nil {
		t.Fatalf("Decode as %s: %v", release, err)
	}
	return r
}

func baseDoc(t testing.TB) map[string]any {
	t.Helper()
	return parseDoc(t, loadFixture(t, baseFixture))
}

func parseDoc(t testing.TB, b []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func encode(t testing.TB, doc any) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// at walks a generic JSON tree: a string step indexes an object, an int step a list.
func at(t testing.TB, v any, steps ...any) any {
	t.Helper()
	for _, s := range steps {
		switch s := s.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("step %q: not an object", s)
			}
			v = m[s]
		case int:
			l, ok := v.([]any)
			if !ok || s >= len(l) {
				t.Fatalf("step %d: not a list of that length", s)
			}
			v = l[s]
		}
	}
	return v
}

func set(t testing.TB, doc any, value any, steps ...any) {
	t.Helper()
	parent, last := at(t, doc, steps[:len(steps)-1]...), steps[len(steps)-1]
	switch last := last.(type) {
	case string:
		parent.(map[string]any)[last] = value
	case int:
		parent.([]any)[last] = value
	}
}

func del(t testing.TB, doc any, steps ...any) {
	t.Helper()
	delete(at(t, doc, steps[:len(steps)-1]...).(map[string]any), steps[len(steps)-1].(string))
}

// wantError fails unless err is set and names loc. No error text may echo a payload value.
func wantError(t *testing.T, err error, loc string) {
	t.Helper()
	if err == nil {
		t.Fatalf("decoded, want an error naming %q", loc)
	}
	if !strings.Contains(err.Error(), loc) {
		t.Errorf("error %q does not name %q", err, loc)
	}
}

// mutation is one table row: edit the base document, then expect success (wantLoc "") or an error naming wantLoc.
type mutation struct {
	name    string
	edit    func(t *testing.T, doc map[string]any)
	wantLoc string
}

func runMutations(t *testing.T, rows []mutation) {
	t.Helper()
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := baseDoc(t)
			row.edit(t, doc)
			r, err := Decode(encode(t, doc), fixtureEngine)
			if row.wantLoc == "" {
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				return
			}
			wantError(t, err, row.wantLoc)
			if !isZero(r) {
				t.Error("an error must come with a zero Report")
			}
		})
	}
}

func isZero(r Report) bool {
	return r.SchemaVersion == "" && r.Verdict == "" && r.Dimensions == nil && r.Findings == nil &&
		r.AgentTasks == nil && r.Seams == nil && r.GateReference == nil
}

func setter(value any, steps ...any) func(*testing.T, map[string]any) {
	return func(t *testing.T, doc map[string]any) { set(t, doc, value, steps...) }
}

func deleter(steps ...any) func(*testing.T, map[string]any) {
	return func(t *testing.T, doc map[string]any) { del(t, doc, steps...) }
}
