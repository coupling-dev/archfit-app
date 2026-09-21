// Package inputs resolves the gate's trusted inputs from the protected ref and the cached entitlement.
package inputs

import (
	"context"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
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
	BaseRef     string // protected ref; the default branch for a push
	Fork        bool
}

type Approval string

const (
	ApprovalNotRequired Approval = "not_required" // no protected path changed
	ApprovalGranted     Approval = "granted"      // a base-version owner approved this exact head SHA
	ApprovalMissing     Approval = "missing"
)

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
	Settings             settings.Settings
	SettingsFound        bool
	PolicyDigests        []string // base digest, plus the head digest when Approval == ApprovalGranted
	BaselineDigests      []string // same rule; empty = no baseline on the protected ref
	LabelsDigests        []string // same rule; empty = no labels file on the protected ref
	ProtectedPaths       []string // protected paths this PR changes
	Approval             Approval
	ApprovedBy           string
	EarliestWaiverExpiry time.Time // zero = no waivers
	Suspended            bool
	Entitlement          Entitlement
}

type Resolver interface {
	// Resolve reads the protected ref and reviews. It never reads policy, settings, baseline,
	// owners, or CODEOWNERS from the head tree, except to hash an owner-approved candidate.
	Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error)
}

func New(repos api.Repos, pulls api.Pulls, s store.Store) Resolver {
	return nil
}
