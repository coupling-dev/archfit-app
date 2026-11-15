package proposals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// RevertView is the exact file a revert writes, including previously approved answers.
// The input bindings let a caller refuse a changed preview before redeeming a passkey.
type RevertView struct {
	Digest                    string
	ApprovedCommit            string
	Tip                       string
	Approved, Outside, Result []byte
	Proposals                 []RevertProposal
}

// RevertProposal identifies the App's policy pull request and its ordered approval inputs.
// A repository has at most one open App proposal, which can hold several answers.
type RevertProposal struct {
	ID        string
	Pull      int
	Head      string
	Digest    string
	Approvals []RevertApproval
}

// RevertApproval binds an input approval, including ones the writer will void.
type RevertApproval struct {
	ID      string
	Request int
	Digest  string
}

// revertPolicy is the shared computation of the action and its read-only preview.
func revertPolicy(approvedFile, tipFile []byte, cur *proposal) ([]byte, []approved, []approved, error) {
	if cur == nil {
		return approvedFile, nil, nil, nil
	}
	snapshot := *cur
	snapshot.base = approvedFile
	file, kept, voided, err := apply(tipFile, &snapshot)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(kept) >= records.MaxApprovals {
		return nil, nil, nil, ErrFull
	}
	return file, kept, voided, nil
}

// RevertPreview reads the same inputs as Revert without its lease or cleanup writes.
func (s *Service) RevertPreview(ctx context.Context, r api.Repo) (RevertView, error) {
	rev, err := s.revertOf(ctx, r)
	if err != nil {
		return RevertView{}, err
	}
	approvedFile, err := s.approvedPolicy(ctx, r.RepoRef, rev)
	if err != nil {
		return RevertView{}, err
	}
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return RevertView{}, err
	}
	outside, _, err := s.gh.File(ctx, r.RepoRef, tip, policyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return RevertView{}, err
	}
	if digestOf(outside) != rev.Outside {
		return RevertView{}, ErrMoved
	}
	cur, err := s.readCurrent(ctx, r, false)
	if err != nil {
		return RevertView{}, err
	}
	file, _, _, err := revertPolicy(approvedFile, outside, cur)
	if err != nil {
		return RevertView{}, err
	}
	v := RevertView{Digest: revertDigest(rev), ApprovedCommit: rev.Commit, Tip: tip, Approved: approvedFile, Outside: outside, Result: file,
		Proposals: []RevertProposal{}}
	if cur != nil {
		p := RevertProposal{ID: cur.id, Pull: cur.rec.PullRequest, Head: cur.rec.HeadSHA, Digest: snapshotDigest(cur.rec), Approvals: []RevertApproval{}}
		p.Approvals = cur.inputs
		v.Proposals = append(v.Proposals, p)
	}
	return v, nil
}

func snapshotDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
