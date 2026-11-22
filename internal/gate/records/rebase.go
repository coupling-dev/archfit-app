package records

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/coupling-dev/archfit-app/internal/store"
)

// Rebase pins an open proposal to headSHA, a new commit of the same policy bytes (digest) on target, the default
// branch's head, and moves the Target of each of its edit approvals to target, in one swap: an approval binds the
// policy bytes, and the policy at target is the one each edit was made on (the caller proved it). A proposal that is
// not open, whose edit epoch moved, that holds a void edit, or that expects another digest is ErrMoved; so is an edit
// that is not open or not in it.
func Rebase(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID, headSHA, digest, target string,
	editEpoch uint64, edits []string) error {
	if proposalID == "" || headSHA == "" || digest == "" || target == "" {
		return errors.New("records: rebase: an empty id, head, digest or target")
	}
	keys := []string{ProposalKey(fullName, proposalID)}
	for _, id := range edits {
		keys = append(keys, ApprovalKey(fullName, id))
	}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return nil, ErrNoRecord
		}
		var p Proposal
		if err := Decode(cur[0], &p); err != nil {
			return nil, err
		}
		if p.State != StateOpen || p.EditEpoch != editEpoch || p.EditVoided || p.ExpectedDigest != digest {
			return nil, ErrMoved
		}
		next := slices.Clone(cur)
		for i, id := range edits {
			raw := cur[i+1]
			if raw == nil {
				return nil, ErrNoRecord
			}
			var a Approval
			if err := Decode(raw, &a); err != nil {
				return nil, err
			}
			var e Edit
			if err := json.Unmarshal(a.Change, &e); err != nil {
				return nil, err
			}
			if a.Kind != KindEdit || a.State != StateOpen || a.Proposal != proposalID || !slices.Contains(p.Approvals, id) {
				return nil, ErrMoved
			}
			e.Target = target
			change, err := json.Marshal(e)
			if err != nil {
				return nil, err
			}
			a.Change = change
			if next[i+1], err = Encode(&a); err != nil {
				return nil, err
			}
		}
		p.HeadSHA = headSHA
		var err error
		if next[0], err = Encode(&p); err != nil {
			return nil, err
		}
		return next, nil
	})
}
