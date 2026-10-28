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
