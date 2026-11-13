package records

import (
	"context"
	"github.com/alexei-led/archfit-app/internal/store"
)

// SweepRechecked clears a recovered consume's handoff only while it still names proposal.
func SweepRechecked(ctx context.Context, s store.Store, t store.Tenant, name, proposal string) error {
	return editRepo(ctx, s, t, name, func(r *Repo) bool {
		if r.SweepRecheck != proposal {
			return false
		}
		r.SweepRecheck = ""
		return true
	})
}
