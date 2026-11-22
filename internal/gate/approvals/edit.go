package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/proposals"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// editParent proves that an edit landed immediately on the exact target it was
// approved against. Reading a matching policy alone cannot prove that.
func (s *Service) editParent(ctx context.Context, r api.Repo, p *proposal) (string, error) {
	if p.rec.EditVoided {
		return "", records.ErrMoved
	}
	parent := ""
	for _, id := range p.rec.Approvals {
		var a records.Approval
		if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, id), &a); err != nil {
			return "", err
		}
		if a.Kind != records.KindEdit {
			continue
		}
		var edit records.Edit
		if err := json.Unmarshal(a.Change, &edit); err != nil {
			return "", err
		}
		if a.State != records.StateOpen || edit.Target == "" || parent != "" && parent != edit.Target {
			return "", records.ErrMoved
		}
		cmp, err := s.gh.Compare(ctx, r.RepoRef, edit.Target, p.commit)
		if err != nil {
			return "", err
		}
		chain := linearRange(cmp, edit.Target, p.commit)
		if len(chain) != 1 {
			return "", records.ErrMoved
		}
		parent = edit.Target
	}
	return parent, nil
}

// retireMovedEdits also runs for code-only pushes and sweeps. An edit approval binds the policy bytes: it is voided
// only when the policy at the tip is not the one at its target, so a code-only push voids nothing (the App rebuilds the
// pull request on the tip and moves the target with it). A merged proposal is checked by editParent at consumption,
// not compared to its own new tip.
func (s *Service) retireMovedEdits(ctx context.Context, r api.Repo) error {
	release, err := proposals.Hold(ctx, r, s.store, s.now)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, tenant(r), records.ProposalIndexKey(r.FullName), &index); err != nil {
		if errors.Is(err, records.ErrNoRecord) {
			return nil
		}
		return err
	}
	for _, e := range index.Entries {
		p := &proposal{id: e.ID}
		if err := records.Load(ctx, s.store, tenant(r), records.ProposalKey(r.FullName, e.ID), &p.rec); err != nil {
			return err
		}
		if p.rec.State != records.StateOpen || p.rec.Kind != "" {
			continue
		}
		for _, id := range p.rec.Approvals {
			var a records.Approval
			if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, id), &a); err != nil {
				return err
			}
			if a.Kind != records.KindEdit || a.State != records.StateOpen {
				continue
			}
			tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
			if err != nil {
				return err
			}
			pull, err := s.gh.Pull(ctx, r.RepoRef, p.rec.PullRequest)
			if err != nil {
				return err
			}
			if pull.Merged {
				continue
			}
			var edit records.Edit
			if err := json.Unmarshal(a.Change, &edit); err != nil {
				return err
			}
			same, err := s.samePolicy(ctx, r.RepoRef, edit.Target, tip)
			if err != nil {
				return err
			}
			if same {
				continue
			}
			if err := s.retire(ctx, r, p, proposals.RetiredVoid); err != nil {
				return err
			}
			if _, _, err := records.Void(ctx, s.store, tenant(r), r.FullName, id); err != nil {
				return err
			}
			s.note(ctx, r, p.rec.PullRequest, "archfit: the policy on the default branch changed after the owner approved this "+
				"pull request, so the approval no longer holds. An owner approves it again on the Policy page.\n")
		}
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
		raw, _, err := s.gh.File(ctx, ref, at, PolicyPath)
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

func editConsumeError(err error) error {
	return fmt.Errorf("approvals: the edit's target moved or could not be proved: %w", err)
}
