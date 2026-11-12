package records

import (
	"context"
	"errors"
	"slices"
	"strconv"

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

// Join adds an open approval to an open proposal, in one CompareAndSwapAll of both keys, and pins the proposal to
// headSHA and digest: the head that carries every approval of the proposal, and its policy digest. Pinning in the
// same swap means a merge of an older head can never consume the approval that just joined: its digest no longer
// matches (Consume refuses it). The approval records the proposal once: joining the same proposal again only pins it
// again, and any other proposal is ErrJoined.
func Join(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID, proposalID, headSHA, digest string) error {
	if approvalID == "" || proposalID == "" || digest == "" {
		return errors.New("records: join: an empty id or digest")
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
		again := a.Proposal == proposalID && slices.Contains(p.Approvals, approvalID)
		switch {
		case again && p.State != StateOpen:
			return nil, ErrMoved
		case again:
			p.HeadSHA, p.ExpectedDigest = headSHA, digest
			raw, err := Encode(&p)
			if err != nil {
				return nil, err
			}
			return [][]byte{cur[0], raw}, nil
		case a.Proposal != "":
			return nil, ErrJoined
		case a.State != StateOpen || p.State != StateOpen:
			return nil, ErrMoved
		case len(p.Approvals) >= MaxApprovals:
			return nil, ErrFull
		}
		a.Proposal = proposalID
		p.Approvals = append(p.Approvals, approvalID)
		p.HeadSHA, p.ExpectedDigest = headSHA, digest
		return encodeAll(&a, &p)
	})
}

// Consume is the one-use step of a proposal: its merge commit landed on the default branch with the expected
// digest. In one CompareAndSwapAll of five keys it moves the proposal from open to merged and records commit, removes
// the proposal from the proposal index, makes digest the repository's approved digest, removes the requests of its
// approvals from the request index (an answered request covers its key until its proposal is consumed), and records
// the push at PolicyPushKey. The approvals of the proposal count as consumed from then on (each records the proposal
// it joined, once). While the repository is outside the flow the approved digest stays as it is: the merged policy
// also carries the change no approval made.
//
// The same commit with the same digest again is a no-op success: it only checks the keys. A proposal that is void,
// merged by another commit, without a pull request, or expecting another digest (also after it was merged) is
// ErrMoved, and so is a push recorded with another judgement; a missing proposal is ErrNoRecord. The state is decided
// again after every lost swap, so of concurrent callers with different commits exactly one wins.
func Consume(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID, commit, digest string) error {
	if proposalID == "" || commit == "" || digest == "" {
		return errors.New("records: consume: an empty proposal id, commit or digest")
	}
	// The approvals cannot be in the swap (up to 9 keys more), so their requests are read first. An approval that
	// joined in between (it may keep the digest) is read on the next round.
	for range consumeReads {
		requests, err := approvalRequests(ctx, s, t, fullName, proposalID)
		if err != nil {
			return err
		}
		err = consume(ctx, s, t, fullName, proposalID, commit, digest, requests)
		if !errors.Is(err, errApprovalsMoved) {
			return err
		}
	}
	return store.ErrConflict
}

// consumeReads is how often Consume reads the approvals of a proposal that keeps gaining one.
const consumeReads = 3

// errApprovalsMoved is a proposal holding an approval Consume did not read.
var errApprovalsMoved = errors.New("records: the proposal's approvals moved")

// approvalRequests maps each approval of the proposal to its request.
func approvalRequests(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID string) (map[string]int, error) {
	var p Proposal
	if err := Load(ctx, s, t, ProposalKey(fullName, proposalID), &p); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(p.Approvals))
	for _, id := range p.Approvals {
		var a Approval
		if err := Load(ctx, s, t, ApprovalKey(fullName, id), &a); err != nil {
			return nil, err
		}
		out[id] = a.Request
	}
	return out, nil
}

func consume(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID, commit, digest string,
	requests map[string]int) error {
	keys := []string{ProposalKey(fullName, proposalID), ProposalIndexKey(fullName), RepoKey(fullName),
		RequestIndexKey(fullName), PolicyPushKey(fullName, commit)}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return nil, ErrNoRecord
		}
		var p Proposal
		if err := Decode(cur[0], &p); err != nil {
			return nil, err
		}
		switch {
		case p.State == StateMerged && p.Commit == commit && p.ExpectedDigest == digest:
			return cur, nil
		case p.State != StateOpen || p.PullRequest == 0 || p.ExpectedDigest != digest:
			return nil, ErrMoved
		}
		push := PolicyPush{Digest: digest, Proposal: proposalID}
		if cur[4] != nil {
			var was PolicyPush
			if err := Decode(cur[4], &was); err != nil {
				return nil, err
			}
			if was.Digest != digest || was.Proposal != proposalID {
				return nil, ErrMoved
			}
		}
		answered := map[string]bool{}
		for _, id := range p.Approvals {
			request, ok := requests[id]
			if !ok {
				return nil, errApprovalsMoved
			}
			answered[strconv.Itoa(request)] = true
		}
		p.State, p.Commit = StateMerged, commit

		proposals, err := readIndex(cur[1], SchemaProposalIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(proposals.Entries)
		if id, ok := proposals.Lookup(PullEntry(p.PullRequest)); ok && id == proposalID {
			proposals.Remove(PullEntry(p.PullRequest))
		}
		proposalIndex, err := writeIndex(cur[1], before, proposals)
		if err != nil {
			return nil, err
		}

		reqs, err := readIndex(cur[3], SchemaRequestIndex)
		if err != nil {
			return nil, err
		}
		before = slices.Clone(reqs.Entries)
		reqs.Entries = slices.DeleteFunc(reqs.Entries, func(e Entry) bool { return answered[e.ID] })
		requestIndex, err := writeIndex(cur[3], before, reqs)
		if err != nil {
			return nil, err
		}

		var r Repo
		if cur[2] != nil {
			if err := Decode(cur[2], &r); err != nil {
				return nil, err
			}
		}
		if r.OutsideFlow == nil {
			r.ApprovedDigest = digest
		}

		out, err := encodeAll(&p, &r, &push)
		if err != nil {
			return nil, err
		}
		return [][]byte{out[0], proposalIndex, out[1], requestIndex, out[2]}, nil
	})
}

// Outside records that the push of commit brought a policy of digest that no approval made: the repository is outside
// the flow (design 7.1) until an owner confirms or reverts it (M2 task 23). One swap sets Repo.OutsideFlow to digest,
// keeping an issue already opened for it, and records the push at PolicyPushKey. A push already recorded outside the
// flow changes nothing and marked is false; one recorded as a consume is ErrMoved. r is the repository afterwards.
func Outside(ctx context.Context, s store.Store, t store.Tenant, fullName, commit, digest string) (r Repo, marked bool, err error) {
	if commit == "" {
		return Repo{}, false, errors.New("records: outside: an empty commit")
	}
	keys := []string{RepoKey(fullName), PolicyPushKey(fullName, commit)}
	err = store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		r, marked = Repo{}, false
		if cur[0] != nil {
			if err := Decode(cur[0], &r); err != nil {
				return nil, err
			}
		}
		if cur[1] != nil {
			var was PolicyPush
			if err := Decode(cur[1], &was); err != nil {
				return nil, err
			}
			if was.Proposal != "" {
				return nil, ErrMoved
			}
			return cur, nil
		}
		if r.OutsideFlow == nil {
			r.OutsideFlow = &OutsideFlow{}
		}
		r.OutsideFlow.Digest, marked = digest, true
		return encodeAll(&r, &PolicyPush{Digest: digest})
	})
	return r, marked, err
}

// OutsideIssue records issue as the issue of the policy changed outside the flow, when none is recorded yet, and
// returns the issue recorded: another one when another delivery recorded its issue first. A repository no longer
// outside the flow is ErrMoved.
func OutsideIssue(ctx context.Context, s store.Store, t store.Tenant, fullName string, issue int) (int, error) {
	if issue <= 0 {
		return 0, errors.New("records: outside issue: no issue")
	}
	recorded := 0
	err := store.Update(ctx, s, t, []string{RepoKey(fullName)}, func(cur [][]byte) ([][]byte, error) {
		var r Repo
		if cur[0] == nil {
			return nil, ErrMoved
		}
		if err := Decode(cur[0], &r); err != nil {
			return nil, err
		}
		switch {
		case r.OutsideFlow == nil:
			return nil, ErrMoved
		case r.OutsideFlow.Issue != 0:
			recorded = r.OutsideFlow.Issue
			return cur, nil
		}
		r.OutsideFlow.Issue, recorded = issue, issue
		raw, err := Encode(&r)
		return [][]byte{raw}, err
	})
	return recorded, err
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
