package records

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/store"
)

// RecoveredDelivery records the real delivery range against immutable recovered-merge evidence, without changing
// repository authority or consuming an approval again.
func RecoveredDelivery(ctx context.Context, s store.Store, t store.Tenant, name, commit string, push Push, proof PolicyPush) error {
	keys := []string{RecoveredPushKey(name, commit), PolicyPushKey(name, push)}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return nil, ErrMoved
		}
		var now PolicyPush
		if err := Decode(cur[0], &now); err != nil {
			return nil, err
		}
		if now != proof {
			return nil, ErrMoved
		}
		if cur[1] != nil {
			var was PolicyPush
			if err := Decode(cur[1], &was); err != nil {
				return nil, err
			}
			if was != proof {
				return nil, errors.New("records: recovered delivery has another judgement")
			}
			return cur, nil
		}
		return [][]byte{cur[0], cur[0]}, nil
	})
}

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
