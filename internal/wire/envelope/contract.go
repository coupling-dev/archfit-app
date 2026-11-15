// Package envelope is the upload-envelope v2 wire contract: what a producer states about a result.
package envelope

import (
	"crypto/sha256"
	_ "embed" // the published schema document
	"encoding/hex"
)

// PayloadDigest returns "sha256:<hex>" of payload.
func PayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return sha256Prefix + hex.EncodeToString(sum[:])
}
