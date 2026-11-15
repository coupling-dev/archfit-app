package records

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/alexei-led/archfit-app/internal/store"
)

// Proposal kinds of a pull request someone else wrote, which an owner approved as it is (M2 task 23). The App's
// policy pull request on archfit/policy has no kind.
const (
	ProposalFirst    = "first"    // an onboarding pull request: the first policy, and the workflow it brings
	ProposalWorkflow = "workflow" // a pull request that changes .github/workflows/archfit.yaml
)

// FileDigest is "sha256:" and the lowercase hex SHA-256 of the exact bytes of a file: the digest a workflow approval
// is bound to, in the form of the policy digest.
func FileDigest(file []byte) string {
	sum := sha256.Sum256(file)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Revert is the change of a revert approval: the policy at Commit, of digest Digest, the last one the owners
// approved, replaces the policy of digest Outside that changed outside the flow; Issue is the issue of that state.
type Revert struct {
	Commit  string `json:"commit"`
	Digest  string `json:"digest"`
	Outside string `json:"outside"`
	Issue   int    `json:"issue,omitempty"`
}

// Holds reports whether the state the revert was made in still holds in r: the policy changed outside the flow is still
// the one it replaces, and the approved policy still the one it restores. A confirm, or a later change, ends it; a
// revert of a state that ended admits nothing, even when the same change is pushed again.
func (rev Revert) Holds(r Repo) bool {
	return r.OutsideFlow != nil && r.OutsideFlow.Digest == rev.Outside && r.ApprovedDigest == rev.Digest &&
		r.ApprovedCommit == rev.Commit
}

// Bound is the pull request an owner approves as it is: its head and the digests there.
type Bound struct {
	Proposal       string // the open proposal of the pull request, or a new id
	PullRequest    int
	HeadSHA        string
	Digest         string // the policy digest at HeadSHA
	WorkflowDigest string // the workflow digest at HeadSHA; "" when the approval admits no workflow
}

// First records a first approval (design 9.2) of an onboarding pull request: the approval, joined to the
// ProposalFirst proposal of the pull request, which is created with its proposal index entry or pinned again to the
// new head, and the repository in ReposKey, in one swap. A repository that has an approved policy is ErrMoved.
func First(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, b Bound) error {
	return approveAsIs(ctx, s, t, fullName, approvalID, a, ProposalFirst, b, func(r Repo) bool {
		return r.ApprovedDigest == "" && r.OutsideFlow == nil
	})
}

// Workflow records a workflow approval of a pull request that changes .github/workflows/archfit.yaml, as First does
// but with a ProposalWorkflow proposal and no ReposKey entry. A repository with no approved policy, or one outside
// the flow, is ErrMoved.
func Workflow(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, b Bound) error {
	return approveAsIs(ctx, s, t, fullName, approvalID, a, ProposalWorkflow, b, func(r Repo) bool {
		return r.ApprovedDigest != "" && r.OutsideFlow == nil
	})
}

// approveAsIs is First and Workflow. The repository record is in the swap unchanged, so the state it was judged in
// is the state at the swap. An approval id in use is ErrExists, a pull request held by a proposal of another kind or
// one that is not open ErrMoved, a full proposal ErrFull.
func approveAsIs(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, kind string,
	b Bound, due func(Repo) bool,
) error {
	listRepo := kind == ProposalFirst // a first approval lists the repository
	if approvalID == "" || b.Proposal == "" || b.PullRequest <= 0 || b.HeadSHA == "" || b.Digest == "" {
		return errors.New("records: approve: an empty id, pull request, head or digest")
	}
	keys := []string{ApprovalKey(fullName, approvalID), ProposalKey(fullName, b.Proposal), ProposalIndexKey(fullName),
		RepoKey(fullName)}
	if listRepo {
		keys = append(keys, ReposKey)
	}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] != nil {
			return nil, ErrExists
		}
		var r Repo
		if cur[3] != nil {
			if err := Decode(cur[3], &r); err != nil {
				return nil, err
			}
		}
		if !due(r) {
			return nil, ErrMoved
		}
		x, err := readIndex(cur[2], SchemaProposalIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(x.Entries)
		var p Proposal
		if cur[1] == nil {
			p = Proposal{Kind: kind, PullRequest: b.PullRequest, State: StateOpen}
			if err := x.Add(PullEntry(b.PullRequest), b.Proposal); err != nil {
				return nil, err
			}
		} else if err := Decode(cur[1], &p); err != nil {
			return nil, err
		}
		switch {
		case p.State != StateOpen || p.Kind != kind || p.PullRequest != b.PullRequest:
			return nil, ErrMoved
		case len(p.Approvals) >= MaxApprovals:
			return nil, ErrFull
		}
		p.Approvals = append(p.Approvals, approvalID)
		p.HeadSHA, p.ExpectedDigest, p.WorkflowDigest = b.HeadSHA, b.Digest, b.WorkflowDigest
		a.State, a.Proposal = StateOpen, b.Proposal
		index, err := writeIndex(cur[2], before, x)
		if err != nil {
			return nil, err
		}
		out, err := encodeAll(&a, &p)
		if err != nil {
			return nil, err
		}
		next := [][]byte{out[0], out[1], index, cur[3]}
		if listRepo {
			repos, err := listed(cur[4], fullName)
			if err != nil {
				return nil, err
			}
			next = append(next, repos)
		}
		return next, nil
	})
}

// listed is the ReposKey bytes with fullName in them.
func listed(cur []byte, fullName string) ([]byte, error) {
	x, err := readIndex(cur, SchemaRepos)
	if err != nil {
		return nil, err
	}
	before := slices.Clone(x.Entries)
	if err := x.Add(repo(fullName), ""); err != nil {
		return nil, err
	}
	return writeIndex(cur, before, x)
}

// ApproveCurrent records the first approval of a repository whose default branch already has its policy and no
// record (an already-converted repository, M2 task 23): the approval, recorded merged at once, and the repository's
// approved digest digest at commit, where change control starts (GuardedSince), as a first consume sets them, with
// the repository in ReposKey, in one swap. A repository with an approved policy or outside the flow is ErrMoved; an
// approval id in use ErrExists.
func ApproveCurrent(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, commit, digest string) error {
	if approvalID == "" || commit == "" || digest == "" {
		return errors.New("records: approve current: an empty id, commit or digest")
	}
	keys := []string{ApprovalKey(fullName, approvalID), RepoKey(fullName), ReposKey}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] != nil {
			return nil, ErrExists
		}
		var r Repo
		if cur[1] != nil {
			if err := Decode(cur[1], &r); err != nil {
				return nil, err
			}
		}
		if r.ApprovedDigest != "" || r.OutsideFlow != nil {
			return nil, ErrMoved
		}
		r.ApprovedDigest, r.ApprovedCommit, r.GuardedSince = digest, commit, commit
		a.State, a.Proposal = StateMerged, ""
		out, err := encodeAll(&a, &r)
		if err != nil {
			return nil, err
		}
		repos, err := listed(cur[2], fullName)
		if err != nil {
			return nil, err
		}
		return [][]byte{out[0], out[1], repos}, nil
	})
}

// Confirm records an owner's confirm of the policy changed outside the flow (design 7.1, 9.2): the approval, recorded
// merged at once, and digest at commit as the repository's approved policy, which ends the state, in one swap. It
// returns the issue of the state that ended. A repository not outside the flow, or outside it with another digest
// (the policy changed again since the owner looked), is ErrMoved; an approval id in use ErrExists.
func Confirm(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, commit, digest string) (issue int, err error) {
	if approvalID == "" || commit == "" || digest == "" {
		return 0, errors.New("records: confirm: an empty id, commit or digest")
	}
	keys := []string{ApprovalKey(fullName, approvalID), RepoKey(fullName)}
	err = store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		issue = 0
		if cur[0] != nil {
			return nil, ErrExists
		}
		var r Repo
		if cur[1] != nil {
			if err := Decode(cur[1], &r); err != nil {
				return nil, err
			}
		}
		if r.OutsideFlow == nil || r.OutsideFlow.Digest != digest {
			return nil, ErrMoved
		}
		issue = r.OutsideFlow.Issue
		r.ApprovedDigest, r.ApprovedCommit, r.OutsideFlow = digest, commit, nil
		a.State, a.Proposal = StateMerged, ""
		return encodeAll(&a, &r)
	})
	return issue, err
}

// RevertTo records an owner's revert of the policy changed outside the flow: the approval, whose change is revert,
// joins the App's policy proposal as Answer's does with pin, which the caller wrote with the approved policy restored.
// The repository record is in the swap unchanged: one where the revert no longer holds (Revert.Holds; revert.Outside
// is "" for a deleted policy) is ErrMoved. An approval id in use is ErrExists, a proposal that is
// not open or has a kind ErrMoved, a full one ErrFull.
func RevertTo(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, revert Revert, pin Pin) error {
	if approvalID == "" || pin.Proposal == "" || pin.HeadSHA == "" || pin.Digest == "" || revert.Commit == "" || revert.Digest == "" {
		return errors.New("records: revert: an empty id, head, commit or digest")
	}
	change, err := json.Marshal(revert)
	if err != nil {
		return err
	}
	a.Kind, a.Change = KindRevert, change
	return joinPolicy(ctx, s, t, fullName, approvalID, a, pin, revert.Holds)
}

// joinPolicy records a requestless policy approval and its proposal pin together.
func joinPolicy(ctx context.Context, s store.Store, t store.Tenant, fullName, approvalID string, a Approval, pin Pin, holds func(Repo) bool) error {
	keys := []string{ApprovalKey(fullName, approvalID), ProposalKey(fullName, pin.Proposal), ProposalIndexKey(fullName),
		RepoKey(fullName)}
	return store.Update(ctx, s, t, keys, func(cur [][]byte) ([][]byte, error) {
		if cur[0] != nil {
			return nil, ErrExists
		}
		var r Repo
		if cur[3] != nil {
			if err := Decode(cur[3], &r); err != nil {
				return nil, err
			}
		}
		if !holds(r) {
			return nil, ErrMoved
		}
		x, err := readIndex(cur[2], SchemaProposalIndex)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(x.Entries)
		var p Proposal
		switch {
		case cur[1] == nil && pin.PullRequest <= 0:
			return nil, ErrNoRecord
		case cur[1] == nil:
			p = Proposal{PullRequest: pin.PullRequest, State: StateOpen}
			if err := x.Add(PullEntry(pin.PullRequest), pin.Proposal); err != nil {
				return nil, err
			}
		default:
			if err := Decode(cur[1], &p); err != nil {
				return nil, err
			}
		}
		switch {
		case p.State != StateOpen || p.Kind != "" || p.EditEpoch != pin.EditEpoch:
			return nil, ErrMoved
		case len(p.Approvals) >= MaxApprovals:
			return nil, ErrFull
		}
		p.Approvals = append(p.Approvals, approvalID)
		p.HeadSHA, p.ExpectedDigest, p.EditVoided = pin.HeadSHA, pin.Digest, false
		a.State, a.Proposal = StateOpen, pin.Proposal
		index, err := writeIndex(cur[2], before, x)
		if err != nil {
			return nil, err
		}
		out, err := encodeAll(&a, &p)
		if err != nil {
			return nil, err
		}
		return [][]byte{out[0], out[1], index, cur[3]}, nil
	})
}

// ConsumeWorkflow is the one-use step of a workflow proposal: its pull request merged in commit with the approved
// workflow. In one swap the proposal moves from open to merged with commit and leaves the proposal index. The same
// commit again is a no-op; any other state is ErrMoved, a missing proposal ErrNoRecord.
func ConsumeWorkflow(ctx context.Context, s store.Store, t store.Tenant, fullName, proposalID, commit string) error {
	if proposalID == "" || commit == "" {
		return errors.New("records: consume workflow: an empty id or commit")
	}
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
		case p.Kind != ProposalWorkflow:
			return nil, ErrMoved
		case p.State == StateMerged && p.Commit == commit:
			return cur, nil
		case p.State != StateOpen:
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
		index, err := writeIndex(cur[1], before, x)
		if err != nil {
			return nil, err
		}
		raw, err := Encode(&p)
		if err != nil {
			return nil, err
		}
		return [][]byte{raw, index}, nil
	})
}
