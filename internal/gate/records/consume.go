package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Merge is a proposal's pull request landed on the default branch.
type Merge struct {
	Proposal string
	Commit   string // the pull request's merge commit
	Digest   string // the policy digest it landed
	Push     Push   // the push that landed it; its After is a later commit when a merge queue landed several
	// AtTip says the policy at the default branch's tip has Digest now, and Commit comes after Over. Only then does
	// Digest become the approved digest: a consume handled after a later policy push never puts an older digest back,
	// and never approves a tip that holds a change no approval made.
	AtTip bool
	// Recheck records a sweep recovery handoff in the same swap as the consume.
	Recheck bool
	// Over is the repository's ApprovedCommit the caller judged AtTip against. With AtTip, a repository that holds
	// another one now (a consume landed in between) is ErrApprovedMoved: read it again and judge again.
	Over string
}

// ErrApprovedMoved is returned by Consume when the repository's approved commit is not the Merge's Over any more.
var ErrApprovedMoved = errors.New("records: the approved commit moved")

// Consume is the one-use step of a proposal: its merge commit landed on the default branch with the expected
// digest. In one CompareAndSwapAll of five keys it moves the proposal from open to merged and records the merge
// commit, removes the proposal from the proposal index, makes the digest the repository's approved digest (see
// Merge.AtTip), removes the requests of its approvals from the request index (an answered request covers its key
// until its proposal is consumed), and records the push at PolicyPushKey. The approvals of the proposal count as
// consumed from then on (each records the proposal it joined, once). While the repository is outside the flow the
// approved digest stays as it is: the merged policy also carries the change no approval made. A proposal that holds a
// revert approval restored the approved policy (M2 task 23): with AtTip it ends the state and approves its digest.
//
// The same commit with the same digest again is a no-op success: it only checks the keys. A proposal that is void,
// merged by another commit, without a pull request, or expecting another digest (also after it was merged) is
// ErrMoved, and so is a push recorded with another judgement; a missing proposal is ErrNoRecord. The state is decided
// again after every lost swap, so of concurrent callers with different commits exactly one wins.
func Consume(ctx context.Context, s store.Store, t store.Tenant, fullName string, m Merge) error {
	if m.Proposal == "" || m.Commit == "" || m.Digest == "" || m.Push.After == "" {
		return errors.New("records: consume: an empty proposal id, commit, digest or push")
	}
	// The approvals cannot be in the swap (up to 9 keys more), so their requests are read first. An approval that
	// joined in between (it may keep the digest) is read on the next round.
	for range consumeReads {
		approvals, err := approvalsOf(ctx, s, t, fullName, m.Proposal)
		if err != nil {
			return err
		}
		err = consume(ctx, s, t, fullName, m, approvals)
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

// joined is what a consume needs of an approval of the proposal: its request (0 when none), and, for a revert, the
// revert.
type joined struct {
	request int
	revert  *Revert
}

// approvalsOf reads each approval of the proposal.
func approvalsOf(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID string) (map[string]joined, error) {
	var p Proposal
	if err := Load(ctx, s, t, ProposalKey(fullName, proposalID), &p); err != nil {
		return nil, err
	}
	out := make(map[string]joined, len(p.Approvals))
	for _, id := range p.Approvals {
		var a Approval
		if err := Load(ctx, s, t, ApprovalKey(fullName, id), &a); err != nil {
			return nil, err
		}
		j := joined{request: a.Request}
		if a.Kind == KindRevert {
			j.revert = &Revert{}
			if err := json.Unmarshal(a.Change, j.revert); err != nil {
				return nil, fmt.Errorf("records: approval %s holds an unreadable revert", id)
			}
		}
		out[id] = j
	}
	return out, nil
}

func consume(ctx context.Context, s store.Store, t store.Tenant, fullName string, m Merge, approvals map[string]joined) error {
	keys := []string{ProposalKey(fullName, m.Proposal), ProposalIndexKey(fullName), RepoKey(fullName),
		RequestIndexKey(fullName), PolicyPushKey(fullName, m.Push)}
	approvedMoved, stale := false, false
	err := store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		approvedMoved, stale = false, false
		if cur[0] == nil {
			return nil, ErrNoRecord
		}
		var p Proposal
		if err := Decode(cur[0], &p); err != nil {
			return nil, err
		}
		switch {
		case p.State == StateMerged && p.Commit == m.Commit && p.ExpectedDigest == m.Digest:
			return cur, nil
		case p.State != StateOpen || p.PullRequest == 0 || p.ExpectedDigest != m.Digest:
			return nil, ErrMoved
		}
		push := PolicyPush{Digest: m.Digest, Proposal: m.Proposal}
		if cur[4] != nil {
			var was PolicyPush
			if err := Decode(cur[4], &was); err != nil {
				return nil, err
			}
			if was != push {
				return nil, ErrMoved
			}
		}
		answered := map[string]bool{}
		var revs []Revert
		for _, id := range p.Approvals {
			a, ok := approvals[id]
			if !ok {
				return nil, errApprovalsMoved
			}
			answered[strconv.Itoa(a.request)] = true
			if a.revert != nil {
				revs = append(revs, *a.revert)
			}
		}
		reverts := len(revs) > 0
		p.State, p.Commit = StateMerged, m.Commit

		proposals, err := readIndex(cur[1], SchemaProposalIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(proposals.Entries)
		if id, ok := proposals.Lookup(PullEntry(p.PullRequest)); ok && id == m.Proposal {
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
		if !applies(p, r, revs) {
			// Judged on the proposal and the repository together, so confirmed by a swap of the unchanged bytes, as
			// below.
			stale = true
			return cur, nil
		}
		// Only a first approval starts change control (design 9.2; M2 review H1): an ordinary proposal merged into a
		// repository with no approved policy uses its approvals and approves nothing.
		if m.AtTip && (r.OutsideFlow == nil || reverts) && (r.ApprovedDigest != "" || p.Kind == ProposalFirst) {
			if r.ApprovedCommit != m.Over {
				// This judges the proposal and the repository together, and Update reads them one by one: a consume
				// that landed between the two reads shows the proposal open and the repository already moved. Only a
				// swap of the unchanged bytes proves both were current together; a lost one reads again.
				approvedMoved = true
				return cur, nil
			}
			if r.ApprovedDigest == "" {
				r.GuardedSince = m.Commit // the first approval: change control starts here
			}
			r.ApprovedDigest, r.ApprovedCommit, r.OutsideFlow = m.Digest, m.Commit, nil
			if m.Recheck {
				r.SweepRecheck = m.Proposal
			}
		}

		out, err := encodeAll(&p, &r, &push)
		if err != nil {
			return nil, err
		}
		return [][]byte{out[0], proposalIndex, out[1], requestIndex, out[2]}, nil
	})
	switch {
	case err == nil && stale:
		return ErrMoved
	case err == nil && approvedMoved:
		return ErrApprovedMoved
	}
	return err
}

// applies reports whether proposal p still applies to the repository r: a first approval only while r has no approved
// policy and is not outside the flow, and each of its reverts only while it holds (Revert.Holds). A proposal approved
// in a state that ended admits nothing (M2 task 23).
func applies(p Proposal, r Repo, revs []Revert) bool {
	if p.Kind == ProposalFirst && (r.ApprovedDigest != "" || r.OutsideFlow != nil) {
		return false
	}
	for _, rev := range revs {
		if !rev.Holds(r) {
			return false
		}
	}
	return true
}

// Outside records that push brought a policy of digest that no approval made: the repository is outside the flow
// (design 7.1) until an owner confirms or reverts it (M2 task 23). One swap sets Repo.OutsideFlow to digest, keeping
// an issue already opened for it, and records the push at PolicyPushKey. A push already recorded outside the flow
// changes nothing and marked is false; one recorded with another judgement is ErrMoved. r is the repository afterwards.
func Outside(ctx context.Context, s store.Store, t store.Tenant, fullName string, push Push, digest string) (r Repo, marked bool, err error) {
	if push.After == "" {
		return Repo{}, false, errors.New("records: outside: an empty commit")
	}
	keys := []string{RepoKey(fullName), PolicyPushKey(fullName, push)}
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
			if was.Proposal != "" || was.Unguarded {
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

// Unguarded records that push brought digest to a repository with no approved policy yet: change control had not
// started (M2 task 23 starts it), so the push is not outside the flow, and a delivery of it again after the first
// consume is not either. A push already recorded changes nothing.
func Unguarded(ctx context.Context, s store.Store, t store.Tenant, fullName string, push Push, digest string) error {
	if push.After == "" {
		return errors.New("records: unguarded: an empty commit")
	}
	return store.Update(ctx, s, t, []string{PolicyPushKey(fullName, push)}, func(cur [][]byte) ([][]byte, error) {
		if cur[0] != nil {
			return cur, nil
		}
		raw, err := Encode(&PolicyPush{Digest: digest, Unguarded: true})
		return [][]byte{raw}, err
	})
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
