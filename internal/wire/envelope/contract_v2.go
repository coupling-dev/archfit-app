package envelope

import (
	"bytes"
	_ "embed" // the published schema document
)

// SchemaV2 is the version of upload envelope 2. Envelope 2 replaces v1 at the Archfit 4 release; the two are never
// accepted side by side there.
const SchemaV2 = "archfit.upload-envelope.v2"

// The events an envelope 2 may state. Action 4 refuses every other event before it uploads.
const (
	EventPullRequest = "pull_request"
	EventPush        = "push"
	EventMergeGroup  = "merge_group"
)

// EnvelopeV2 is what Action 4 states about one uploaded result. The payload is the engine's result JSON v1, and
// PayloadDigest names its bytes. Every field is self-reported: it must match authoritative GitHub data and is never
// trusted alone.
type EnvelopeV2 struct {
	SchemaVersion string `json:"schema_version"` // must equal SchemaV2
	Repository    string `json:"repository"`     // "owner/name"
	Event         string `json:"event"`          // EventPullRequest, EventPush or EventMergeGroup
	PullRequest   int    `json:"pull_request"`   // > 0 for a pull_request, else 0
	HeadSHA       string `json:"head_sha"`       // 40 lowercase hex: the commit the engine checked
	MergeBaseSHA  string `json:"merge_base_sha"` // 40 lowercase hex: merge-base(head, target); the head itself on a push to the target
	TargetRef     string `json:"target_ref"`     // "refs/heads/<branch>": the branch the policy was read from
	TargetSHA     string `json:"target_sha"`     // 40 lowercase hex: the target tip the policy was read from
	PolicyDigest  string `json:"policy_digest"`  // "sha256:<64 hex>" of the policy file bytes, as the result's policy.digest
	RunID         int64  `json:"run_id"`
	RunAttempt    int    `json:"run_attempt"`
	EngineVersion string `json:"engine_version"` // e.g. "v4.0.0"
	ImageDigest   string `json:"image_digest"`   // "sha256:<64 hex>"
	Platform      string `json:"platform"`       // e.g. "linux/amd64"
	PayloadDigest string `json:"payload_digest"` // "sha256:<64 hex>" of the exact result bytes
}

//go:embed envelope.v2.schema.json
var schemaV2 []byte

// SchemaV2Document returns the published JSON Schema document for SchemaV2.
func SchemaV2Document() []byte {
	return bytes.Clone(schemaV2)
}
