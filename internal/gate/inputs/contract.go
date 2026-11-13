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
	BaseRef     string // protected ref; the default branch for a push and a merge group
	BaseSHA     string // protected-ref commit, pinned for PR events and uploads when absent
	Fork        bool
	// MergeGroup marks HeadSHA as a merge group commit of BaseRef's merge queue; PullRequest is 0. Its inputs are read
	// at immutable commit SHAs (resolveGroup).
	MergeGroup bool
}

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
	Settings             settings.Settings
	SettingsFound        bool
	SettingsSHA          string    // exact commit of the protected settings snapshot when this PR was resolved
	PolicyDigests        []string  // the protected ref's digest; empty = no policy on the protected ref
	BaselineDigests      []string  // same rule; empty = no baseline on the protected ref
	LabelsDigests        []string  // same rule; empty = no labels file on the protected ref
	ProtectedPaths       []string  // protected paths this PR changes; nothing approves such a change (owner_approval_missing)
	EarliestWaiverExpiry time.Time // zero = no waivers
	Suspended            bool
	Entitlement          Entitlement
	// Group is set for a merge-group Target only: what the queue says about the group's members. nil otherwise.
	Group *Group
}

// GroupProblem is why a merge group's inputs cannot be trusted as a whole; "" when they can.
type GroupProblem string

const (
	GroupFork       GroupProblem = "fork"       // a member comes from a fork
	GroupUnverified GroupProblem = "unverified" // the queue, a member or the group commit is not what GitHub says now
	GroupConflict   GroupProblem = "conflict"   // a protected file of the group commit is changed by no queued pull request
)

// Group is the merge-group part of Trusted. Detail names members and paths ("#12 .archfit.yaml"), never content.
type Group struct {
	Problem GroupProblem
	Detail  string
	// Unapproved names each member that changes a protected path, as "#<n> <path>, <path>"; "" when there is none.
	// Nothing approves such a member.
	Unapproved string
}

type Resolver interface {
	// Resolve reads the protected ref. It never reads policy, settings, baseline, owners, or CODEOWNERS from the head
	// tree.
	Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error)
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
