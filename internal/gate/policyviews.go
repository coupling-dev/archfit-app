package gate

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// PolicyViews supplies the change-control state for the policy page.
type PolicyViews interface {
	PolicyState(context.Context, api.Repo) (PolicyState, error)
}

// PolicyState is the portion of repository state needed to offer editing.
type PolicyState struct {
	ApprovedDigest string
	OutsideFlow    bool
}

// PolicyState reads the repository's current change-control state.
func (g *Gate) PolicyState(ctx context.Context, repo api.Repo) (PolicyState, error) {
	if err := g.admitted(ctx, Target{Repo: repo}); err != nil {
		return PolicyState{}, err
	}
	var record records.Repo
	err := records.Load(ctx, g.store, store.Tenant(repo.InstallationID), records.RepoKey(repo.FullName), &record)
	if errors.Is(err, records.ErrNoRecord) {
		return PolicyState{}, nil
	}
	if err != nil {
		return PolicyState{}, fmt.Errorf("gate: read policy state: %w", err)
	}
	return PolicyState{ApprovedDigest: record.ApprovedDigest, OutsideFlow: record.OutsideFlow != nil}, nil
}
