package envelope_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

// fixtureDirV2 holds the envelope 2 goldens. It uses the shared expectation format of internal/wire/module.md and
// is not part of the wire corpus scan, which decodes v1.
const (
	fixtureDirV2   = "../testdata/envelope-v2"
	standInPayload = "stand-in.payload"
)

// testPayload stands in for the result JSON v1 bytes in table tests; DecodeV2 never parses it.
var testPayload = []byte(`{"schema":"archfit.result.v1"}`)

func fieldOrderV2() []string {
	return []string{
		"schema_version", "repository", "event", "pull_request", "head_sha", "merge_base_sha", "target_ref",
		"target_sha", "policy_digest", "run_id", "run_attempt", "engine_version", "image_digest", "platform",
		"payload_digest",
	}
}

// prFieldsV2 is a valid pull_request envelope 2 for testPayload, as key -> raw JSON value.
func prFieldsV2() map[string]string {
	return map[string]string{
		"schema_version": quoted(envelope.SchemaV2),
		"repository":     `"alexei-led/archfit-demo"`,
		"event":          `"pull_request"`,
		"pull_request":   `42`,
		"head_sha":       quoted(hex40),
		"merge_base_sha": quoted(hex40),
		"target_ref":     `"refs/heads/main"`,
		"target_sha":     quoted(hex40),
		"policy_digest":  quoted("sha256:" + hex64),
		"run_id":         `17654321098`,
		"run_attempt":    `1`,
		"engine_version": `"v4.0.0"`,
		"image_digest":   quoted("sha256:" + hex64),
		"platform":       `"linux/amd64"`,
		"payload_digest": quoted(envelope.PayloadDigest(testPayload)),
	}
}

// pushFieldsV2 is a valid envelope 2 for an event that is not pull_request.
func pushFieldsV2(event string) map[string]string {
	f := prFieldsV2()
	f["event"] = quoted(event)
	f["pull_request"] = `0`
	return f
}

// renderV2 writes the object in contract key order; keys outside the contract follow, sorted.
func renderV2(fields map[string]string) []byte {
	keys := fieldOrderV2()
	var extra []string
	for k := range fields {
		if !slices.Contains(keys, k) {
			extra = append(extra, k)
		}
	}
	slices.Sort(extra)
	var parts []string
	for _, k := range append(keys, extra...) {
		if v, ok := fields[k]; ok {
			parts = append(parts, quoted(k)+":"+v)
		}
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

func wantDecodedV2(t *testing.T, raw, payload []byte) envelope.EnvelopeV2 {
	t.Helper()
	got, err := envelope.DecodeV2(raw, payload)
	if err != nil {
		t.Fatalf("DecodeV2: unexpected error: %v", err)
	}
	// Plain encoding/json is an independent statement of the expected field values.
	var want envelope.EnvelopeV2
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("test input is not an envelope: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeV2 changed fields:\n got %+v\nwant %+v", got, want)
	}
	return got
}

// wantRejectedV2 checks the error contract and returns the message; field "" means the error belongs to no single
// field.
func wantRejectedV2(t *testing.T, raw, payload []byte, field string) string {
	t.Helper()
	got, err := envelope.DecodeV2(raw, payload)
	if err == nil {
		t.Fatalf("DecodeV2 accepted an invalid envelope")
	}
	if !reflect.DeepEqual(got, envelope.EnvelopeV2{}) {
		t.Errorf("DecodeV2 returned a non-zero EnvelopeV2 with an error")
	}
	if field != "" && !strings.HasPrefix(err.Error(), "envelope: "+field+":") {
		t.Errorf("error %q does not name field %q", err, field)
	}
	return err.Error()
}

// schemaV2Accepts evaluates raw against SchemaV2Document() with the test-local evaluator of schema_eval_test.go.
func schemaV2Accepts(t *testing.T, raw []byte) bool {
	t.Helper()
	doc, err := parseJSON(envelope.SchemaV2Document())
	if err != nil {
		t.Fatalf("SchemaV2Document() is not valid JSON: %v", err)
	}
	schema, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("SchemaV2Document() is not a JSON object")
	}
	inst, err := parseJSON(raw)
	if err != nil {
		return false
	}
	return evalSchema(t, schema, inst)
}

func wantSchemaV2Agrees(t *testing.T, raw []byte, decoded bool) {
	t.Helper()
	if got := schemaV2Accepts(t, raw); got != decoded {
		t.Errorf("schema accepts = %v, DecodeV2 accepts = %v", got, decoded)
	}
}

type expectationV2 struct {
	Decode   string              `json:"decode"`
	Envelope envelope.EnvelopeV2 `json:"fields"`
}

type fixtureV2 struct {
	name   string
	raw    []byte
	expect expectationV2
}

func (f fixtureV2) ok() bool { return f.expect.Decode == decodeOK }

func readFixtureV2(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDirV2, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func loadFixturesV2(t testing.TB) []fixtureV2 {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDirV2, "*"+expectSuffix))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures in %s (err: %v)", fixtureDirV2, err)
	}
	var out []fixtureV2
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), expectSuffix)
		f := fixtureV2{name: name, raw: readFixtureV2(t, name+".json")}
		if err := json.Unmarshal(readFixtureV2(t, name+expectSuffix), &f.expect); err != nil {
			t.Fatalf("%s: bad expectation: %v", name, err)
		}
		out = append(out, f)
	}
	return out
}
