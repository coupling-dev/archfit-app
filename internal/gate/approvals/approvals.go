// Package approvals judges each push to the default branch that changes the policy (design 7.1, 9.2; M2 task 20): the
// merge of a proposal with the policy its approvals wrote consumes them, once; any other change of the policy puts the
// repository outside the flow, where every check is unknown until an owner confirms or reverts it (task 23).
package approvals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// PolicyPath is the policy file.
const PolicyPath = ".archfit.yaml"

// maxAssignees is GitHub's limit of assignees on an issue.
const maxAssignees = 10

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
	Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
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
}

// New returns a Service over gh and s.
func New(gh GitHub, s store.Store) *Service { return &Service{gh: gh, store: s} }

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

// Pushed judges push p once: a delivery of the same push again, late or after later pushes, finds the recorded
// judgement and only finishes what it left (the issue, the hand-off). An error is a failed read or write; the caller
// retries the delivery.
func (s *Service) Pushed(ctx context.Context, p Push) (Outcome, error) {
	if len(p.ChangedPaths) > 0 && !slices.Contains(p.ChangedPaths, PolicyPath) {
		return Outcome{Judged: JudgedNone}, nil
	}
	t, name := tenant(p.Repo), p.Repo.FullName
	var was records.PolicyPush
	switch err := records.Load(ctx, s.store, t, records.PolicyPushKey(name, p.After), &was); {
	case err == nil && was.Proposal != "":
		return s.consumed(ctx, p.Repo, was.Proposal)
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

	merged, err := s.mergedBy(ctx, p.Repo, p.After)
	if err != nil {
		return Outcome{}, err
	}
	voided := ""
	if merged != nil {
		if merged.rec.ExpectedDigest == digest {
			if err := records.Consume(ctx, s.store, t, name, merged.id, p.After, digest); err != nil {
				return Outcome{}, fmt.Errorf("approvals: consume proposal %s: %w", merged.id, err)
			}
			return s.consumed(ctx, p.Repo, merged.id)
		}
		// A merge commit is one commit: this proposal can never be consumed. Its approvals are void and their requests
		// open again, or they would cover their keys for ever.
		if err := s.void(ctx, p.Repo, merged); err != nil {
			return Outcome{}, err
		}
		voided = merged.id
	}

	var r records.Repo
	if err := records.Load(ctx, s.store, t, records.RepoKey(name), &r); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return Outcome{}, fmt.Errorf("approvals: read the repository: %w", err)
	}
	if r.ApprovedDigest == "" && r.OutsideFlow == nil {
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
	r, marked, err := records.Outside(ctx, s.store, t, p.Repo.FullName, p.After, digest)
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

// proposal is a proposal record and its id.
type proposal struct {
	id  string
	rec records.Proposal
}

// mergedBy is the open proposal whose pull request merged as commit, or nil.
func (s *Service) mergedBy(ctx context.Context, r api.Repo, commit string) (*proposal, error) {
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
		if p.rec.State != records.StateOpen {
			continue
		}
		pull, err := s.gh.Pull(ctx, r.RepoRef, p.rec.PullRequest)
		if err != nil {
			return nil, fmt.Errorf("approvals: read pull request %d: %w", p.rec.PullRequest, err)
		}
		if pull.Merged && pull.MergeCommitSHA == commit {
			return p, nil
		}
	}
	return nil, nil
}

// void voids the approvals of p, notes and reopens the requests that opened again, then voids p: approvals first,
// so a cleanup that stops half way is finished by the delivery again, which finds p still indexed.
func (s *Service) void(ctx context.Context, r api.Repo, p *proposal) error {
	t := tenant(r)
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
	if err := records.VoidProposal(ctx, s.store, t, r.FullName, p.id); err != nil {
		return fmt.Errorf("approvals: void proposal %s: %w", p.id, err)
	}
	return nil
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
