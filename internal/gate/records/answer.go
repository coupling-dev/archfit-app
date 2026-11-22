package records

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/coupling-dev/archfit-app/internal/store"
)

// ErrAnswered is returned when the request of an answer is no longer open: another owner answered it, or it closed.
var ErrAnswered = errors.New("records: the request is not open")

// ErrOutsideFlow is returned when an answer would be recorded while the repository's policy changed outside the flow:
// its change would be written on a policy no owner approved (M2 task 23).
var ErrOutsideFlow = errors.New("records: the policy changed outside the flow")

// Pin is the proposal an answer joins and the head that carries it (M2 task 19). PullRequest is set when the proposal
// is new: Answer creates it with its proposal index entry.
type Pin struct {
	Proposal    string
	PullRequest int
	HeadSHA     string
	EditEpoch   uint64 // proposal edit generation the writer read, after its own voids
	Digest      string // the expected policy digest of HeadSHA
	// MergeNow says the approval that joins asked to merge now; it sets Proposal.MergeNow.
	MergeNow bool
}

// BatchAnswer describes one approval and whether it joins the shared proposal.
type BatchAnswer struct {
	ApprovalID string
	Approval   Approval
	Join       bool
}

// Answer records an owner's answer in one CompareAndSwapAll, so nothing of it can be written without the rest: the new
// approval a at approvalID, its request moved from open to answered, and, when pin is set, the approval joining that
// proposal pinned to its head and digest, as Join does. Without pin the request leaves the request index at once; with
// pin it stays there until the proposal is consumed (Consume) or the approval is voided (Void). A proposal that does not exist yet is
// created open on pin.PullRequest with its index entry. A request that is not open is ErrAnswered; an approval id in
// use is ErrExists; a proposal that is not open is ErrMoved, and a full one ErrFull. The repository record is in the
// swap unchanged: one outside the flow is ErrOutsideFlow.
func Answer(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, pin *Pin) error {
	return AnswerBatch(ctx, s, t, fullName, []BatchAnswer{{ApprovalID: approvalID, Approval: a, Join: pin != nil}}, pin)
}

// AnswerBatch records all answers in one atomic swap.
func AnswerBatch(ctx context.Context, s store.Store, t store.Tenant, fullName string, items []BatchAnswer, pin *Pin) error {
	if len(items) == 0 || len(items) > 20 {
		return errors.New("records: answer: invalid batch size")
	}
	ids := map[string]bool{}
	requestsSeen := map[int]bool{}
	joined := 0
	keys := make([]string, 0, len(items)*2+4)
	for _, item := range items {
		if item.ApprovalID == "" || item.Approval.Request <= 0 || item.Join && pin == nil {
			return errors.New("records: answer: an empty id, request, head or digest")
		}
		if ids[item.ApprovalID] || requestsSeen[item.Approval.Request] {
			return errors.New("records: answer: duplicate id or request")
		}
		ids[item.ApprovalID], requestsSeen[item.Approval.Request] = true, true
		if item.Join {
			joined++
		}
		keys = append(keys, ApprovalKey(fullName, item.ApprovalID), RequestKey(fullName, item.Approval.Request))
	}
	if pin != nil && (pin.Proposal == "" || pin.HeadSHA == "" || pin.Digest == "") {
		return errors.New("records: answer: an empty id, request, head or digest")
	}
	idx := len(keys)
	keys = append(keys, RequestIndexKey(fullName))
	if joined > 0 {
		keys = append(keys, ProposalKey(fullName, pin.Proposal), ProposalIndexKey(fullName))
	}
	keys = append(keys, RepoKey(fullName))
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		next := slices.Clone(cur)
		// Preserve the single-answer validation order before checking the repository.
		reqs := make([]Request, len(items))
		for i := range items {
			if cur[2*i] != nil {
				return nil, ErrExists
			}
			if cur[2*i+1] == nil {
				return nil, ErrNoRecord
			}
			if err := Decode(cur[2*i+1], &reqs[i]); err != nil {
				return nil, err
			}
			if reqs[i].State != StateOpen {
				return nil, ErrAnswered
			}
		}
		if raw := cur[len(cur)-1]; raw != nil {
			var r Repo
			if err := Decode(raw, &r); err != nil {
				return nil, err
			}
			if r.OutsideFlow != nil {
				return nil, ErrOutsideFlow
			}
		}
		x, err := readIndex(cur[idx], SchemaRequestIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(x.Entries)
		for i, item := range items {
			req := &reqs[i]
			req.State, req.Approval = StateAnswered, item.ApprovalID
			if id, ok := x.Lookup(req.Key); ok && id == strconv.Itoa(item.Approval.Request) && !item.Join {
				x.Remove(req.Key)
			}
			a := item.Approval
			a.State, a.Proposal = StateOpen, ""
			if item.Join {
				a.Proposal = pin.Proposal
			}
			out, err := encodeAll(&a, req)
			if err != nil {
				return nil, err
			}
			next[2*i], next[2*i+1] = out[0], out[1]
		}
		next[idx], err = writeIndex(cur[idx], before, x)
		if err != nil {
			return nil, err
		}
		if joined > 0 {
			pi := idx + 1
			proposals, err := readIndex(cur[pi+1], SchemaProposalIndex)
			if err != nil {
				return nil, err
			}
			before := slices.Clone(proposals.Entries)
			var p Proposal
			if cur[pi] == nil {
				if pin.PullRequest <= 0 {
					return nil, ErrNoRecord
				}
				p = Proposal{PullRequest: pin.PullRequest, State: StateOpen}
				if err := proposals.Add(PullEntry(pin.PullRequest), pin.Proposal); err != nil {
					return nil, err
				}
			} else if err := Decode(cur[pi], &p); err != nil {
				return nil, err
			}
			if p.State != StateOpen || p.EditEpoch != pin.EditEpoch {
				return nil, ErrMoved
			}
			if len(p.Approvals)+joined > MaxApprovals {
				return nil, ErrFull
			}
			for _, item := range items {
				if item.Join {
					p.Approvals = append(p.Approvals, item.ApprovalID)
				}
			}
			p.HeadSHA, p.ExpectedDigest, p.EditVoided, p.MergeNow = pin.HeadSHA, pin.Digest, false, pin.MergeNow
			next[pi], err = Encode(&p)
			if err != nil {
				return nil, err
			}
			next[pi+1], err = writeIndex(cur[pi+1], before, proposals)
			if err != nil {
				return nil, err
			}
		}
		return next, nil
	})
}

// Void marks an open approval void because its change no longer applies to the default branch, takes it out of its
// proposal's list (so the proposal's capacity counts only approvals that still apply), and opens its request again
// with its request index entry, all in one swap. When another open request now holds the key, the request stays
// answered and reopened is false. A void approval again is no change.
func Void(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string) (request int, reopened bool, err error) {
	var a Approval
	if err := Load(ctx, s, t, ApprovalKey(fullName, approvalID), &a); err != nil {
		return 0, false, err
	}
	keys := []string{ApprovalKey(fullName, approvalID), RequestKey(fullName, a.Request), RequestIndexKey(fullName)}
	if a.Proposal != "" {
		keys = append(keys, ProposalKey(fullName, a.Proposal))
	}
	err = store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		reopened = false
		return voidApproval(cur, approvalID, a.Request, &reopened)
	})
	return a.Request, reopened, err
}

// voidApproval is the change of Void: the approval, its request, the request index and, when it joined one, its
// proposal, in that order.
func voidApproval(cur [][]byte, approvalID string, request int, reopened *bool) ([][]byte, error) {
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
	next := slices.Clone(cur)
	var err error
	if next[0], err = Encode(&a); err != nil {
		return nil, err
	}
	if len(cur) > 3 && cur[3] != nil {
		var p Proposal
		if err := Decode(cur[3], &p); err != nil {
			return nil, err
		}
		if i := slices.Index(p.Approvals, approvalID); i >= 0 {
			if a.Kind == KindEdit {
				p.EditVoided = true
				p.EditEpoch++
			}
			p.Approvals = slices.Delete(p.Approvals, i, i+1)
			// The answer that asked to merge now may be the one leaving: the rest waits for a new answer (fail safe).
			p.MergeNow = false
			if next[3], err = Encode(&p); err != nil {
				return nil, err
			}
		}
	}
	var req Request
	if request <= 0 || cur[1] == nil {
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
	req.State, req.Approval = StateOpen, ""
	if next[1], err = Encode(&req); err != nil {
		return nil, err
	}
	if next[2], err = writeIndex(cur[2], before, x); err != nil {
		return nil, err
	}
	*reopened = true
	return next, nil
}

// ErrApprovalsLeft is returned by VoidProposal while the proposal still lists an approval: one joined after the caller
// voided the ones it read. Void that one too, then try again.
var ErrApprovalsLeft = errors.New("records: the proposal still holds an approval")

// VoidProposal marks an open proposal void and removes it from the proposal index, in one swap: its pull request was
// closed without a merge, merged another policy, or no approval in it applies any more. Void its approvals first, one
// by one (Void): while the proposal is in the index, a later answer finds it and finishes a cleanup that stopped half
// way. A proposal that still lists an approval is ErrApprovalsLeft, so no approval stays joined to a void proposal with
// its request covering its key. A void proposal again is no change; a merged one is ErrMoved.
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
		switch {
		case p.State == StateVoid:
			return cur, nil
		case p.State == StateMerged:
			return nil, ErrMoved
		case len(p.Approvals) > 0:
			return nil, ErrApprovalsLeft
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
