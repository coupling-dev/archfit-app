package records

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/store"
)

// StoreDrift replaces the latest push result without changing approvals or notification state.
// The caller admits only accepted results at the default branch tip.
func StoreDrift(ctx context.Context, s store.Store, t store.Tenant, name string, state DriftState) error {
	// Unique triples suffice for the map. Keep room for existing repository fields by checking
	// the complete encoded record inside the CAS, and mark omissions as incomplete.
	seen := make(map[DriftError]struct{}, len(state.Errors))
	unique := make([]DriftError, 0, len(state.Errors))
	for _, e := range state.Errors {
		if _, ok := seen[e]; !ok {
			seen[e] = struct{}{}
			unique = append(unique, e)
		}
	}
	state.Errors = unique
	return store.Update(ctx, s, t, []string{RepoKey(name)}, func(cur [][]byte) ([][]byte, error) {
		var r Repo
		if cur[0] != nil {
			if err := Decode(cur[0], &r); err != nil {
				return nil, err
			}
		}
		r.Drift = &state
		raw, err := Encode(&r)
		if errors.Is(err, ErrTooLarge) {
			// Binary search the largest prefix that fits. Encoding the whole Repo protects unrelated fields.
			state.Unread = true
			all := state.Errors
			lo, hi := 0, len(all)
			for lo < hi {
				mid := lo + (hi-lo+1)/2
				state.Errors = all[:mid]
				r.Drift = &state
				_, e := Encode(&r)
				if e == nil {
					lo = mid
				} else if errors.Is(e, ErrTooLarge) {
					hi = mid - 1
				} else {
					return nil, e
				}
			}
			state.Errors = all[:lo]
			r.Drift = &state
			raw, err = Encode(&r)
		}
		if err != nil {
			return nil, err
		}
		return [][]byte{raw}, nil
	})
}
