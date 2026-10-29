package report

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func TestDecodeArtifact(t *testing.T) {
	payload := loadFixture(t, baseFixture)
	sum := sha256.Sum256(payload)
	hexDigest := hex.EncodeToString(sum[:])
	other := sha256.Sum256([]byte("other"))
	notJSON := []byte("not a report")
	notJSONSum := sha256.Sum256(notJSON)

	tests := []struct {
		name     string
		payload  []byte
		digest   string
		mismatch bool
		wantErr  bool
	}{
		{"prefixed digest", payload, "sha256:" + hexDigest, false, false},
		{"bare hex digest", payload, hexDigest, false, false},
		{"another artifact's digest", payload, "sha256:" + hex.EncodeToString(other[:]), true, true},
		{"empty digest", payload, "", true, true},
		{"prefix only", payload, "sha256:", true, true},
		{"altered bytes", append(append([]byte(nil), payload...), ' '), "sha256:" + hexDigest, true, true},
		{"verified bytes that are no report", notJSON, "sha256:" + hex.EncodeToString(notJSONSum[:]), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeArtifact(tt.payload, tt.digest)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodeArtifact error = %v, want error %v", err, tt.wantErr)
			}
			if errors.Is(err, ErrDigestMismatch) != tt.mismatch {
				t.Errorf("error = %v, want digest mismatch %v", err, tt.mismatch)
			}
			if err != nil {
				if !isZero(got) {
					t.Error("an error must come with a zero Report")
				}
				return
			}
			if want := mustDecode(t, payload); !reflect.DeepEqual(got, want) {
				t.Error("the artifact decodes differently from Decode")
			}
		})
	}
}

// A kept artifact names no release and is read by the newest key set with identity optional: captures of every release
// decode, and an identity that is present is still validated.
func TestDecodeStored(t *testing.T) {
	captures := []string{
		"report/real-blocked-no-baseline.json", "report/real-pr-origin.json", "report/real-v320-blocked-no-baseline.json",
		"report/real-v322-blocked-no-baseline.json", "report/real-v323-amd64-blocked-no-baseline.json",
		"report/real-v340-arm64-blocked-no-baseline.json",
	}
	for _, name := range captures {
		t.Run(name, func(t *testing.T) {
			payload := loadFixture(t, name)
			got, err := DecodeStored(payload)
			if err != nil {
				t.Fatalf("DecodeStored: %v", err)
			}
			if want := mustDecodeFixture(t, name); !reflect.DeepEqual(got, want) {
				t.Error("the stored report decodes differently from Decode as its own release")
			}
			sum := sha256.Sum256(payload)
			if _, err := DecodeArtifact(payload, hex.EncodeToString(sum[:])); err != nil {
				t.Errorf("DecodeArtifact: %v", err)
			}
		})
	}

	rows := []mutation{
		{name: "no identity"},
		{name: "a wrong identity version", wantLoc: "identity.schema_version", edit: func(t *testing.T, d map[string]any) {
			d["identity"] = map[string]any{"schema_version": "archfit.result-identity.v2", "engine_version": "v3.4.0"}
		}},
		{name: "an identity without an engine version", wantLoc: "identity.engine_version", edit: func(t *testing.T, d map[string]any) {
			d["identity"] = map[string]any{"schema_version": ResultIdentitySchemaV1}
		}},
		{name: "an identity with a key the engine does not define", wantLoc: "identity", edit: func(t *testing.T, d map[string]any) {
			d["identity"] = map[string]any{"schema_version": ResultIdentitySchemaV1, "engine_version": "v3.4.0", "trusted": true}
		}},
		{name: "a null identity", wantLoc: "identity", edit: func(t *testing.T, d map[string]any) { d["identity"] = nil }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := parseDoc(t, loadFixture(t, "report/real-blocked-no-baseline.json"))
			if row.edit != nil {
				row.edit(t, doc)
			}
			_, err := DecodeStored(encode(t, doc))
			if row.wantLoc != "" {
				wantError(t, err, row.wantLoc)
			} else if err != nil {
				t.Fatalf("DecodeStored: %v", err)
			}
		})
	}
}
