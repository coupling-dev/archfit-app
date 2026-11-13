package records

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/store"
)

// Rechecked writes rc into the pull record of pull request pr while that record is still was: the same head, run and
// attempt. It reports whether it wrote. A record that an upload replaced meanwhile, or none at all, is left as it is:
// the newer upload is the fresh result the re-check was for.
func Rechecked(ctx context.Context, s store.Store, t store.Tenant, fullName string, pr int, was Pull, rc Recheck) (bool, error) {
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
		if now.HeadSHA != was.HeadSHA || now.RunID != was.RunID || now.RunAttempt != was.RunAttempt {
			return cur, nil
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
