// Package policy locates the engine's trusted input files and reads the few policy keys the App needs.
package policy

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// The engine's three trusted input files on the protected ref. Baseline and labels sit beside the policy.
const (
	PolicyPath   = ".archfit.yaml"
	BaselinePath = ".archfit-baseline.json"
	LabelsPath   = ".archfit-labels.yaml"
)

// Provenance of the copy is recorded in SCHEMA_SOURCE.
//
//go:embed archfit.schema.json
var schema []byte

// Digest returns the bare lowercase hex SHA-256 of the raw file bytes. For the policy file it equals the
// engine's comparison.config_hash. The engine reports no digest for baseline or labels; for those the
// producer states Digest of the bytes it used in the envelope, and the App compares it with the protected ref.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Schema returns the engine v2.4.0 config JSON Schema, embedded unchanged, for the editor.
func Schema() []byte {
	return bytes.Clone(schema)
}
