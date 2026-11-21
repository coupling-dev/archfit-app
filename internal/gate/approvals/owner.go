package approvals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/coupling-dev/archfit-app/internal/gate/inputs"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// Errors of an owner action outside requests (M2 task 23). None of them writes anything.
var (
	// ErrNotAdmin refuses a first approval by a user who is not both a repository administrator and an owner named in
	// the policy being approved (design 9.2).
	ErrNotAdmin = errors.New("approvals: the first policy is approved by a repository administrator")
	// ErrNotOwner refuses a workflow approval or a confirm by a user the approved policy does not name in owners. The
	// approved policy, not the default branch's: a policy changed outside the flow could name anyone. A revert's is
	// gate/proposals' ErrNotOwner.
	ErrNotOwner = errors.New("approvals: not an owner named in the approved policy")
	// ErrNotDue refuses an action the repository's state does not call for: a first approval of an approved repository,
	// a workflow approval before the first approval or outside the flow, a confirm inside the flow.
	ErrNotDue = errors.New("approvals: the repository's state does not call for this action")
	// ErrNotApprovable refuses a pull request the action cannot approve: not open, from a fork, into another branch, a
	// head without a valid policy or without the workflow, a workflow approval of a head that does not change it.
	ErrNotApprovable = errors.New("approvals: the pull request cannot be approved this way")
	// ErrMoved refuses an action whose change is not the one the owner's step-up was bound to: the pull request, the
	// default branch or the state moved since the owner looked.
	ErrMoved = errors.New("approvals: the change moved since the owner looked")
	// ErrNeedsPasskey refuses an action without a passkey step-up.
	ErrNeedsPasskey = errors.New("approvals: the action needs a passkey")
)

// Owner is the signed-in user who acts, as control authorized them for the repository.
type Owner struct {
	ID    int64
	Login string
	Admin bool // a repository administrator
}

// Action is an owner action outside requests: Kind is records.KindFirst, KindWorkflow or KindConfirm. Pull names the
// pull request of a first approval of an onboarding pull request and of a workflow approval; 0 for a first approval
// of the policy already on the default branch and for a confirm.
type Action struct {
	Kind string
	Pull int
}

// Change is what an action binds to, read from GitHub now. The owner's passkey step-up is bound to its Digest, and the
// approval records it.
type Change struct {
	Kind     string `json:"kind"`
	Pull     int    `json:"pull,omitempty"`
	HeadSHA  string `json:"head_sha,omitempty"` // a pull request's head
	Commit   string `json:"commit,omitempty"`   // the default branch's tip, when there is no pull request
	Policy   string `json:"policy"`             // the policy digest at HeadSHA or Commit
	Workflow string `json:"workflow,omitempty"` // the workflow digest at HeadSHA
	// Base is the policy digest the pull request changes the policy from, for a workflow approval: the policy at its
	// merge base, "" when that has none. A head whose policy is the base branch's (at Pull.BaseSHA) leaves the policy as
	// it is on merge, so Base is that digest and the merge base is not read.
	Base string `json:"base,omitempty"`
}

// OwnerVerdict reports whether the approval of c is the archfit/gate verdict of its head: a first approval, or a
// workflow approval of a pull request that also changes the policy, such as the move to engine 0.9.0 and policy
// version 1 (beta plan task 5.3). Both change what the engine judges with. Any other workflow approval leaves the
// verdict to the engine: a head that merely sits on an older policy, or one whose approved policy moved since, is not a
// policy change.
func (c Change) OwnerVerdict() bool {
	return c.Pull > 0 && (c.Kind == records.KindFirst || c.Kind == records.KindWorkflow && c.Policy != c.Base)
}

// Digest is "sha256:" and the hex SHA-256 of the change's JSON.
func (c Change) Digest() string {
	raw, _ := json.Marshal(c) // strings and ints always encode
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Input is one owner action as the owner made it.
type Input struct {
	Action Action
	Owner  Owner
	// Digest is the change digest the owner's step-up is bound to: the change the owner saw.
	Digest string
	// Redeem consumes the step-up token for the digest and returns the passkey id. It is called after every other
	// check passed and before anything is written.
	Redeem func(ctx context.Context, digest string) (passkeyID string, err error)
}

// Done is what an owner action did.
type Done struct {
	Approval string
	Change   Change
	Passkey  string
	Issue    int // the issue that records the action, when it is one
}

// Bind reads the change action a binds to now. An error refuses the action.
func (s *Service) Bind(ctx context.Context, r api.Repo, a Action) (Change, error) {
	var repo records.Repo
	if err := records.Load(ctx, s.store, tenant(r), records.RepoKey(r.FullName), &repo); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		return Change{}, fmt.Errorf("approvals: read the repository: %w", err)
	}
	unapproved := repo.ApprovedDigest == "" && repo.OutsideFlow == nil
	switch {
	case a.Kind == records.KindFirst && !unapproved, a.Kind == records.KindWorkflow && (unapproved || repo.OutsideFlow != nil),
		a.Kind == records.KindConfirm && repo.OutsideFlow == nil:
		return Change{}, ErrNotDue
	case (a.Kind == records.KindFirst || a.Kind == records.KindWorkflow) && a.Pull > 0:
		return s.bindPull(ctx, r, a)
	case a.Kind == records.KindFirst || a.Kind == records.KindConfirm:
		return s.bindTip(ctx, r, a, repo)
	}
	return Change{}, fmt.Errorf("%w: no action %q", ErrNotApprovable, a.Kind)
}

// bindPull binds a first or a workflow approval to the open pull request's head: its policy, which must be of version
// 3, and its workflow file, which it must have. The pull request and the default branch head are read together, then
// the files of the head and of the default branch. The refusals are taken in the order the reads used to run.
func (s *Service) bindPull(ctx context.Context, r api.Repo, a Action) (Change, error) {
	ref := r.RepoRef
	var (
		pull    api.Pull
		pullErr error
		tip     string
		tipErr  error
		first   sync.WaitGroup
	)
	first.Go(func() { pull, pullErr = s.pullAt(ctx, ref, a.Pull) })
	first.Go(func() { tip, tipErr = s.headAt(ctx, ref, r.DefaultBranch) })
	first.Wait()
	switch {
	case api.Permanent(pullErr):
		return Change{}, fmt.Errorf("%w: no pull request #%d", ErrNotApprovable, a.Pull)
	case pullErr != nil:
		return Change{}, fmt.Errorf("approvals: read pull request %d: %w", a.Pull, pullErr)
	case pull.State != "open" || pull.BaseRef != r.DefaultBranch || !strings.EqualFold(pull.HeadRepo, r.FullName) || pull.HeadSHA == "" ||
		a.Kind == records.KindWorkflow && pull.BaseSHA == "":
		return Change{}, fmt.Errorf("%w: #%d is not an open pull request of this repository into %s", ErrNotApprovable,
			a.Pull, r.DefaultBranch)
	case tipErr != nil:
		return Change{}, fmt.Errorf("approvals: read the default branch head: %w", tipErr)
	}
	var (
		head, run, target, current, base                []byte
		headErr, runErr, targetErr, currentErr, baseErr error
		second                                          sync.WaitGroup
	)
	second.Go(func() { head, headErr = s.fileAt(ctx, ref, pull.HeadSHA, PolicyPath) })
	second.Go(func() { run, runErr = s.fileAt(ctx, ref, pull.HeadSHA, WorkflowPath) })
	if a.Kind == records.KindWorkflow {
		second.Go(func() { target, targetErr = s.fileAt(ctx, ref, tip, WorkflowPath) })
		second.Go(func() { base, baseErr = s.fileAt(ctx, ref, pull.BaseSHA, PolicyPath) })
	} else {
		// An onboarding pull request brings the first valid policy: a default branch that has one already is
		// approved as it is (bindTip).
		second.Go(func() { current, currentErr = s.fileAt(ctx, ref, tip, PolicyPath) })
	}
	second.Wait()
	if headErr != nil {
		return Change{}, headErr
	}
	if _, derr := policy3.Decode(head); head == nil || derr != nil {
		return Change{}, fmt.Errorf("%w: the head has no valid policy", ErrNotApprovable)
	}
	if runErr != nil {
		return Change{}, runErr
	}
	if run == nil {
		return Change{}, fmt.Errorf("%w: the head has no %s", ErrNotApprovable, WorkflowPath)
	}
	if a.Kind == records.KindWorkflow {
		if targetErr != nil {
			return Change{}, targetErr
		}
		if target != nil && records.FileDigest(target) == records.FileDigest(run) {
			return Change{}, fmt.Errorf("%w: the pull request does not change %s", ErrNotApprovable, WorkflowPath)
		}
		if baseErr != nil {
			return Change{}, baseErr
		}
	} else {
		if currentErr != nil {
			return Change{}, currentErr
		}
		if _, derr := policy3.Decode(current); current != nil && derr == nil {
			return Change{}, fmt.Errorf("%w: %s has a valid policy; approve it as it is", ErrNotApprovable, r.DefaultBranch)
		}
	}
	c := Change{Kind: a.Kind, Pull: a.Pull, HeadSHA: pull.HeadSHA, Policy: policy3.Digest(head),
		Workflow: records.FileDigest(run)}
	if a.Kind == records.KindWorkflow {
		var err error
		if c.Base, err = s.policyBase(ctx, ref, pull, c.Policy, base); err != nil {
			return Change{}, err
		}
	}
	return c, nil
}

// policyBase is Change.Base of the open pull request pull, whose head's policy has digest policy and whose base
// branch's policy at pull.BaseSHA is atBase. GitHub's base SHA is the base branch's tip when the pull request was last
// synchronized, not the commit its branch started from: a branch cut before a later policy change differs from it with
// no change of its own. Only the merge base tells, so it is read when the two differ.
func (s *Service) policyBase(ctx context.Context, ref api.RepoRef, pull api.Pull, policy string, atBase []byte) (string, error) {
	if atBase != nil && policy3.Digest(atBase) == policy {
		return policy, nil
	}
	full, err := s.gh.Pull(ctx, ref, pull.Number)
	switch {
	case err != nil:
		return "", fmt.Errorf("approvals: read the merge base of pull request %d: %w", pull.Number, err)
	case full.HeadSHA != pull.HeadSHA:
		return "", fmt.Errorf("%w: pull request #%d has a new head", ErrMoved, pull.Number)
	case full.MergeBaseSHA == "":
		return "", fmt.Errorf("approvals: pull request #%d has no merge base", pull.Number)
	}
	raw, err := s.fileAt(ctx, ref, full.MergeBaseSHA, PolicyPath)
	if err != nil || raw == nil {
		return "", err
	}
	return policy3.Digest(raw), nil
}

// bindTip binds a first approval of the policy on the default branch, or a confirm, to the default branch's tip. A
// confirm binds only the digest the state recorded: a later change waits for its own judgement.
func (s *Service) bindTip(ctx context.Context, r api.Repo, a Action, repo records.Repo) (Change, error) {
	tip, err := s.headAt(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return Change{}, fmt.Errorf("approvals: read the default branch head: %w", err)
	}
	raw, err := s.fileAt(ctx, r.RepoRef, tip, PolicyPath)
	if err != nil {
		return Change{}, err
	}
	if _, derr := policy3.Decode(raw); raw == nil || derr != nil {
		return Change{}, fmt.Errorf("%w: %s has no valid policy", ErrNotApprovable, r.DefaultBranch)
	}
	digest := policy3.Digest(raw)
	if a.Kind == records.KindConfirm && digest != repo.OutsideFlow.Digest {
		return Change{}, fmt.Errorf("%w: the policy on %s changed again; wait for its judgement", ErrMoved, r.DefaultBranch)
	}
	return Change{Kind: a.Kind, Commit: tip, Policy: digest}, nil
}

// Review checks a first-policy or workflow review without redeeming a passkey or writing.
// digest binds the check to the same files the read-only owner view verified.
func (s *Service) Review(ctx context.Context, r api.Repo, a Action, u Owner, digest string) error {
	if a.Kind != records.KindFirst && a.Kind != records.KindWorkflow {
		return ErrNotApprovable
	}
	c, err := s.Bind(ctx, r, a)
	if err != nil {
		return err
	}
	if c.Digest() != digest {
		return ErrMoved
	}
	if err := s.notAlreadyApproved(ctx, r, c); err != nil {
		return err
	}
	_, err = s.mayAct(ctx, r, Input{Action: a, Owner: u}, c)
	return err
}

// Act makes an owner action outside requests: it binds the change again, checks it is the one the owner's
// step-up was bound to and who may make it, redeems the step-up, records the approval and records it on GitHub
// (design 19). An error means nothing was recorded; a token Redeem used stays used.
func (s *Service) Act(ctx context.Context, r api.Repo, in Input) (Done, error) {
	c, err := s.Bind(ctx, r, in.Action)
	if err != nil {
		return Done{}, err
	}
	if c.Digest() != in.Digest {
		return Done{}, ErrMoved
	}
	if err := s.notAlreadyApproved(ctx, r, c); err != nil {
		return Done{}, err
	}
	owners, err := s.mayAct(ctx, r, in, c)
	if err != nil {
		return Done{}, err
	}
	if in.Redeem == nil {
		return Done{}, ErrNeedsPasskey
	}
	passkey, err := in.Redeem(ctx, c.Digest())
	if err != nil {
		return Done{}, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Done{}, err
	}
	a := records.Approval{Kind: c.Kind, Change: raw, Owner: in.Owner.ID, Login: in.Owner.Login, Credential: passkey,
		At: s.now().UTC()}
	done := Done{Approval: records.NewID(), Change: c, Passkey: passkey}
	if done.Issue, err = s.record(ctx, r, done.Approval, a, c); err != nil {
		return Done{}, err
	}
	if c.OwnerVerdict() {
		s.checkApproved(ctx, r, done, in.Owner.Login)
	}
	if c.Kind == records.KindConfirm {
		// The confirm ended the state an open revert was made in: its head must not restore the old policy over it.
		if err := s.retireStaleReverts(ctx, r); err != nil {
			logFailure(ctx, r, "retire a stale revert's head", err)
		}
	}
	s.noteDone(ctx, r, &done, in.Owner.Login, owners)
	slog.Default().InfoContext(ctx, "approvals owner action", "installation_id", r.InstallationID, "repository_id", r.ID,
		"kind", c.Kind, "pull", c.Pull, "approval", done.Approval)
	return done, nil
}

// mayAct applies who acts: a first approval, a repository administrator who is also an owner of the policy being
// approved; any other action, an owner named in the approved policy. It returns the owners
// that the GitHub record is assigned to.
func (s *Service) mayAct(ctx context.Context, r api.Repo, in Input, c Change) ([]string, error) {
	named := func(owners []string) bool {
		for _, o := range owners {
			if in.Owner.Login != "" && strings.EqualFold(trimLogin(o), in.Owner.Login) {
				return true
			}
		}
		return false
	}
	if c.Kind == records.KindFirst {
		if !in.Owner.Admin {
			return nil, ErrNotAdmin
		}
		commit := c.Commit
		if c.Pull > 0 {
			commit = c.HeadSHA
		}
		owners, err := s.ownersAt(ctx, r.RepoRef, commit)
		if err != nil {
			return nil, err
		}
		if !named(owners) {
			return nil, ErrNotAdmin
		}
		return owners, nil
	}
	var repo records.Repo
	if err := records.Load(ctx, s.store, tenant(r), records.RepoKey(r.FullName), &repo); err != nil {
		return nil, fmt.Errorf("approvals: read the repository: %w", err)
	}
	if repo.ApprovedCommit == "" {
		return nil, ErrNotOwner
	}
	owners, err := s.ownersAt(ctx, r.RepoRef, repo.ApprovedCommit)
	if err != nil {
		return nil, err
	}
	if !named(owners) {
		return nil, ErrNotOwner
	}
	return owners, nil
}

// ownersAt are the owners of the policy at commit; none when it has no valid policy.
func (s *Service) ownersAt(ctx context.Context, ref api.RepoRef, commit string) ([]string, error) {
	raw, err := s.fileAt(ctx, ref, commit, PolicyPath)
	if err != nil {
		return nil, err
	}
	pol, err := policy3.Decode(raw)
	if raw == nil || err != nil {
		return nil, nil
	}
	return pol.Owners, nil
}

// record writes the approval of change c. A first or a workflow approval of a pull request joins that pull request's
// proposal, which its merge push consumes; a first approval of the default branch's policy and a confirm take effect
// at once. It returns the issue of a state a confirm ended.
func (s *Service) record(ctx context.Context, r api.Repo, id string, a records.Approval, c Change) (int, error) {
	t, name := tenant(r), r.FullName
	var err error
	issue := 0
	switch {
	case c.Pull > 0:
		var proposal string
		if proposal, err = s.proposalOf(ctx, r, c.Pull); err != nil {
			return 0, err
		}
		b := records.Bound{Proposal: proposal, PullRequest: c.Pull, HeadSHA: c.HeadSHA, Digest: c.Policy, WorkflowDigest: c.Workflow}
		if c.Kind == records.KindFirst {
			err = records.First(ctx, s.store, t, name, id, a, b)
		} else {
			err = records.Workflow(ctx, s.store, t, name, id, a, b)
		}
	case c.Kind == records.KindFirst:
		err = records.ApproveCurrent(ctx, s.store, t, name, id, a, c.Commit, c.Policy)
	default:
		issue, err = records.Confirm(ctx, s.store, t, name, id, a, c.Commit, c.Policy)
	}
	if errors.Is(err, records.ErrMoved) {
		return 0, ErrMoved
	}
	if err != nil {
		return 0, fmt.Errorf("approvals: record the %s approval: %w", c.Kind, err)
	}
	return issue, nil
}

// proposalOf is the id of the open proposal of pull request pull, or a new one.
func (s *Service) proposalOf(ctx context.Context, r api.Repo, pull int) (string, error) {
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, tenant(r), records.ProposalIndexKey(r.FullName), &index); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		return "", fmt.Errorf("approvals: read the proposal index: %w", err)
	}
	if id, ok := index.Lookup(records.PullEntry(pull)); ok {
		return id, nil
	}
	return records.NewID(), nil
}

// noteDone records the action on GitHub, naming the owner and the passkey (design 19): a first or a workflow approval
// on its pull request; a first approval of the default branch's policy on a new issue assigned to the owners; a
// confirm on the issue of the state it ended, which it closes. It is a notification: the approval stands, and a
// failure is logged.
func (s *Service) noteDone(ctx context.Context, r api.Repo, d *Done, login string, owners []string) {
	c := d.Change
	switch {
	case c.Pull > 0:
		s.note(ctx, r, c.Pull, approvedPullNote(c, login, d.Passkey))
	case c.Kind == records.KindFirst:
		created, err := s.gh.CreateIssue(ctx, r.RepoRef, api.NewIssue{Title: firstTitle,
			Body: firstBody(r.DefaultBranch, c, login, d.Passkey)})
		if err != nil {
			logFailure(ctx, r, "open the issue of the first approval", err)
			return
		}
		d.Issue = created.Number
		s.assign(ctx, r, created.Number, owners)
	case d.Issue != 0:
		s.note(ctx, r, d.Issue, confirmedNote(c, login, d.Passkey))
		if err := s.gh.CloseIssue(ctx, r.RepoRef, d.Issue); err != nil {
			logFailure(ctx, r, "close the issue of a confirmed policy", err)
		}
	}
}

// pullAt, headAt and fileAt are the reads of an owner's action. Under inputs.WithReadMemo each is read once per request.
func (s *Service) pullAt(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	return inputs.Memo(ctx, inputs.PullRead{Repo: r, Number: number}, func() (api.Pull, error) {
		return s.gh.PullSummary(ctx, r, number)
	})
}

func (s *Service) headAt(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	return inputs.Memo(ctx, inputs.BranchRead{Repo: r, Branch: branch}, func() (string, error) {
		return s.gh.BranchHead(ctx, r, branch)
	})
}

// fileAt reads path at a commit; a missing file is nil. The bytes are shared under a memo: callers must not change them.
func (s *Service) fileAt(ctx context.Context, ref api.RepoRef, at, path string) ([]byte, error) {
	raw, err := inputs.Memo(ctx, inputs.FileRead{Repo: ref, At: at, Path: path}, func() ([]byte, error) {
		raw, _, err := s.gh.File(ctx, ref, at, path)
		return raw, err
	})
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("approvals: read %s at %s: %w", path, at, err)
	}
	return raw, nil
}
