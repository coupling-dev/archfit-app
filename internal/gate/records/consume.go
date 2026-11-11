package records

import (
	"context"
	"errors"
	"slices"

	"github.com/alexei-led/archfit-app/internal/store"
)

var (
	// ErrMoved is returned when a record is not in the state the step needs: a proposal that is void, consumed by
	// another commit, or expects another digest; an approval or a proposal that is no longer open.
	ErrMoved = errors.New("records: the record moved")
	// ErrJoined is returned when an approval already joined another proposal. An approval is used once.
	ErrJoined = errors.New("records: the approval already joined another proposal")
	// ErrFull is returned when a proposal already holds MaxApprovals approvals.
	ErrFull = errors.New("records: the proposal is full")
)

// Join adds an open approval to an open proposal, in one CompareAndSwapAll of both keys. The approval records the
// proposal once: joining the same proposal again changes nothing, and any other proposal is ErrJoined.
func Join(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID, proposalID string) error {
	if approvalID == "" || proposalID == "" {
		return errors.New("records: join: an empty id")
	}
	keys := []string{ApprovalKey(fullName, approvalID), ProposalKey(fullName, proposalID)}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil || cur[1] == nil {
			return nil, ErrNoRecord
		}
		var a Approval
		var p Proposal
		if err := Decode(cur[0], &a); err != nil {
			return nil, err
		}
		if err := Decode(cur[1], &p); err != nil {
			return nil, err
		}
		switch {
		case a.Proposal == proposalID && slices.Contains(p.Approvals, approvalID):
			return cur, nil
		case a.Proposal != "":
			return nil, ErrJoined
		case a.State != StateOpen || p.State != StateOpen:
			return nil, ErrMoved
		case len(p.Approvals) >= MaxApprovals:
			return nil, ErrFull
		}
		a.Proposal = proposalID
		p.Approvals = append(p.Approvals, approvalID)
		return encodeAll(&a, &p)
	})
}

// Consume is the one-use step of a proposal: its merge commit landed on the default branch with the expected
// digest. In one CompareAndSwapAll it moves the proposal from open to merged and records commit, removes the
// proposal from the proposal index, and makes digest the repository's approved digest. The approvals of the
// proposal count as consumed from then on (each records the proposal it joined, once).
//
// The same commit again is a no-op success: it only checks the keys. A proposal that is void, merged by another
// commit, without a pull request, or expecting another digest is ErrMoved; a missing one is ErrNoRecord. The state
// is decided again after every lost swap, so of concurrent callers with different commits exactly one wins.
func Consume(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID, commit, digest string) error {
	if proposalID == "" || commit == "" || digest == "" {
		return errors.New("records: consume: an empty proposal id, commit or digest")
	}
	keys := []string{ProposalKey(fullName, proposalID), ProposalIndexKey(fullName), RepoKey(fullName)}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return nil, ErrNoRecord
		}
		var p Proposal
		if err := Decode(cur[0], &p); err != nil {
			return nil, err
		}
		switch {
		case p.State == StateMerged && p.Commit == commit:
			return cur, nil
		case p.State != StateOpen || p.PullRequest == 0 || p.ExpectedDigest != digest:
			return nil, ErrMoved
		}
		p.State, p.Commit = StateMerged, commit

		x, err := readIndex(cur[1], SchemaProposalIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(x.Entries)
		if id, ok := x.Lookup(PullEntry(p.PullRequest)); ok && id == proposalID {
			x.Remove(PullEntry(p.PullRequest))
		}
		idx, err := writeIndex(cur[1], before, x)
		if err != nil {
			return nil, err
		}

		var r Repo
		if cur[2] != nil {
			if err := Decode(cur[2], &r); err != nil {
				return nil, err
			}
		}
		r.ApprovedDigest = digest

		out, err := encodeAll(&p, &r)
		if err != nil {
			return nil, err
		}
		return [][]byte{out[0], idx, out[1]}, nil
	})
}

func encodeAll(vs ...Value) ([][]byte, error) {
	out := make([][]byte, len(vs))
	for i, v := range vs {
		raw, err := Encode(v)
		if err != nil {
			return nil, err
		}
		out[i] = raw
	}
	return out, nil
}
