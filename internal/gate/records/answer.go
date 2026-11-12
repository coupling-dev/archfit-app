package records

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/store"
)

// ErrAnswered is returned when the request of an answer is no longer open: another owner answered it, or it closed.
var ErrAnswered = errors.New("records: the request is not open")

// Pin is the proposal an answer joins and the head that carries it (M2 task 19). PullRequest is set when the proposal
// is new: Answer creates it with its proposal index entry.
type Pin struct {
	Proposal    string
	PullRequest int
	HeadSHA     string
	Digest      string // the expected policy digest of HeadSHA
}

// Answer records an owner's answer in one CompareAndSwapAll, so nothing of it can be written without the rest: the new
// approval a at approvalID, its request moved from open to answered and out of the request index, and, when pin is set,
// the approval joining that proposal pinned to its head and digest, as Join does. A proposal that does not exist yet is
// created open on pin.PullRequest with its index entry. A request that is not open is ErrAnswered; an approval id in
// use is ErrExists; a proposal that is not open is ErrMoved, and a full one ErrFull.
func Answer(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, pin *Pin) error {
	if approvalID == "" || a.Request <= 0 || pin != nil && (pin.Proposal == "" || pin.HeadSHA == "" || pin.Digest == "") {
		return errors.New("records: answer: an empty id, request, head or digest")
	}
	keys := []string{ApprovalKey(fullName, approvalID), RequestKey(fullName, a.Request), RequestIndexKey(fullName)}
	if pin != nil {
		keys = append(keys, ProposalKey(fullName, pin.Proposal), ProposalIndexKey(fullName))
	}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] != nil {
			return nil, ErrExists
		}
		var req Request
		if cur[1] == nil {
			return nil, ErrNoRecord
		}
		if err := Decode(cur[1], &req); err != nil {
			return nil, err
		}
		if req.State != StateOpen {
			return nil, ErrAnswered
		}
		req.State = StateAnswered
		requests, err := readIndex(cur[2], SchemaRequestIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(requests.Entries)
		if id, ok := requests.Lookup(req.Key); ok && id == strconv.Itoa(a.Request) {
			requests.Remove(req.Key)
		}
		requestIndex, err := writeIndex(cur[2], before, requests)
		if err != nil {
			return nil, err
		}

		a.State, a.Proposal = StateOpen, ""
		if pin == nil {
			out, err := encodeAll(&a, &req)
			if err != nil {
				return nil, err
			}
			return [][]byte{out[0], out[1], requestIndex}, nil
		}

		a.Proposal = pin.Proposal
		var p Proposal
		proposals, err := readIndex(cur[4], SchemaProposalIndex)
		if err != nil {
			return nil, err
		}
		before = slices.Clone(proposals.Entries)
		switch {
		case cur[3] == nil && pin.PullRequest <= 0:
			return nil, ErrNoRecord
		case cur[3] == nil:
			p = Proposal{PullRequest: pin.PullRequest, State: StateOpen}
			if err := proposals.Add(PullEntry(pin.PullRequest), pin.Proposal); err != nil {
				return nil, err
			}
		default:
			if err := Decode(cur[3], &p); err != nil {
				return nil, err
			}
		}
		switch {
		case p.State != StateOpen:
			return nil, ErrMoved
		case len(p.Approvals) >= MaxApprovals:
			return nil, ErrFull
		}
		p.Approvals = append(p.Approvals, approvalID)
		p.HeadSHA, p.ExpectedDigest = pin.HeadSHA, pin.Digest
		proposalIndex, err := writeIndex(cur[4], before, proposals)
		if err != nil {
			return nil, err
		}
		out, err := encodeAll(&a, &req, &p)
		if err != nil {
			return nil, err
		}
		return [][]byte{out[0], out[1], requestIndex, out[2], proposalIndex}, nil
	})
}

// Void marks an open approval void because its change no longer applies to the default branch, and opens its request
// again with its request index entry, in one swap. When another open request now holds the key, the request stays
// answered and reopened is false. A void approval again is no change.
func Void(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string) (request int, reopened bool, err error) {
	var a Approval
	if err := Load(ctx, s, t, ApprovalKey(fullName, approvalID), &a); err != nil {
		return 0, false, err
	}
	keys := []string{ApprovalKey(fullName, approvalID), RequestKey(fullName, a.Request), RequestIndexKey(fullName)}
	err = store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		reopened = false
		return voidApproval(cur, a.Request, &reopened)
	})
	return a.Request, reopened, err
}

// voidApproval is the change of Void: the approval, its request and the request index, in that order.
func voidApproval(cur [][]byte, request int, reopened *bool) ([][]byte, error) {
	var a Approval
	if cur[0] == nil {
		return nil, ErrNoRecord
	}
	if err := Decode(cur[0], &a); err != nil {
		return nil, err
	}
	if a.State == StateVoid {
		return cur, nil
	}
	a.State = StateVoid
	approval, err := Encode(&a)
	if err != nil {
		return nil, err
	}
	next := [][]byte{approval, cur[1], cur[2]}
	var req Request
	if cur[1] == nil {
		return next, nil
	}
	if err := Decode(cur[1], &req); err != nil {
		return nil, err
	}
	x, err := readIndex(cur[2], SchemaRequestIndex)
	if err != nil {
		return nil, err
	}
	before := slices.Clone(x.Entries)
	if req.State != StateAnswered || x.Add(req.Key, strconv.Itoa(request)) != nil {
		return next, nil
	}
	req.State = StateOpen
	if next[1], err = Encode(&req); err != nil {
		return nil, err
	}
	if next[2], err = writeIndex(cur[2], before, x); err != nil {
		return nil, err
	}
	*reopened = true
	return next, nil
}

// VoidProposal marks an open proposal void and removes it from the proposal index, in one swap: its pull request was
// closed without a merge, or no approval in it applies any more. Its approvals are voided one by one (Void). A void
// proposal again is no change; a merged one is ErrMoved.
func VoidProposal(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID string) error {
	keys := []string{ProposalKey(fullName, proposalID), ProposalIndexKey(fullName)}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return nil, ErrNoRecord
		}
		var p Proposal
		if err := Decode(cur[0], &p); err != nil {
			return nil, err
		}
		switch p.State {
		case StateVoid:
			return cur, nil
		case StateMerged:
			return nil, ErrMoved
		}
		p.State = StateVoid
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
		raw, err := Encode(&p)
		if err != nil {
			return nil, err
		}
		return [][]byte{raw, idx}, nil
	})
}
