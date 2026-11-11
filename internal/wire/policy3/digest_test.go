package policy3_test

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// The expected digests were computed once with `shasum -a 256` over the vendored fixture
// bytes. The engine defines its policy digest the same way (resolved.Digest, archfit #147):
// "sha256:" and the hex SHA-256 of the file bytes. No engine code runs here.
func TestDigestEqualsTheEngineDigest(t *testing.T) {
	tests := map[string]string{
		"smallest":  "sha256:c482e499dc88f1c036864461493fb14806607166ba068bed2e5513918efe0729",
		"typical":   "sha256:1cf7e56b7dc701c6f69fd8bacee1be2b8b1d241e2aea04bf6545b3ba3bcc05f7",
		"every-key": "sha256:d3542f3f605a8e1e78c0b0665e81f964e5abfbb90e9d1b1012e8bc9bfcf8e2c1",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := policy3.Digest(readFixture(t, "valid", name)); got != want {
				t.Errorf("Digest = %s, want %s", got, want)
			}
		})
	}
	if got, want := policy3.Digest(nil), "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"; got != want {
		t.Errorf("Digest(nil) = %s, want %s", got, want)
	}
}
