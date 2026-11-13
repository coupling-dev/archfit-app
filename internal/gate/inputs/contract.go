// Package inputs owns installation admission, the entitlement contract and gate target identities.
package inputs

import (
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// EntitlementKey is the Store key, under the installation tenant, of the JSON-encoded Entitlement.
const EntitlementKey = "entitlement"

// Entitlement is the cached subscription state the gate reads. control/account writes it;
// the gate never calls billing code. A missing or unreadable record means "not suspended".
type Entitlement struct {
	SuspendAfter time.Time `json:"suspend_after"` // zero = never suspends
	WarnFrom     time.Time `json:"warn_from"`     // zero = no notice; feedback shows Notice from this instant
	Notice       string    `json:"notice"`        // e.g. "Trial ends 2026-10-20"
	Reason       string    `json:"reason"`        // shown when suspended, with the recovery action
}

// Target is the pull request (or default-branch push) whose trusted inputs are resolved.
type Target struct {
	Repo        api.Repo
	PullRequest int // 0 for a default-branch push
	HeadSHA     string
	BaseRef     string // protected ref; the default branch for a push and a merge group
	BaseSHA     string // protected-ref commit, pinned for PR events and uploads when absent
	Fork        bool
	// MergeGroup marks HeadSHA as a merge group commit of BaseRef's merge queue; PullRequest is 0. Its inputs are read
	// at immutable commit SHAs (resolveGroup).
	MergeGroup bool
}
