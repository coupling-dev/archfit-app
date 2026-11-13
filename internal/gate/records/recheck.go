package records

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/store"
)

// Rechecked writes rc into the pull record of pull request pr, whatever run the record names now, and reports whether it
// wrote. The marker belongs to the pull request, not to a run: uploads keep it (gate's storePull), so a delivery of the
// same push again finds it after the re-run's own upload. No record is left as it is (false).
func Rechecked(ctx context.Context, s store.Store, t store.Tenant, fullName string, pr int, rc Recheck) (bool, error) {
	if rc.Proposal == "" || rc.Path == "" {
		return false, errors.New("records: recheck: an empty proposal or path")
	}
	written := false
	err := store.Update(ctx, s, t, []string{PullKey(fullName, pr)}, func(cur [][]byte) ([][]byte, error) {
		written = false
		if cur[0] == nil {
			return cur, nil
		}
		var now Pull
		if err := Decode(cur[0], &now); err != nil {
			return nil, err
		}
		now.Recheck = &rc
		raw, err := Encode(&now)
		if err != nil {
			return nil, err
		}
		written = true
		return [][]byte{raw}, nil
	})
	return written, err
}
