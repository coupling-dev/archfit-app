package records

import (
	"context"
	"slices"

	"github.com/alexei-led/archfit-app/internal/store"
)

// StoreDrift replaces the latest push result without changing approvals or notification state.
// The caller admits only accepted results at the default branch tip.
func StoreDrift(ctx context.Context, s store.Store, t store.Tenant, name string, state DriftState) error {
	return editRepo(ctx, s, t, name, func(r *Repo) bool {
		if r.Drift != nil && r.Drift.Head == state.Head && r.Drift.State == state.State && r.Drift.Unread == state.Unread && slices.Equal(r.Drift.Errors, state.Errors) {
			return false
		}
		r.Drift = &state
		return true
	})
}
