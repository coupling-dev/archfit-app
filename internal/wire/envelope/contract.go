// Package envelope is the report-envelope v1 wire contract: what a producer states about a payload.
package envelope

import (
	"bytes"
	"crypto/sha256"
	_ "embed" // the published schema document
	"encoding/hex"
)

// SchemaV1 is the only envelope schema version accepted.
const SchemaV1 = "archfit.report-envelope.v1"

// Kind names the payload that accompanies the envelope.
type Kind string

const (
	KindReport    Kind = "report"    // engine state report, POST /v1/reports
	KindDiscovery Kind = "discovery" // discovery draft policy, POST /v1/discoveries
	KindBaseline  Kind = "baseline"  // captured engine baseline file, POST /v1/baselines
)

// Envelope is what the producer states about a payload. Every field is self-reported:
// it must match authoritative GitHub data and is never trusted alone.
type Envelope struct {
	SchemaVersion  string `json:"schema_version"` // must equal SchemaV1
	Kind           Kind   `json:"kind"`
	Repository     string `json:"repository"`     // "owner/name"
	Event          string `json:"event"`          // "pull_request" | "push" | "workflow_dispatch"
	PullRequest    int    `json:"pull_request"`   // 0 unless Event == "pull_request"
	HeadSHA        string `json:"head_sha"`       // 40 lowercase hex
	BaseSHA        string `json:"base_sha"`       // "" unless pull_request
	MergeBaseSHA   string `json:"merge_base_sha"` // "" unless pull_request
	RunID          int64  `json:"run_id"`
	RunAttempt     int    `json:"run_attempt"`
	EngineVersion  string `json:"engine_version"`  // e.g. "v2.3.1"
	ImageDigest    string `json:"image_digest"`    // "sha256:<64 hex>"
	Platform       string `json:"platform"`        // e.g. "linux/amd64"
	PayloadDigest  string `json:"payload_digest"`  // "sha256:<64 hex>" of the exact payload bytes
	BaselineDigest string `json:"baseline_digest"` // bare hex SHA-256 of the baseline file the engine loaded; "" = none; always "" for discovery and baseline
	LabelsDigest   string `json:"labels_digest"`   // bare hex SHA-256 of the labels file the engine loaded; "" = none
}

//go:embed envelope.v1.schema.json
var schemaV1 []byte

// PayloadDigest returns "sha256:<hex>" of payload.
func PayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return sha256Prefix + hex.EncodeToString(sum[:])
}

// Schema returns the published JSON Schema document for SchemaV1.
func Schema() []byte {
	return bytes.Clone(schemaV1)
}
