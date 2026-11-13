// Package approvals judges each push to the default branch that changes the policy (design 7.1, 9.2; M2 task 20): the
// merge of a proposal with the policy its approvals wrote consumes them, once; in a repository with an approved policy,
// any other change of the policy puts it outside the flow, where every check is unknown until an owner confirms or
// reverts it (task 23). A repository without an approved policy is not under change control yet.
package approvals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// PolicyPath is the policy file.
const PolicyPath = ".archfit.yaml"

// WorkflowPath is the workflow file of M2 D15: a pull request runs its own copy, so a change to it needs an owner.
const WorkflowPath = ".github/workflows/archfit.yaml"

// maxAssignees is GitHub's limit of assignees on an issue.
const maxAssignees = 10

// The compare statuses that put a commit on the history of another: the same commit, or behind it.
const (
	compareIdentical = "identical"
	compareAhead     = "ahead"
	compareBehind    = "behind"
)

// voidRounds is how often a void reads a proposal that keeps gaining an approval; approveRounds how often a consume
// reads an approved commit that keeps moving.
const (
	voidRounds    = 3
	approveRounds = 3
)

// The judgements of a push.
const (
	JudgedNone      = "none"      // the push left the policy as it was
	JudgedUnguarded = "unguarded" // the repository has no approved policy yet: change control starts with the first approval
	JudgedConsumed  = "consumed"  // the push merged a proposal with its expected policy: its approvals are used
	JudgedOutside   = "outside"   // the policy changed with no recorded approval
)

// GitHub is what the judgement reads and writes on GitHub.
type GitHub interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) (content []byte, blobSHA string, err error)
	BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error)
	Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
	Compare(ctx context.Context, r api.RepoRef, base, head string) (api.Comparison, error)
	CreateIssue(ctx context.Context, r api.RepoRef, i api.NewIssue) (api.Issue, error)
	CreateComment(ctx context.Context, r api.RepoRef, number int, body string) (api.Comment, error)
	CloseIssue(ctx context.Context, r api.RepoRef, number int) error
	ReopenIssue(ctx context.Context, r api.RepoRef, number int) error
	SetAssigneesAndLabels(ctx context.Context, r api.RepoRef, number int, assignees, labels []string) ([]string, []string, error)
}

// Service judges the pushes of the repositories of one App.
type Service struct {
	gh    GitHub
	store store.Store
	now   func() time.Time // the time an owner action records
}

// New returns a Service over gh and s.
func New(gh GitHub, s store.Store) *Service { return &Service{gh: gh, store: s, now: time.Now} }

// Push is a push to the default branch.
type Push struct {
	Repo         api.Repo // DefaultBranch names the branch
	Before       string   // the commit before the push; "" when unknown
	After        string   // the pushed commit
	ChangedPaths []string // empty: unknown
}

// Outcome is the judgement of a push.
type Outcome struct {
	Judged string
	// Proposal is the consumed proposal; Requests are the requests of its approvals and Waiting the pull requests
	// waiting on them, for the re-check after an answer (task 21).
	Proposal string
	Requests []int
	Waiting  []int
	Issue    int    // outside: the issue of the state
	Voided   string // a proposal whose pull request merged another policy: it can never be consumed
}

// Pushed judges push p once: a delivery of the same push (the same commits before and after) again, late or after
// later pushes, finds the recorded judgement and only finishes what it left (the issue, the hand-off). A push that
// moves the branch back to a commit judged before is another push, judged again. An error is a failed read or write;
// the caller retries the delivery.
func (s *Service) Pushed(ctx context.Context, p Push) (Outcome, error) {
	if len(p.ChangedPaths) > 0 && !slices.Contains(p.ChangedPaths, PolicyPath) {
		return Outcome{Judged: JudgedNone}, nil
	}
	t, name, push := tenant(p.Repo), p.Repo.FullName, records.Push{Before: p.Before, After: p.After}
	var was records.PolicyPush
	switch err := records.Load(ctx, s.store, t, records.PolicyPushKey(name, push), &was); {
	case err == nil && was.Proposal != "":
		return s.consumed(ctx, p.Repo, was.Proposal)
	case err == nil && was.Unguarded:
		return Outcome{Judged: JudgedUnguarded}, nil
	case err == nil:
		return s.outside(ctx, p, was.Digest)
	case !errors.Is(err, records.ErrNoRecord):
		return Outcome{}, fmt.Errorf("approvals: read the judgement of %s: %w", p.After, err)
	}

	after, err := s.policy(ctx, p.Repo.RepoRef, p.After)
	if err != nil {
		return Outcome{}, err
	}
	digest := digestOf(after)
	if p.Before != "" {
		before, err := s.policy(ctx, p.Repo.RepoRef, p.Before)
		if err != nil {
			return Outcome{}, err
		}
		if digestOf(before) == digest {
			return Outcome{Judged: JudgedNone}, nil
		}
	}

	merged, err := s.mergedIn(ctx, p, false)
	if err != nil {
		return Outcome{}, err
	}
	voided := ""
	if merged != nil {
		admits := merged.rec.ExpectedDigest == digest
		if admits {
			// A first approval admits the workflow of its head too: a merge with another one uses none of it.
			if admits, err = s.landedWorkflow(ctx, p.Repo.RepoRef, merged); err != nil {
				return Outcome{}, err
			}
		}
		if admits {
			if err := s.consume(ctx, p.Repo, merged, push, digest); err != nil {
				return Outcome{}, err
			}
			s.closeReverted(ctx, p.Repo, merged)
			return s.consumed(ctx, p.Repo, merged.id)
		}
		// A merge commit is one commit, landed once: this proposal can never be consumed. Its approvals are void and
		// their requests open again, or they would cover their keys for ever.
		if err := s.void(ctx, p.Repo, merged); err != nil {
			return Outcome{}, err
		}
		voided = merged.id
	}

	var r records.Repo
	if err := records.Load(ctx, s.store, t, records.RepoKey(name), &r); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return Outcome{}, fmt.Errorf("approvals: read the repository: %w", err)
	}
	unguarded := r.ApprovedDigest == "" && r.OutsideFlow == nil
	if !unguarded && r.GuardedSince != "" {
		// A push that happened before the first approval was made outside change control, whenever it is delivered.
		if unguarded, err = s.preceded(ctx, p, r.GuardedSince); err != nil {
			return Outcome{}, err
		}
	}
	if unguarded {
		// Recorded, so a delivery of this push again after the first consume is not judged outside the flow.
		if err := records.Unguarded(ctx, s.store, t, name, push, digest); err != nil {
			return Outcome{}, fmt.Errorf("approvals: record the push to a repository without an approved policy: %w", err)
		}
		return Outcome{Judged: JudgedUnguarded, Voided: voided}, nil
	}
	out, err := s.outside(ctx, p, digest)
	out.Voided = voided
	return out, err
}

// outside records the push as a policy changed outside the flow and makes sure the state has its one issue: opened to
// the owners when none is recorded, else noted when this push changed the policy again.
func (s *Service) outside(ctx context.Context, p Push, digest string) (Outcome, error) {
	t, ref := tenant(p.Repo), p.Repo.RepoRef
	r, marked, err := records.Outside(ctx, s.store, t, p.Repo.FullName, records.Push{Before: p.Before, After: p.After}, digest)
	if err != nil {
		return Outcome{}, fmt.Errorf("approvals: record the policy changed outside the flow: %w", err)
	}
	out := Outcome{Judged: JudgedOutside}
	if r.OutsideFlow == nil {
		return out, nil // a redelivery after an owner ended the state (task 23)
	}
	if out.Issue = r.OutsideFlow.Issue; out.Issue != 0 {
		if marked {
			s.note(ctx, p.Repo, out.Issue, changedAgainNote(p.After))
		}
		return out, nil
	}

	owners, err := s.owners(ctx, ref, p.Before, p.After)
	if err != nil {
		return Outcome{}, err
	}
	created, err := s.gh.CreateIssue(ctx, ref, api.NewIssue{Title: outsideTitle, Body: outsideBody(p.Repo.DefaultBranch, p.After)})
	if err != nil {
		return Outcome{}, fmt.Errorf("approvals: open the issue of the policy changed outside the flow: %w", err)
	}
	s.assign(ctx, p.Repo, created.Number, owners)
	recorded, err := records.OutsideIssue(ctx, s.store, t, p.Repo.FullName, created.Number)
	if err != nil {
		return Outcome{}, fmt.Errorf("approvals: record issue %d: %w", created.Number, err)
	}
	if recorded != created.Number { // another delivery of the same push opened its issue first
		s.note(ctx, p.Repo, created.Number, duplicateNote(recorded))
		if err := s.gh.CloseIssue(ctx, ref, created.Number); err != nil {
			logFailure(ctx, p.Repo, "close a second issue", err)
		}
	}
	out.Issue = recorded
	return out, nil
}

// owners are the owners of the policy at before, the policy the owners stood behind, or at after when before has
// none. A missing or unreadable policy names none: the issue is then assigned to nobody.
func (s *Service) owners(ctx context.Context, ref api.RepoRef, before, after string) ([]string, error) {
	for _, at := range []string{before, after} {
		if at == "" {
			continue
		}
		raw, err := s.policy(ctx, ref, at)
		if err != nil {
			return nil, err
		}
		if pol, err := policy3.Decode(raw); raw != nil && err == nil {
			return pol.Owners, nil
		}
	}
	return nil, nil
}

// assign assigns the owners to the issue. It is a notification, never authority: a failure is logged.
func (s *Service) assign(ctx context.Context, r api.Repo, issue int, owners []string) {
	var logins []string
	for _, o := range owners {
		if login := trimLogin(o); login != "" && !slices.Contains(logins, login) && len(logins) < maxAssignees {
			logins = append(logins, login)
		}
	}
	if len(logins) == 0 {
		return
	}
	if _, _, err := s.gh.SetAssigneesAndLabels(ctx, r.RepoRef, issue, logins, nil); err != nil {
		logFailure(ctx, r, "assign the owners", err)
	}
}

// proposal is a proposal record, its id and, once its pull request merged, its merge commit.
type proposal struct {
	id     string
	rec    records.Proposal
	commit string
}

// mergedIn is the open proposal whose pull request's merge commit this push landed, or nil: the pushed commit itself,
// or, when a merge queue landed several pull requests at once, a commit after the commit before the push and on the
// history of the pushed one. workflow picks the workflow proposals, which the workflow push consumes
// (WorkflowPushed), else every other one, which the policy push consumes.
func (s *Service) mergedIn(ctx context.Context, push Push, workflow bool) (*proposal, error) {
	r := push.Repo
	t := tenant(r)
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, t, records.ProposalIndexKey(r.FullName), &index); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		return nil, fmt.Errorf("approvals: read the proposal index: %w", err)
	}
	for _, e := range index.Entries {
		p := &proposal{id: e.ID}
		if err := records.Load(ctx, s.store, t, records.ProposalKey(r.FullName, e.ID), &p.rec); err != nil {
			return nil, fmt.Errorf("approvals: read proposal %s: %w", e.ID, err)
		}
		if p.rec.State != records.StateOpen || (p.rec.Kind == records.ProposalWorkflow) != workflow {
			continue
		}
		pull, err := s.gh.Pull(ctx, r.RepoRef, p.rec.PullRequest)
		if err != nil {
			return nil, fmt.Errorf("approvals: read pull request %d: %w", p.rec.PullRequest, err)
		}
		if !pull.Merged || pull.MergeCommitSHA == "" {
			continue
		}
		in := pull.MergeCommitSHA == push.After
		if !in {
			if in, err = s.landed(ctx, r.RepoRef, pull.MergeCommitSHA, push); err != nil {
				return nil, err
			}
		}
		if in {
			p.commit = pull.MergeCommitSHA
			return p, nil
		}
	}
	return nil, nil
}

// landed reports whether commit is on the history of the pushed commit and not of the commit before the push.
func (s *Service) landed(ctx context.Context, ref api.RepoRef, commit string, push Push) (bool, error) {
	onAfter, err := s.onHistory(ctx, ref, commit, push.After)
	if err != nil || !onAfter || push.Before == "" {
		return onAfter, err
	}
	onBefore, err := s.onHistory(ctx, ref, commit, push.Before)
	return !onBefore, err
}

// preceded reports whether push happened before commit: the commit before the push is strictly behind commit, and the
// pushed commit is on commit's history. A push that moves the branch back from a later commit did not. A push that
// created the branch has no commit before: its pushed commit strictly behind commit decides.
func (s *Service) preceded(ctx context.Context, push Push, commit string) (bool, error) {
	switch {
	case push.Before == "" && push.After == commit:
		return false, nil
	case push.Before == "":
		return s.onHistory(ctx, push.Repo.RepoRef, push.After, commit)
	case push.Before == commit:
		return false, nil
	}
	behind, err := s.onHistory(ctx, push.Repo.RepoRef, push.Before, commit)
	if err != nil || !behind {
		return false, err
	}
	return s.onHistory(ctx, push.Repo.RepoRef, push.After, commit)
}

// onHistory reports whether commit is head or behind it. A commit GitHub does not know, or one it cannot compare, is
// not.
func (s *Service) onHistory(ctx context.Context, ref api.RepoRef, commit, head string) (bool, error) {
	c, err := s.gh.Compare(ctx, ref, commit, head)
	switch {
	case api.Permanent(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("approvals: compare %s with %s: %w", commit, head, err)
	}
	return c.Status == compareIdentical || c.Status == compareAhead, nil
}

// consume consumes the merged proposal p. It approves the digest only when the default branch's tip holds it now and
// p's merge commit comes after the repository's approved commit; a consume that lands in between
// (records.ErrApprovedMoved) makes it read both again.
func (s *Service) consume(ctx context.Context, r api.Repo, p *proposal, push records.Push, digest string) error {
	t := tenant(r)
	for range approveRounds {
		var repo records.Repo
		if err := records.Load(ctx, s.store, t, records.RepoKey(r.FullName), &repo); err != nil &&
			!errors.Is(err, records.ErrNoRecord) {
			return fmt.Errorf("approvals: read the repository: %w", err)
		}
		approve, err := s.approves(ctx, r, repo.ApprovedCommit, p.commit, digest)
		if err != nil {
			return err
		}
		m := records.Merge{Proposal: p.id, Commit: p.commit, Digest: digest, Push: push, AtTip: approve, Over: repo.ApprovedCommit}
		err = records.Consume(ctx, s.store, t, r.FullName, m)
		if !errors.Is(err, records.ErrApprovedMoved) {
			if err != nil {
				return fmt.Errorf("approvals: consume proposal %s: %w", p.id, err)
			}
			return nil
		}
	}
	return fmt.Errorf("approvals: consume proposal %s: %w", p.id, store.ErrConflict)
}

// approves reports whether the consume of commit approves digest: the default branch's tip holds digest now, and commit
// is not older than the approved commit. When the two have no order (a history rewrite left the approved commit off
// the branch, or GitHub no longer knows it), the tip alone decides, so a rewrite never stops every later approval.
func (s *Service) approves(ctx context.Context, r api.Repo, approved, commit, digest string) (bool, error) {
	tip, err := s.tipDigest(ctx, r)
	if err != nil || tip != digest {
		return false, err
	}
	if approved == "" {
		return true, nil
	}
	c, err := s.gh.Compare(ctx, r.RepoRef, approved, commit)
	switch {
	case api.Permanent(err):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("approvals: compare %s with %s: %w", approved, commit, err)
	}
	return c.Status != compareBehind, nil
}

// tipDigest is the policy digest at the default branch's tip now; "" when it has none or the branch is not named.
func (s *Service) tipDigest(ctx context.Context, r api.Repo) (string, error) {
	if r.DefaultBranch == "" {
		return "", nil
	}
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return "", fmt.Errorf("approvals: read the default branch head: %w", err)
	}
	raw, err := s.policy(ctx, r.RepoRef, tip)
	return digestOf(raw), err
}

// void voids the approvals of p, notes and reopens the requests that opened again, then voids p: approvals first,
// so a cleanup that stops half way is finished by the delivery again, which finds p still indexed. An answer that
// joined p meanwhile (records.ErrApprovalsLeft) is read and voided too.
func (s *Service) void(ctx context.Context, r api.Repo, p *proposal) error {
	t := tenant(r)
	for range voidRounds {
		for _, id := range p.rec.Approvals {
			request, reopened, err := records.Void(ctx, s.store, t, r.FullName, id)
			if err != nil {
				return fmt.Errorf("approvals: void approval %s: %w", id, err)
			}
			if reopened {
				s.note(ctx, r, request, mergedOtherNote(p.rec.PullRequest))
				if err := s.gh.ReopenIssue(ctx, r.RepoRef, request); err != nil {
					logFailure(ctx, r, "reopen a request", err)
				}
			}
		}
		err := records.VoidProposal(ctx, s.store, t, r.FullName, p.id)
		if !errors.Is(err, records.ErrApprovalsLeft) {
			if err != nil {
				return fmt.Errorf("approvals: void proposal %s: %w", p.id, err)
			}
			return nil
		}
		if err := records.Load(ctx, s.store, t, records.ProposalKey(r.FullName, p.id), &p.rec); err != nil {
			return fmt.Errorf("approvals: read proposal %s: %w", p.id, err)
		}
	}
	return fmt.Errorf("approvals: void proposal %s: %w", p.id, store.ErrConflict)
}

// consumed is the outcome of the consumed proposal id: its requests and the pull requests waiting on them.
func (s *Service) consumed(ctx context.Context, r api.Repo, id string) (Outcome, error) {
	t := tenant(r)
	var p records.Proposal
	if err := records.Load(ctx, s.store, t, records.ProposalKey(r.FullName, id), &p); err != nil {
		return Outcome{}, fmt.Errorf("approvals: read proposal %s: %w", id, err)
	}
	out := Outcome{Judged: JudgedConsumed, Proposal: id}
	for _, aid := range p.Approvals {
		var a records.Approval
		if err := records.Load(ctx, s.store, t, records.ApprovalKey(r.FullName, aid), &a); err != nil {
			return Outcome{}, fmt.Errorf("approvals: read approval %s: %w", aid, err)
		}
		if a.Request == 0 {
			continue
		}
		out.Requests = append(out.Requests, a.Request)
		var req records.Request
		if err := records.Load(ctx, s.store, t, records.RequestKey(r.FullName, a.Request), &req); err != nil {
			return Outcome{}, fmt.Errorf("approvals: read request %d: %w", a.Request, err)
		}
		for _, pull := range req.Waiting {
			if !slices.Contains(out.Waiting, pull) {
				out.Waiting = append(out.Waiting, pull)
			}
		}
	}
	slices.Sort(out.Waiting)
	return out, nil
}

// policy reads the policy at a commit; a missing file is nil.
func (s *Service) policy(ctx context.Context, ref api.RepoRef, at string) ([]byte, error) {
	raw, _, err := s.gh.File(ctx, ref, at, PolicyPath)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("approvals: read the policy at %s: %w", at, err)
	}
	return raw, nil
}

// digestOf is the policy digest of raw, "" for no policy.
func digestOf(raw []byte) string {
	if raw == nil {
		return ""
	}
	return policy3.Digest(raw)
}

// note posts a note on an issue. It is a notification: a failure is logged.
func (s *Service) note(ctx context.Context, r api.Repo, issue int, body string) {
	if _, err := s.gh.CreateComment(ctx, r.RepoRef, issue, body); err != nil {
		logFailure(ctx, r, "note on an issue", err)
	}
}

func tenant(r api.Repo) store.Tenant { return store.Tenant(r.InstallationID) }

// logFailure logs a failed notification with identifiers only.
func logFailure(ctx context.Context, r api.Repo, step string, err error) {
	slog.Default().WarnContext(ctx, "approvals step failed", "installation_id", r.InstallationID,
		"repository_id", r.ID, "step", step, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
}
