package proposals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// editTarget re-reads the protected tip. An edit approval binds the policy bytes: it still holds while the policy at
// the tip is the one at its target, so a code-only movement voids nothing. The tip must still be the one this action
// read; when it moved, the action stops without a void and a later pass brings the pull request up to date.
func (s *Service) editTarget(ctx context.Context, r api.Repo, tip string, listed []approved) error {
	if !slices.ContainsFunc(listed, func(a approved) bool { return a.edit != nil }) {
		return nil
	}
	head, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return err
	}
	for _, a := range listed {
		if a.edit != nil {
			var recorded records.Approval
			if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, a.id), &recorded); err != nil {
				return err
			}
			if recorded.State != records.StateOpen {
				return ErrEditChanged
			}
		}
		if a.edit == nil {
			continue
		}
		same, err := s.samePolicy(ctx, r.RepoRef, a.edit.Target, head)
		if err != nil {
			return err
		}
		if !same {
			return ErrEditChanged
		}
	}
	if head != tip {
		return fmt.Errorf("%w: the default branch moved", errRefused)
	}
	return nil
}

// samePolicy reports whether commits a and b hold the same policy bytes. A missing policy is a policy too: it equals
// only another missing one.
func (s *Service) samePolicy(ctx context.Context, ref api.RepoRef, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	read := func(at string) ([]byte, bool, error) {
		raw, _, err := s.gh.File(ctx, ref, at, policyPath)
		if errors.Is(err, api.ErrNotFound) {
			return nil, false, nil
		}
		return raw, err == nil, err
	}
	x, xOK, err := read(a)
	if err != nil {
		return false, err
	}
	y, yOK, err := read(b)
	if err != nil {
		return false, err
	}
	return xOK == yOK && bytes.Equal(x, y), nil
}

func (s *Service) voidMovedEdits(ctx context.Context, r api.Repo, pin records.Pin, listed []approved) {
	var edits []approved
	for _, a := range listed {
		if a.edit != nil {
			edits = append(edits, a)
		}
	}
	if len(edits) == 0 {
		return
	}
	if err := Retire(ctx, s.gh, r, pin.HeadSHA, pin.PullRequest, pin.Proposal, RetiredVoid, s.now()); err != nil {
		logFailure(ctx, r, "retire the moved edit", err)
		return
	}
	if err := s.voidAndNote(ctx, r, pin.PullRequest, edits, false); err != nil {
		logFailure(ctx, r, "void the moved edit", err)
	}
}

func editNote(login, passkey string, asIs bool) string {
	what := "policy edited"
	if asIs {
		what = "the policy pull request approved again as it is"
	}
	return "archfit: " + what + " by `" + login + "` with the passkey `" + requests.PasskeyShown(passkey) + "`.\n"
}
