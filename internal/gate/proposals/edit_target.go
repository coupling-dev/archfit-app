package proposals

import (
	"context"
	"slices"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// editTarget re-reads the protected tip, including code-only movements.
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
		if a.edit != nil && (a.edit.Target != tip || head != a.edit.Target) {
			return ErrEditChanged
		}
	}
	return nil
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

func editNote(login, passkey string) string {
	return "archfit: policy YAML edited by `" + login + "` with the passkey `" + requests.PasskeyShown(passkey) + "`.\n"
}
