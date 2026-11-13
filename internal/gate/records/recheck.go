package records

import (
	"context"
	"errors"
	"slices"

	"github.com/alexei-led/archfit-app/internal/store"
)

// Rechecked appends rc to the pull record of pull request pr, whatever run the record names now, and reports whether it
// wrote. It keeps the latest MaxRechecks. The markers belong to the pull request, not to a run: uploads keep them
// (gate's storePull), so a delivery of a consuming push again finds its proposal after the re-run's own upload and
// after later proposals. No record is left as it is (false).
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
		now.Rechecks = append(now.Rechecks, rc)
		if extra := len(now.Rechecks) - MaxRechecks; extra > 0 {
			now.Rechecks = now.Rechecks[extra:]
		}
		raw, err := Encode(&now)
		if err != nil {
			return nil, err
		}
		written = true
		return [][]byte{raw}, nil
	})
	return written, err
}

// Rechecked reports whether the pull record names a re-check after the answers of proposal.
func (v *Pull) Rechecked(proposal string) bool {
	return slices.ContainsFunc(v.Rechecks, func(r Recheck) bool { return r.Proposal == proposal })
}
