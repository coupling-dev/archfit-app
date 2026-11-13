package proposals

import (
	"context"
	"slices"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// EditorBase is the recorded proposal file, or the protected target without one.
// Recovery means the live branch is not the recorded, applicable proposal.
type EditorBase struct {
	File     []byte
	Digest   string
	Recovery bool
}

// Editor reads only recorded authority. An unrecorded pull never supplies bytes.
func (s *Service) Editor(ctx context.Context, r api.Repo) (EditorBase, error) {
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return EditorBase{}, err
	}
	raw, _, err := s.policyAt(ctx, r.RepoRef, tip)
	if err != nil {
		return EditorBase{}, err
	}
	if err := s.controlled(ctx, r, raw); err != nil {
		return EditorBase{}, err
	}
	cur, err := s.readCurrent(ctx, r, false)
	if err != nil {
		return EditorBase{}, err
	}
	if cur == nil {
		return EditorBase{File: raw, Digest: policy3.Digest(raw)}, nil
	}
	raw, _, err = s.gh.File(ctx, r.RepoRef, cur.rec.HeadSHA, policyPath)
	if err != nil {
		return EditorBase{}, err
	}
	if policy3.Digest(raw) != cur.rec.ExpectedDigest {
		return EditorBase{}, ErrEditChanged
	}
	head, err := s.gh.BranchHead(ctx, r.RepoRef, Branch)
	if err != nil {
		return EditorBase{}, err
	}
	pull, err := s.gh.Pull(ctx, r.RepoRef, cur.rec.PullRequest)
	if err != nil {
		return EditorBase{}, err
	}
	if pull.HeadRef != Branch || !strings.EqualFold(pull.HeadRepo, r.FullName) || pull.BaseRef != r.DefaultBranch {
		return EditorBase{}, ErrEditChanged
	}
	return EditorBase{File: raw, Digest: cur.rec.ExpectedDigest,
		Recovery: head != cur.rec.HeadSHA || pull.HeadSHA != cur.rec.HeadSHA || cur.rec.EditVoided || len(cur.stale) > 0}, nil
}

// ReloadEditor explicitly restores only recorded approvals under the existing
// proposal lease. It never approves new bytes or merges. Preview stays read-only.
func (s *Service) ReloadEditor(ctx context.Context, r api.Repo, owner Owner) (EditorBase, error) {
	release, err := s.hold(ctx, r)
	if err != nil {
		return EditorBase{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return EditorBase{}, err
	}
	raw, pol, err := s.policyAt(ctx, r.RepoRef, tip)
	if err != nil {
		return EditorBase{}, err
	}
	if err := s.controlled(ctx, r, raw); err != nil {
		return EditorBase{}, err
	}
	if !slices.ContainsFunc(pol.Owners, func(login string) bool { return owner.Login != "" && strings.EqualFold(login, owner.Login) }) {
		return EditorBase{}, ErrNotOwner
	}
	cur, err := s.current(ctx, r)
	if err != nil {
		return EditorBase{}, err
	}
	view, err := s.Editor(ctx, r)
	if err != nil {
		return EditorBase{}, err
	}
	if cur == nil || !view.Recovery {
		return view, nil
	}
	file, kept, voided, err := apply(raw, cur)
	if err != nil {
		return EditorBase{}, err
	}
	if err := s.voidAndNote(ctx, r, cur.rec.PullRequest, voided, false); err != nil {
		return EditorBase{}, err
	}
	pin := s.resync(ctx, r, cur, tip, file, kept)
	if pin != nil {
		s.publish(ctx, r, tip, *pin, kept, true)
	} else if len(kept) > 0 {
		return EditorBase{}, ErrEditChanged
	}
	return s.Editor(ctx, r)
}
