package gate

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
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
}

// SetupState returns the recorded state. Missing state means a first approval is due.
func (g *Gate) SetupState(ctx context.Context, r api.Repo) (SetupState, error) {
	if err := g.admitted(ctx, Target{Repo: r}); err != nil {
		return SetupState{}, err
	}
	var state records.Repo
	err := records.Load(ctx, g.v4.store, store.Tenant(r.InstallationID), records.RepoKey(r.FullName), &state)
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
