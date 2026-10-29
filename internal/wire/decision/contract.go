// Package decision is the decision-request v1 wire contract: what an unattended agent files, as a pull request
// comment, to ask the architecture owners for a policy change.
package decision

import (
	"bytes"
	_ "embed" // the published schema document
	"strconv"
)

// SchemaV1 is the only decision-request schema version accepted.
const SchemaV1 = "archfit.decision-request.v1"

// ProposalPolicyPatch is the only proposal kind of v1: a whole candidate policy file.
const ProposalPolicyPatch = "policy_patch"

// Request is what the agent states. Every field is self-reported and never trusted alone.
type Request struct {
	SchemaVersion     string   `json:"schema_version"`
	FindingID         string   `json:"finding_id"` // 32 lowercase hex: the engine's finding ID
	RuleID            string   `json:"rule_id"`
	Edge              Edge     `json:"edge"`
	HeadSHA           string   `json:"head_sha"`      // 40 lowercase hex: the commit the request is about
	PolicyDigest      string   `json:"policy_digest"` // 64 lowercase hex: the policy the finding was measured under
	Why               string   `json:"why"`
	AlternativesTried []string `json:"alternatives_tried"`
	Proposal          Proposal `json:"proposal"`
}

// Edge is the dependency the finding is about, as module names.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Proposal is the change asked for.
type Proposal struct {
	Kind   string `json:"kind"`   // ProposalPolicyPatch
	Policy string `json:"policy"` // the full candidate .archfit.yaml
}

//go:embed decision-request.v1.schema.json
var schema []byte

// Schema returns the published JSON Schema document for SchemaV1, as a fresh copy.
func Schema() []byte { return bytes.Clone(schema) }

// Status is where a request stands, as the App's status comment says it.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDeclined Status = "declined"
	StatusStale    Status = "stale"
)

// The reasons a request goes stale.
const (
	ReasonHeadMoved     = "head moved"
	ReasonPolicyChanged = "policy changed"
	ReasonPullClosed    = "pull request closed"
	ReasonBaseChanged   = "base branch changed" // the pull request no longer targets the default branch
)

// ReasonOwnerDeclined is the reason of a request an owner declined.
const (
	ReasonOwnerDeclined = "by an owner"
)

// Unknown stands in the status marker for an ID the request did not state in a valid form.
const Unknown = "-"

// StatusMarkerPrefix starts the first line of every status comment.
const StatusMarkerPrefix = "<!-- archfit:decision-status "

// StatusMarker is the first line of the App's status comment for one request. The agent finds its answer by it.
func StatusMarker(findingID, policyDigest string) string {
	return StatusMarkerPrefix + findingID + " " + policyDigest + " -->"
}

// ApprovedDetail is the detail of an approved status: the proposal pull request the App opened.
func ApprovedDetail(proposalPR int) string { return "proposal PR #" + strconv.Itoa(proposalPR) }

// StatusLine is the second line of the status comment: the status, and the detail in parentheses when there is one:
// "pending", "approved (proposal PR #7)", "declined", "stale (head moved)".
func StatusLine(s Status, detail string) string {
	if detail == "" {
		return string(s)
	}
	return string(s) + " (" + detail + ")"
}
