package proposals

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// ActionMakeRequired is the passkey step-up action that MakeRequired redeems. A token made for
// it binds the repository, the action and the digest of the policy the App writes; no client
// bytes name the policy.
const ActionMakeRequired = "make-required"

// RequiredInput binds an owner and their passkey to the change the App computes itself: the
// policy with gate set to required.
type RequiredInput struct {
	Owner  Owner
	Redeem func(context.Context, string) (string, error)
}

// MakeRequiredPreview is what the setup page shows before the passkey step-up: the digest and
// the diff of the policy change that makes the gate required. It writes nothing.
func (s *Service) MakeRequiredPreview(ctx context.Context, r api.Repo, owner Owner) (EditView, error) {
	tip, raw, _, err := s.requiredBase(ctx, r, owner)
	if err != nil {
		return EditView{}, err
	}
	next, err := s.requireGate(raw)
	if err != nil {
		return EditView{}, err
	}
	return EditView{Digest: policy3.Digest(next), Target: tip, Diff: requests.Diff(policyPath, raw, next),
		Against: AgainstDefaultBranch, Errors: []string{}}, nil
}

// MakeRequired sets gate to required in the policy on the default branch through the ordinary
// whole-file edit path: the owner's passkey binds the new digest, and the proposal, approval and
// merge machinery does the rest. When the policy already requires the gate, it is a no-op.
func (s *Service) MakeRequired(ctx context.Context, r api.Repo, owner Owner, redeem func(context.Context, string) (string, error)) (Outcome, error) {
	tip, raw, pol, err := s.requiredBase(ctx, r, owner)
	if err != nil {
		return Outcome{}, err
	}
	if pol.Gate == policy3.GateRequired {
		return Outcome{}, nil
	}
	next, err := s.requireGate(raw)
	if err != nil {
		return Outcome{}, err
	}
	if err := changesGateOnly(raw, next); err != nil {
		return Outcome{}, err
	}
	return s.Edit(ctx, r, EditInput{File: next, Base: policy3.Digest(raw), Target: tip, Owner: owner, Redeem: redeem})
}

// requiredBase reads the default-branch policy and refuses the caller unless it names them in
// owners and the repository is under change control, as an answer or an edit does.
func (s *Service) requiredBase(ctx context.Context, r api.Repo, owner Owner) (string, []byte, policy3.File, error) {
	if err := s.controlled(ctx, r, nil); err != nil {
		return "", nil, policy3.File{}, err
	}
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return "", nil, policy3.File{}, fmt.Errorf("proposals: read the default branch head: %w", err)
	}
	raw, pol, err := s.policyAt(ctx, r.RepoRef, tip)
	if err != nil {
		return "", nil, policy3.File{}, err
	}
	if err := s.controlled(ctx, r, raw); err != nil {
		return "", nil, policy3.File{}, err
	}
	if !slices.ContainsFunc(pol.Owners, func(o string) bool { return owner.Login != "" && strings.EqualFold(o, owner.Login) }) {
		return "", nil, policy3.File{}, ErrNotOwner
	}
	return tip, raw, pol, nil
}

// changesGateOnly refuses a required-gate file that alters any policy field but gate.
func changesGateOnly(before, after []byte) error {
	old, err := policy3.Decode(before)
	if err != nil {
		return err
	}
	next, err := policy3.Decode(after)
	if err != nil {
		return fmt.Errorf("proposals: the required policy does not decode: %w", err)
	}
	old.Gate = policy3.GateRequired
	if !reflect.DeepEqual(old, next) {
		return errors.New("proposals: the required change alters another policy field")
	}
	return nil
}
