package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
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

// retireMovedEdits also runs for code-only pushes and sweeps. A merged proposal
// is checked by editParent at consumption, not compared to its own new tip.
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
			if edit.Target == tip {
				continue
			}
			if err := s.retire(ctx, r, p, proposals.RetiredVoid); err != nil {
				return err
			}
			if _, _, err := records.Void(ctx, s.store, tenant(r), r.FullName, id); err != nil {
				return err
			}
			s.note(ctx, r, p.rec.PullRequest, "archfit: the policy changed; reload. The YAML edit's approval is void; edit again in the App.\n")
		}
	}
	return nil
}

func editConsumeError(err error) error {
	return fmt.Errorf("approvals: the edit's target moved or could not be proved: %w", err)
}
