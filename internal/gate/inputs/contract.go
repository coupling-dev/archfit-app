// Package inputs resolves the gate's trusted inputs from the protected ref and the cached entitlement.
package inputs

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// workflowsDir restates the directory rule of wire/settings, which exports no constant for it.
const workflowsDir = ".github/workflows/"

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
	ApprovalGranted     Approval = "granted"      // a base-version owner, no agent, approved this exact head SHA
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
	// RecordPusher adds the login that pushed t's head, from a signed push delivery, to the record of who pushed it.
	// The record is attribution only: no push approves anything. "" records nothing. Opening a pull request records
	// nothing.
	RecordPusher(ctx context.Context, t Target, login string) error
	// Candidate returns the protected paths the pull request t changes, by the rule of Resolve, and the digest of each
	// at t.HeadSHA: what an owner approves in the App.
	Candidate(ctx context.Context, t Target) (Candidate, error)
	// CanApprove reports whether login may approve t's head in the App now: nil, or ErrNothingToApprove, ErrForkPull,
	// ErrAgentIdentity or ErrNotOwner, in that order. Owners and agents come from the settings of the protected ref.
	CanApprove(ctx context.Context, t Target, login string) error
	// Approve records login's approval of t's head, bound to its SHA, when CanApprove allows it and seen equals the
	// Candidate digests (else ErrDigestsChanged). A record for that head already, by any owner, is success and stays
	// as it is.
	Approve(ctx context.Context, t Target, login string, seen map[string]string, now time.Time) error
}

// Accepts is the digest predicate of the gate: whether claim is acceptable for one digest list of Trusted.
// An empty list accepts only the empty claim ("nothing trusted, nothing claimed"). A list with entries
// accepts only a claim that is not empty and is in the list, by exact string equality.
func Accepts(trusted []string, claim string) bool {
	if len(trusted) == 0 {
		return claim == ""
	}
	return claim != "" && slices.Contains(trusted, claim)
}

// First returns the first digest of a list of Trusted, or "" for an empty list.
// For every list that Resolve builds, Accepts(trusted, First(trusted)) is true.
func First(trusted []string) string {
	if len(trusted) == 0 {
		return ""
	}
	return trusted[0]
}

// ProtectedPaths returns the protected path set for the settings of the protected ref, in fixed order:
// PolicyPath, BaselinePath, LabelsPath, Path (the settings file), s.Workflow. An empty s.Workflow is left out.
func ProtectedPaths(s settings.Settings) []string {
	paths := []string{policy.PolicyPath, policy.BaselinePath, policy.LabelsPath, settings.Path}
	if s.Workflow != "" {
		paths = append(paths, s.Workflow)
	}
	return paths
}

// MayBeProtected is the same rule for a caller that has not read the settings file (a push event):
// true for every path of ProtectedPaths(Settings{}) and for every path under ".github/workflows/",
// the only directory wire/settings accepts a workflow in. It is a superset of ProtectedPaths(s) for every parsed s.
func MayBeProtected(path string) bool {
	return slices.Contains(ProtectedPaths(settings.Settings{}), path) || strings.HasPrefix(path, workflowsDir)
}
