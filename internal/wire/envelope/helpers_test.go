package envelope_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

// fixtureDir is the shared corpus; its layout and expectation format are defined in internal/wire/module.md.
const fixtureDir = "../testdata/envelope"

const (
	expectSuffix = ".expect.json"
	decodeOK     = "ok"
	hex40        = "1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
	hex64        = "5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b"
)

func fieldOrder() []string {
	return []string{
		"schema_version", "kind", "repository", "event", "pull_request", "head_sha", "base_sha",
		"merge_base_sha", "run_id", "run_attempt", "engine_version", "image_digest", "platform",
		"payload_digest", "baseline_digest", "labels_digest",
	}
}

// prFields is a valid pull_request report envelope as key -> raw JSON value.
func prFields() map[string]string {
	return map[string]string{
		"schema_version":  `"archfit.report-envelope.v1"`,
		"kind":            `"report"`,
		"repository":      `"alexei-led/archfit-demo"`,
		"event":           `"pull_request"`,
		"pull_request":    `42`,
		"head_sha":        `"` + hex40 + `"`,
		"base_sha":        `"` + hex40 + `"`,
		"merge_base_sha":  `"` + hex40 + `"`,
		"run_id":          `17654321098`,
		"run_attempt":     `1`,
		"engine_version":  `"v3.0.0"`,
		"image_digest":    `"sha256:` + hex64 + `"`,
		"platform":        `"linux/amd64"`,
		"payload_digest":  `"sha256:` + hex64 + `"`,
		"baseline_digest": `""`,
		"labels_digest":   `""`,
	}
}

// pushFields is a valid envelope for an event that is not pull_request.
func pushFields(event string) map[string]string {
	f := prFields()
	f["event"] = `"` + event + `"`
	f["pull_request"] = `0`
	f["base_sha"] = `""`
	f["merge_base_sha"] = `""`
	return f
}

func discoveryFields() map[string]string {
	f := pushFields("workflow_dispatch")
	f["kind"] = `"discovery"`
	return f
}

func baselineFields() map[string]string {
	f := pushFields("workflow_dispatch")
	f["kind"] = `"baseline"`
	return f
}

func reanchorFields() map[string]string {
	f := pushFields("workflow_dispatch")
	f["kind"] = `"reanchor"`
	f["baseline_digest"] = quoted(hex64)
	return f
}

// render writes the object in contract key order; keys outside the contract follow, sorted.
func render(fields map[string]string) []byte {
	keys := fieldOrder()
	known := make(map[string]bool, len(keys))
	for _, k := range keys {
		known[k] = true
	}
	var extra []string
	for k := range fields {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)

	var parts []string
	for _, k := range append(keys, extra...) {
		if v, ok := fields[k]; ok {
			parts = append(parts, `"`+k+`":`+v)
		}
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

// lenient decodes with plain encoding/json: an independent statement of the expected field values.
func lenient(t *testing.T, raw []byte) envelope.Envelope {
	t.Helper()
	var env envelope.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("test input is not an envelope: %v", err)
	}
	return env
}

func wantDecoded(t *testing.T, raw []byte) envelope.Envelope {
	t.Helper()
	got, err := envelope.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if want := lenient(t, raw); got != want {
		t.Fatalf("Decode changed fields:\n got %+v\nwant %+v", got, want)
	}
	return got
}

// wantRejected checks the error contract and returns the message; field "" means the error belongs to no single field.
func wantRejected(t *testing.T, raw []byte, field string) string {
	t.Helper()
	got, err := envelope.Decode(raw)
	if err == nil {
		t.Fatalf("Decode accepted an invalid envelope")
	}
	if got != (envelope.Envelope{}) {
		t.Errorf("Decode returned a non-zero Envelope with an error")
	}
	if field != "" && !strings.HasPrefix(err.Error(), "envelope: "+field+":") {
		t.Errorf("error %q does not name field %q", err, field)
	}
	return err.Error()
}

type expectation struct {
	Decode   string            `json:"decode"`
	Envelope envelope.Envelope `json:"fields"`
}

type fixture struct {
	name   string
	raw    []byte
	expect expectation
}

func (f fixture) ok() bool { return f.expect.Decode == decodeOK }

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func loadFixtures(t testing.TB) []fixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*"+expectSuffix))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures in %s (err: %v)", fixtureDir, err)
	}
	var out []fixture
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), expectSuffix)
		f := fixture{name: name, raw: readFixture(t, name+".json")}
		if err := json.Unmarshal(readFixture(t, name+expectSuffix), &f.expect); err != nil {
			t.Fatalf("%s: bad expectation: %v", name, err)
		}
		out = append(out, f)
	}
	return out
}

func loadFixture(t testing.TB, name string) fixture {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.name == name {
			return f
		}
	}
	t.Fatalf("fixture %q not found", name)
	return fixture{}
}
