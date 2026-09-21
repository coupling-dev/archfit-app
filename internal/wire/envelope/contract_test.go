package envelope_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

func TestPayloadDigestKnownVectors(t *testing.T) {
	shape := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	large := bytes.Repeat([]byte("archfit\x00"), 5<<20/8)

	tests := []struct {
		name    string
		payload []byte
		want    string // "" = shape only
	}{
		{"empty", nil, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", []byte("abc"), "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"5 MB buffer", large, ""},
		{"text with LF", []byte("a: 1\nb: 2\n"), ""},
		{"text with CRLF", []byte("a: 1\r\nb: 2\r\n"), ""},
	}
	digests := map[string]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := envelope.PayloadDigest(tt.payload)
			digests[tt.name] = got
			if tt.want != "" && got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
			if !shape.MatchString(got) {
				t.Errorf("digest %q does not match %s", got, shape)
			}
		})
	}
	if digests["text with LF"] == digests["text with CRLF"] {
		t.Errorf("LF and CRLF payloads have the same digest: line endings were normalized")
	}
}

func TestDecodeAcceptsConformanceEnvelopes(t *testing.T) {
	seen := map[string]bool{}
	for _, fx := range loadFixtures(t) {
		if !fx.ok() {
			continue
		}
		seen[fx.name] = true
		t.Run(fx.name, func(t *testing.T) {
			got, err := envelope.Decode(fx.raw)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got != fx.expect.Envelope {
				t.Errorf("got %+v\nwant %+v", got, fx.expect.Envelope)
			}
		})
	}
	for _, name := range []string{"pr-report", "push-report", "discovery-dispatch"} {
		if !seen[name] {
			t.Errorf("conformance fixture %q is missing", name)
		}
	}
}

func TestSchemaAndDecoderAgree(t *testing.T) {
	// The shared expectation format has no field key; the field each invalid fixture must name is stated here.
	// "" means the error belongs to no single field.
	rejectedField := map[string]string{
		"invalid-extra-key":                "",
		"invalid-top-level-array":          "",
		"invalid-discovery-with-baseline":  "baseline_digest",
		"invalid-missing-key":              "labels_digest",
		"invalid-null-kind":                "kind",
		"invalid-prefixed-baseline-digest": "baseline_digest",
		"invalid-push-with-pull-request":   "pull_request",
		"invalid-schema-version":           "schema_version",
	}
	for _, fx := range loadFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			_, err := envelope.Decode(fx.raw)
			if decoded := err == nil; decoded != fx.ok() {
				t.Errorf("Decode accepts = %v, expectation says %q (err: %v)", decoded, fx.expect.Decode, err)
			}
			if !fx.ok() {
				field, known := rejectedField[fx.name]
				if !known {
					t.Fatalf("no expected field recorded for the invalid fixture %q", fx.name)
				}
				wantRejected(t, fx.raw, field)
			}
			wantSchemaAgrees(t, fx.raw, err == nil)
		})
	}
}

func TestSchemaDocument(t *testing.T) {
	var doc struct {
		Schema               string                     `json:"$schema"`
		ID                   string                     `json:"$id"`
		Type                 string                     `json:"type"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(envelope.Schema(), &doc); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}
	if doc.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q", doc.Schema)
	}
	if doc.ID != "https://archfit.dev/schemas/report-envelope.v1.json" {
		t.Errorf("$id = %q", doc.ID)
	}
	if doc.Type != "object" {
		t.Errorf("type = %q", doc.Type)
	}
	if doc.AdditionalProperties == nil || *doc.AdditionalProperties {
		t.Errorf("additionalProperties must be false")
	}

	var version struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(doc.Properties["schema_version"], &version); err != nil || version.Const != envelope.SchemaV1 {
		t.Errorf("schema_version const = %q (err: %v), want %q", version.Const, err, envelope.SchemaV1)
	}

	required := map[string]bool{}
	for _, name := range doc.Required {
		required[name] = true
	}
	if len(doc.Required) != 16 || len(required) != 16 || len(doc.Properties) != 16 {
		t.Errorf("want 16 distinct required properties, got %d required, %d properties", len(doc.Required), len(doc.Properties))
	}
	for _, name := range fieldOrder() {
		if _, declared := doc.Properties[name]; !declared || !required[name] {
			t.Errorf("property %q must be declared and required", name)
		}
	}

	first, second := envelope.Schema(), envelope.Schema()
	if !bytes.Equal(first, second) {
		t.Fatalf("two calls returned different documents")
	}
	first[0] = 'X'
	if second[0] == 'X' || envelope.Schema()[0] == 'X' {
		t.Errorf("Schema() slices share memory")
	}
}

func TestPackageImportsStdlibOnly(t *testing.T) {
	goList := func(args ...string) []string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...).Output()
		if err != nil {
			t.Fatalf("go list %v: %v", args, err)
		}
		return strings.Fields(string(out))
	}

	const self = "github.com/alexei-led/archfit-app/internal/wire/envelope"
	for _, pkg := range goList("-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".") {
		if pkg != self {
			t.Errorf("non-standard-library dependency: %s", pkg)
		}
	}
	// Direct imports only: encoding/json and crypto/sha256 themselves depend on os.
	for _, pkg := range goList("-f", `{{join .Imports "\n"}}`, ".") {
		switch root, _, _ := strings.Cut(pkg, "/"); root {
		case "net", "os", "log":
			t.Errorf("forbidden import: %s", pkg)
		}
	}
}

func TestModifiedPayloadIsDetectable(t *testing.T) {
	env, err := envelope.Decode(readFixture(t, "pr-report.json"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	payload := readFixture(t, "pr-report.payload")
	if got := envelope.PayloadDigest(payload); got != env.PayloadDigest {
		t.Fatalf("untouched payload: digest %s, envelope states %s", got, env.PayloadDigest)
	}
	payload[len(payload)/2] ^= 0x01
	if envelope.PayloadDigest(payload) == env.PayloadDigest {
		t.Errorf("a modified payload still matches the stated digest")
	}
}

func TestUnsupportedTriggerStillDecodes(t *testing.T) {
	env := wantDecoded(t, render(pushFields("workflow_run")))
	if env.Event != "workflow_run" {
		t.Errorf("event = %q", env.Event)
	}
}

func TestUnknownIdentityStillDecodes(t *testing.T) {
	unlisted := "sha256:" + strings.Repeat("0", 64)
	fields := prFields()
	fields["engine_version"] = quoted("v9.9.9")
	fields["image_digest"] = quoted(unlisted)
	env := wantDecoded(t, render(fields))
	if env.EngineVersion != "v9.9.9" || env.ImageDigest != unlisted {
		t.Errorf("identity changed: %q %q", env.EngineVersion, env.ImageDigest)
	}
}

func TestProducerStatesTheInputsItUsed(t *testing.T) {
	none := loadFixture(t, "pr-report")
	env, err := envelope.Decode(none.raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if env.BaselineDigest != "" || env.LabelsDigest != "" {
		t.Errorf("no baseline and no labels must decode as empty digests, got %q %q", env.BaselineDigest, env.LabelsDigest)
	}

	both := loadFixture(t, "pr-report-with-inputs")
	env, err = envelope.Decode(both.raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	bareHex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for field, got := range map[string]string{"baseline_digest": env.BaselineDigest, "labels_digest": env.LabelsDigest} {
		if !bareHex.MatchString(got) {
			t.Errorf("%s = %q, want bare hex", field, got)
		}
	}
	if env.BaselineDigest != both.expect.Envelope.BaselineDigest || env.LabelsDigest != both.expect.Envelope.LabelsDigest {
		t.Errorf("input digests changed in decoding")
	}
}
