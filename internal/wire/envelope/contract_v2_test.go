package envelope_test

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

func TestDecodeV2Goldens(t *testing.T) {
	// The shared expectation format has no field key; the field each invalid golden must name is stated here.
	// "" means the error belongs to no single field.
	rejectedField := map[string]string{
		"invalid-unknown-key":            "",
		"invalid-kind":                   "",
		"invalid-baseline-digest":        "",
		"invalid-schema-version":         "schema_version",
		"invalid-payload-digest":         "payload_digest",
		"invalid-missing-target-sha":     "target_sha",
		"invalid-bare-policy-digest":     "policy_digest",
		"invalid-event":                  "event",
		"invalid-push-with-pull-request": "pull_request",
		"invalid-target-ref":             "target_ref",
	}
	// A schema cannot see the payload bytes.
	schemaBlind := map[string]bool{"invalid-payload-digest": true}

	payload := readFixtureV2(t, standInPayload)
	seen := map[string]bool{}
	for _, fx := range loadFixturesV2(t) {
		seen[fx.name] = true
		t.Run(fx.name, func(t *testing.T) {
			if fx.ok() {
				if got := wantDecodedV2(t, fx.raw, payload); !reflect.DeepEqual(got, fx.expect.Envelope) {
					t.Errorf("got %+v\nwant %+v", got, fx.expect.Envelope)
				}
			} else {
				field, known := rejectedField[fx.name]
				if !known {
					t.Fatalf("no expected field recorded for the invalid golden %q", fx.name)
				}
				wantRejectedV2(t, fx.raw, payload, field)
			}
			if schemaBlind[fx.name] {
				wantSchemaV2Agrees(t, fx.raw, true)
				return
			}
			wantSchemaV2Agrees(t, fx.raw, fx.ok())
		})
	}
	for _, name := range []string{"pull-request", "push", "merge-group", "rerun"} {
		if !seen[name] {
			t.Errorf("golden %q is missing", name)
		}
	}
	for name := range rejectedField {
		if !seen[name] {
			t.Errorf("invalid golden %q is missing", name)
		}
	}
}

func TestGoldensV2Layout(t *testing.T) {
	entries, err := os.ReadDir(fixtureDirV2)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("%s: folders are not allowed", e.Name())
		}
		files[e.Name()] = true
	}
	for file := range files {
		switch name, isExpect := strings.CutSuffix(file, expectSuffix); {
		case file == standInPayload:
		case isExpect:
			if !files[name+".json"] {
				t.Errorf("%s has no golden %s.json", file, name)
			}
		case strings.HasSuffix(file, ".json"):
			if !files[strings.TrimSuffix(file, ".json")+expectSuffix] {
				t.Errorf("%s has no expectation", file)
			}
		default:
			t.Errorf("%s: only goldens, expectations and %s belong here", file, standInPayload)
		}
	}
}

func TestReRunGoldenIsTheSameRun(t *testing.T) {
	first, rerun := loadFixtureV2(t, "pull-request"), loadFixtureV2(t, "rerun")
	a, b := first.expect.Envelope, rerun.expect.Envelope
	if a.RunID != b.RunID || a.HeadSHA != b.HeadSHA || b.RunAttempt != 2 {
		t.Errorf("rerun must be attempt 2 of the same run on the same head")
	}
	if a.TargetSHA == b.TargetSHA {
		t.Errorf("a re-run reads the target tip again; the golden must show a moved target")
	}
}

func loadFixtureV2(t *testing.T, name string) fixtureV2 {
	t.Helper()
	for _, f := range loadFixturesV2(t) {
		if f.name == name {
			return f
		}
	}
	t.Fatalf("golden %q not found", name)
	return fixtureV2{}
}

func TestSchemaV2MatchesGoType(t *testing.T) {
	var doc struct {
		Schema               string                     `json:"$schema"`
		ID                   string                     `json:"$id"`
		Type                 string                     `json:"type"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(envelope.SchemaV2Document(), &doc); err != nil {
		t.Fatalf("SchemaV2Document() is not valid JSON: %v", err)
	}
	if doc.Schema != "https://json-schema.org/draft/2020-12/schema" || doc.Type != "object" ||
		doc.ID != "https://archfit.dev/schemas/upload-envelope.v2.json" {
		t.Errorf("$schema %q, $id %q, type %q", doc.Schema, doc.ID, doc.Type)
	}
	if doc.AdditionalProperties == nil || *doc.AdditionalProperties {
		t.Errorf("additionalProperties must be false")
	}

	var tags []string
	typ := reflect.TypeFor[envelope.EnvelopeV2]()
	for f := range typ.Fields() {
		tags = append(tags, f.Tag.Get("json"))
	}
	if !slices.Equal(tags, fieldOrderV2()) {
		t.Errorf("fieldOrderV2 does not follow the EnvelopeV2 field order")
	}
	if !slices.Equal(doc.Required, tags) {
		t.Errorf("schema required %v, Go json tags %v", doc.Required, tags)
	}
	props := slices.Sorted(maps.Keys(doc.Properties))
	if slices.Sort(tags); !slices.Equal(props, tags) {
		t.Errorf("schema properties %v, Go json tags %v", props, tags)
	}

	var version struct {
		Const string `json:"const"`
	}
	var event struct {
		Enum []string `json:"enum"`
	}
	if json.Unmarshal(doc.Properties["schema_version"], &version) != nil || version.Const != envelope.SchemaV2 {
		t.Errorf("schema_version const = %q, want %q", version.Const, envelope.SchemaV2)
	}
	wantEvents := []string{envelope.EventPullRequest, envelope.EventPush, envelope.EventMergeGroup}
	if json.Unmarshal(doc.Properties["event"], &event) != nil || !slices.Equal(event.Enum, wantEvents) {
		t.Errorf("event enum = %v, want %v", event.Enum, wantEvents)
	}

	a, b := envelope.SchemaV2Document(), envelope.SchemaV2Document()
	a[0] = 'X'
	if b[0] == 'X' {
		t.Errorf("SchemaV2Document returns shared bytes")
	}
}

func TestDecodeV2BindsThePayload(t *testing.T) {
	raw := renderV2(prFieldsV2())
	wantDecodedV2(t, raw, testPayload)

	t.Run("one changed byte", func(t *testing.T) {
		changed := slices.Clone(testPayload)
		changed[len(changed)/2] ^= 0x01
		wantRejectedV2(t, raw, changed, "payload_digest")
	})
	t.Run("no payload", func(t *testing.T) {
		wantRejectedV2(t, raw, nil, "payload_digest")
	})
	t.Run("empty payload with the empty digest", func(t *testing.T) {
		fields := prFieldsV2()
		fields["payload_digest"] = quoted(envelope.PayloadDigest(nil))
		wantDecodedV2(t, renderV2(fields), []byte{})
	})
	t.Run("line endings are bytes", func(t *testing.T) {
		lf, crlf := []byte("a: 1\nb: 2\n"), []byte("a: 1\r\nb: 2\r\n")
		fields := prFieldsV2()
		fields["payload_digest"] = quoted(envelope.PayloadDigest(lf))
		wantDecodedV2(t, renderV2(fields), lf)
		wantRejectedV2(t, renderV2(fields), crlf, "payload_digest")
	})
}
