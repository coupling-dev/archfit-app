package policy3

import (
	"crypto/sha256"
	"encoding/hex"
)

// Digest is "sha256:" and the lowercase hex SHA-256 of the exact bytes of a policy file.
// It is the engine's policy digest (design 9.1): the same bytes give the same digest in
// the engine, the Action and the App, and any change, even to a comment, changes it.
func Digest(file []byte) string {
	sum := sha256.Sum256(file)
	return "sha256:" + hex.EncodeToString(sum[:])
}
