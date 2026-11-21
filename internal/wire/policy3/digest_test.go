package policy3_test

import (
	"testing"

	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// The expected digests were computed once with `shasum -a 256` over the vendored fixture
// bytes. The engine defines its policy digest the same way (resolved.Digest, archfit #147):
// "sha256:" and the hex SHA-256 of the file bytes. No engine code runs here.
func TestDigestEqualsTheEngineDigest(t *testing.T) {
	tests := map[string]string{
		"smallest":  "sha256:17916240d7ea7a93c7a27fc2547961290093c2db7b7b777327845502fd487454",
		"typical":   "sha256:b97b969edbaaa63b92487dc438fae62ae65307d713013ed6fe24bd09c0a143c2",
		"every-key": "sha256:3905f29b47d2b12b88fe62a74a98e8d364d51b4680e8803ae29b1554b1ca95fe",
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
