package gate

import (
	"context"
	"errors"

	"github.com/coupling-dev/archfit-app/internal/gate/approvals"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

// SetupState is the recorded change-control state, not an admission verdict.
type SetupState struct {
	ApprovedDigest string
	OutsideFlow    bool
	Issue          int
}

// SetupViews reads change-control state for onboarding without exposing storage.
type SetupViews interface {
	SetupState(context.Context, api.Repo) (SetupState, error)
	SetupApproval(context.Context, api.Repo, OwnerAction) ApprovalSummary
}

// ApprovalSummary describes a signature independently of default-branch activation.
type ApprovalSummary = approvals.Summary

// Approval states used by the read-only setup view.
const (
	ApprovalNeeded  = approvals.Needed
	ApprovalPending = approvals.Pending
	ApprovalActive  = approvals.Active
	ApprovalStale   = approvals.Stale
	ApprovalUnknown = approvals.Unknown
)

// SetupApproval returns unknown on read failures; it never grants approval authority.
func (g *Gate) SetupApproval(ctx context.Context, r api.Repo, a OwnerAction) ApprovalSummary {
	if err := g.admitted(ctx, Target{Repo: r}); err != nil {
		return ApprovalSummary{Status: ApprovalUnknown, Kind: a.Kind, Pull: a.Pull}
	}
	var out ApprovalSummary
	var err error
	if snapshot, ok := ctx.Value(snapshotKey{}).(*HomeSnapshot); ok && snapshot.repo == r {
		out, err = g.approvals.SummaryWithInputs(ctx, r, a, summaryInputs{Repos: g.repos, snapshot: snapshot})
	} else {
		out, err = g.approvals.Summary(ctx, r, a)
	}
	if err != nil {
		out.Status = ApprovalUnknown
	}
	return out
}

// SetupState returns the recorded state. Missing state means a first approval is due.
func (g *Gate) SetupState(ctx context.Context, r api.Repo) (SetupState, error) {
	if err := g.admitted(ctx, Target{Repo: r}); err != nil {
		return SetupState{}, err
	}
	state, err := loadRepoRecord(ctx, g.v4.store, r.RepoRef)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return SetupState{}, err
	}
	out := SetupState{ApprovedDigest: state.ApprovedDigest}
	if state.OutsideFlow != nil {
		out.OutsideFlow = true
		out.Issue = state.OutsideFlow.Issue
	}
	return out, nil
}

// SetupPolicyChange is a display-only record, not an approval. An empty Merger
// means the merger is unknown; Direct identifies a push without a linked PR.
type SetupPolicyChange struct {
	Commit      string
	PullRequest int
	Merger      string
	Direct      bool
	Requests    []int
}

// SetupPolicyHistory reads recorded pre-approval changes oldest first. The ledger
// only contains changes observed by the App; it cannot establish complete history
// or describe semantic changes. A successful empty result is a non-nil slice.
func (g *Gate) SetupPolicyHistory(ctx context.Context, r api.Repo) ([]SetupPolicyChange, error) {
	if err := g.admitted(ctx, Target{Repo: r}); err != nil {
		return nil, err
	}
	changes, err := records.PolicyHistory(ctx, g.v4.store, store.Tenant(r.InstallationID), r.FullName)
	if err != nil {
		return nil, err
	}
	out := make([]SetupPolicyChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, SetupPolicyChange{Commit: c.Commit, PullRequest: c.PullRequest, Merger: c.Merger, Direct: c.Direct, Requests: c.Requests})
	}
	return out, nil
}
