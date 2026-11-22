package records

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/coupling-dev/archfit-app/internal/store"
)

// Edit is a whole-file replacement. Target is the protected tip it is made on. Unlike a request answer, an edit is
// never written again on another policy: it moves to another tip only with the same policy bytes (Rebase).
type Edit struct {
	File   []byte `json:"file"`
	Base   string `json:"base"`
	Target string `json:"target"`
}

// EditPolicy joins an edit to the existing policy proposal, atomically with its pin.
func EditPolicy(ctx context.Context, s store.Store, t store.Tenant, fullName, id string, a Approval, edit Edit, pin Pin) error {
	if id == "" || edit.Base == "" || edit.Target == "" || a.Credential == "" || pin.Proposal == "" || pin.HeadSHA == "" || pin.Digest == "" {
		return errors.New("records: edit: an empty id, base, target, passkey, head or digest")
	}
	raw, err := json.Marshal(edit)
	if err != nil {
		return err
	}
	a.Kind, a.Change, a.Request = KindEdit, raw, 0
	return joinPolicy(ctx, s, t, fullName, id, a, pin, func(r Repo) bool {
		return r.ApprovedDigest != "" && r.OutsideFlow == nil
	})
}
