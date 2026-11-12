// Package proposals carries owners' answers to requests to the default branch (design 9.2, 10.4; M2 task 19). An
// answer is recorded as an approval; the approvals of one repository travel together in the App's policy pull request
// on the branch archfit/policy (the proposal). The App writes that branch, sets archfit/gate on its pull request when
// the conditions of M2 D13 hold, and merges it, unless the owner chose to merge later.
package proposals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// Branch is the App's policy branch (M2 D13): every proposal of a repository is written there.
const Branch = "archfit/policy"

const (
	policyPath = ".archfit.yaml"
	gateCheck  = "archfit/gate"
	// One answer, lease wait included, ends within 50 s: inside the App's 55 s deadline of a UI request, and well
	// before its lease (60 s) can be taken over. recordTimeout bounds the reads, the step-up, the branch write and the
	// record, on the caller's context. afterTimeout bounds what follows the record, on a context the caller cannot
	// cancel: the answer stands, so a closed tab or a deadline must not leave its check, merge or issue half done.
	recordTimeout = 15 * time.Second
	afterTimeout  = 25 * time.Second
	// headWait is how long a merge waits for the pull request to show the head the App just wrote: GitHub moves a pull
	// request's head after the ref moved, not with it.
	headWait = 10 * time.Second
	headPoll = time.Second
)

var (
	// ErrNotOwner refuses an answer by a user the policy on the default branch does not name in owners.
	ErrNotOwner = errors.New("proposals: not an owner named in the policy")
	// ErrNeedsPasskey refuses an answer that loosens the policy without a passkey step-up.
	ErrNeedsPasskey = errors.New("proposals: the answer loosens the policy and needs a passkey")
	// ErrFull refuses an answer that writes a line while the open proposal holds records.MaxApprovals answers: it
	// merges first.
	ErrFull = errors.New("proposals: the policy pull request holds the most answers it can")
	// errRefused is a D13 condition that does not hold, so archfit/gate is not set.
	errRefused = errors.New("proposals: the pull request is not the App's proposal as recorded")
)

// GitHub is what the proposals need of github/api.
type GitHub interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) (content []byte, blobSHA string, err error)
	BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error)
	Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
	Compare(ctx context.Context, r api.RepoRef, base, head string) (api.Comparison, error)
	CommitOn(ctx context.Context, r api.RepoRef, branch, parent, message string, files []api.FileChange, force bool) (string, error)
	EnsurePull(ctx context.Context, r api.RepoRef, p api.NewPull) (api.Pull, error)
	UpdatePullBody(ctx context.Context, r api.RepoRef, number int, body string) error
	MarkReady(ctx context.Context, r api.RepoRef, number int) error
	MergePull(ctx context.Context, r api.RepoRef, number int, headSHA string) (mergeSHA string, err error)
	CreateCheckRun(ctx context.Context, r api.RepoRef, c api.CheckRun) (api.CheckRun, error)
	CreateComment(ctx context.Context, r api.RepoRef, number int, body string) (api.Comment, error)
	CloseIssue(ctx context.Context, r api.RepoRef, number int) error
	ReopenIssue(ctx context.Context, r api.RepoRef, number int) error
}

// Service answers requests. It keeps no state of its own: the Store keeps the records, GitHub the branch.
type Service struct {
	gh    GitHub
	store store.Store
	now   func() time.Time
	// sleep pauses between polls of the lease and of a pull request's head. Tests replace it.
	sleep func(context.Context, time.Duration) error
}

// New returns the Service. A nil clock means time.Now.
func New(gh GitHub, s store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{gh: gh, store: s, now: now, sleep: sleepCtx}
}

// Owner is the signed-in owner who answers.
type Owner struct {
	ID    int64
	Login string
}

// Input is one owner's answer to request Request.
type Input struct {
	Request int
	Answer  requests.Answer
	Owner   Owner
	Later   bool // merge later: the pull request stays open and later answers join it
	// Redeem consumes the passkey step-up token for the change digest and returns the passkey id. It is called only for
	// an answer that loosens the policy, after every other check passed and before anything is written.
	Redeem func(ctx context.Context, digest string) (passkeyID string, err error)
}

// Outcome is what an answer did.
type Outcome struct {
	Approval    string // the approval id
	PullRequest int    // the policy pull request; 0 when the default branch already says it, or it writes no line
	State       string // requests.PullMerged, PullLater or PullRefused when PullRequest is set
	Merge       string // the merge commit, when merged
}

// Digest is the change digest of answer a to request number: what the owner's passkey step-up is bound to.
func (s *Service) Digest(ctx context.Context, r api.Repo, number int, a requests.Answer) (string, error) {
	req, err := s.openRequest(ctx, r, number)
	if err != nil {
		return "", err
	}
	c, err := requests.ChangeOf(number, req, a, s.now().UTC())
	if err != nil {
		return "", err
	}
	return changeDigest(c), nil
}

// Answer records an owner's answer and carries it to the default branch. An error means the answer was not recorded
// (a token Redeem used stays used). On its way an answer may record what it finds, whatever becomes of it: a proposal
// whose pull request was closed without a merge is void, and an approval that no longer applies is void, their
// requests open again. GitHub may hold a branch commit no record names, which the D13 check never accepts. Once the
// approval is recorded the answer stands: the check, the merge and the issue are done as far as GitHub allows, on a
// context the caller cannot cancel, and the Outcome says how far.
func (s *Service) Answer(ctx context.Context, r api.Repo, in Input) (Outcome, error) {
	release, err := s.hold(ctx, r)
	if err != nil {
		return Outcome{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	return s.answer(ctx, r, in)
}

// approved is an open approval of a proposal with its change.
type approved struct {
	id     string
	rec    records.Approval
	change requests.Change
}

// proposal is the open proposal of a repository whose pull request is open, with its open approvals in order.
type proposal struct {
	id        string
	rec       records.Proposal
	approvals []approved
}

func (s *Service) answer(ctx context.Context, r api.Repo, in Input) (Outcome, error) {
	ref, t := r.RepoRef, tenant(r)
	tip, err := s.gh.BranchHead(ctx, ref, r.DefaultBranch)
	if err != nil {
		return Outcome{}, fmt.Errorf("proposals: read the default branch head: %w", err)
	}
	tipFile, pol, err := s.policyAt(ctx, ref, tip)
	if err != nil {
		return Outcome{}, err
	}
	if !slices.ContainsFunc(pol.Owners, func(o string) bool { return in.Owner.Login != "" && strings.EqualFold(o, in.Owner.Login) }) {
		return Outcome{}, ErrNotOwner
	}
	req, err := s.openRequest(ctx, r, in.Request)
	if err != nil {
		return Outcome{}, err
	}
	change, err := requests.ChangeOf(in.Request, req, in.Answer, s.now().UTC())
	if err != nil {
		return Outcome{}, err
	}
	cur, err := s.current(ctx, r)
	if err != nil {
		return Outcome{}, err
	}
	file, kept, voided, err := apply(tipFile, cur)
	if err != nil {
		return Outcome{}, err
	}
	// The answer joins the proposal when the default branch does not say it yet, even when an open answer already
	// writes the same line: it travels, and is voided, with that proposal.
	comment := requests.RequestComment(in.Request)
	alone, err := requests.Edit(tipFile, change, comment)
	if err != nil {
		return Outcome{}, err
	}
	joins, want := !bytes.Equal(alone, tipFile), file
	if joins {
		if want, err = requests.Edit(file, change, comment); err != nil {
			return Outcome{}, err
		}
		if cur != nil && len(kept) >= records.MaxApprovals {
			// Refused before the step-up and the branch write: a full proposal merges before it takes another answer.
			return Outcome{}, ErrFull
		}
	}
	approval, err := s.approval(ctx, in, change)
	if err != nil {
		return Outcome{}, err
	}
	// Approvals that no longer apply leave the proposal before the answer joins it, so its capacity and its pin count
	// only the answers its branch holds.
	reopened, err := s.voidRecords(ctx, r, voided)
	if err != nil {
		return Outcome{}, err
	}

	id := records.NewID()
	out := Outcome{Approval: id}
	var pin *records.Pin
	if joins {
		if pin, err = s.write(ctx, r, cur, tip, want, append(kept, approved{id: id, rec: approval, change: change})); err != nil {
			return Outcome{}, err
		}
	}
	switch err := records.Answer(ctx, s.store, t, r.FullName, id, approval, pin); {
	case errors.Is(err, records.ErrAnswered):
		return Outcome{}, requests.ErrClosedRequest
	case err != nil:
		return Outcome{}, fmt.Errorf("proposals: record the answer: %w", err)
	}

	// The answer stands from here on. What follows is done as far as GitHub allows, whatever becomes of the caller.
	after, cancel := context.WithTimeout(context.WithoutCancel(ctx), afterTimeout)
	defer cancel()
	if !joins && cur != nil && len(voided) > 0 {
		pin = s.resync(after, r, cur, tip, file, kept)
	}
	if cur != nil {
		s.noteReopened(after, r, cur.rec.PullRequest, reopened, false)
	}
	if pin != nil {
		listed := kept
		if joins {
			listed = append(listed, approved{id: id, rec: approval, change: change})
		}
		// An answer that joins nothing never merges the proposal: its other answers chose when to merge.
		state, merge := s.publish(after, r, tip, *pin, listed, in.Later || !joins)
		if joins {
			out.PullRequest, out.State, out.Merge = pin.PullRequest, state, merge
		}
	}
	s.close(after, r, in, change, approval.Credential, out)
	slog.Default().InfoContext(after, "proposals answer", "installation_id", r.InstallationID, "repository_id", r.ID,
		"request", in.Request, "approval", id, "kind", string(change.Kind), "pull", out.PullRequest, "state", out.State,
		"voided", len(voided))
	return out, nil
}

// policyAt reads and decodes the policy at commit at.
func (s *Service) policyAt(ctx context.Context, ref api.RepoRef, at string) ([]byte, policy3.File, error) {
	file, _, err := s.gh.File(ctx, ref, at, policyPath)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, policy3.File{}, requests.ErrNoPolicy
	case err != nil:
		return nil, policy3.File{}, fmt.Errorf("proposals: read the policy: %w", err)
	}
	pol, err := policy3.Decode(file)
	if err != nil {
		return nil, policy3.File{}, fmt.Errorf("%w: %w", requests.ErrNoPolicy, err)
	}
	return file, pol, nil
}

// openRequest reads request number, which must be open.
func (s *Service) openRequest(ctx context.Context, r api.Repo, number int) (records.Request, error) {
	var req records.Request
	switch err := records.Load(ctx, s.store, tenant(r), records.RequestKey(r.FullName, number), &req); {
	case errors.Is(err, records.ErrNoRecord):
		return records.Request{}, requests.ErrNoRequest
	case err != nil:
		return records.Request{}, fmt.Errorf("proposals: read request %d: %w", number, err)
	case req.State != records.StateOpen:
		return records.Request{}, requests.ErrClosedRequest
	}
	return req, nil
}

// approval is the record of in, with the passkey of an answer that loosens the policy: Redeem consumes the step-up
// token for this change.
func (s *Service) approval(ctx context.Context, in Input, c requests.Change) (records.Approval, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return records.Approval{}, err
	}
	a := records.Approval{Kind: string(c.Kind), Request: in.Request, Change: raw, Owner: in.Owner.ID, Login: in.Owner.Login,
		At: s.now().UTC()}
	loosens, err := policy3.Loosens(c.Kind)
	if err != nil || !loosens {
		return a, err
	}
	if in.Redeem == nil {
		return records.Approval{}, ErrNeedsPasskey
	}
	if a.Credential, err = in.Redeem(ctx, changeDigest(c)); err != nil {
		return records.Approval{}, err
	}
	return a, nil
}

// current returns the open proposal whose pull request is open, or nil. A proposal whose pull request merged waits
// for the push that consumes it (M2 task 20) and is passed over. One whose pull request was closed without a merge is
// void, and the requests of its approvals open again.
func (s *Service) current(ctx context.Context, r api.Repo) (*proposal, error) {
	t := tenant(r)
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := records.Load(ctx, s.store, t, records.ProposalIndexKey(r.FullName), &index); err != nil &&
		!errors.Is(err, records.ErrNoRecord) {
		return nil, fmt.Errorf("proposals: read the proposal index: %w", err)
	}
	var open *proposal
	for _, e := range index.Entries {
		p := &proposal{id: e.ID}
		if err := records.Load(ctx, s.store, t, records.ProposalKey(r.FullName, e.ID), &p.rec); err != nil {
			return nil, fmt.Errorf("proposals: read proposal %s: %w", e.ID, err)
		}
		if p.rec.State != records.StateOpen {
			continue
		}
		pull, err := s.gh.Pull(ctx, r.RepoRef, p.rec.PullRequest)
		if err != nil {
			return nil, fmt.Errorf("proposals: read pull request %d: %w", p.rec.PullRequest, err)
		}
		if err := s.loadApprovals(ctx, r, p); err != nil {
			return nil, err
		}
		switch {
		case pull.Merged:
			continue
		case pull.State != "open":
			// The approvals first: while the proposal is in the index, a later answer repeats a cleanup that stopped.
			reopened, err := s.voidRecords(ctx, r, p.approvals)
			if err != nil {
				return nil, err
			}
			if err := records.VoidProposal(ctx, s.store, t, r.FullName, p.id); err != nil {
				return nil, fmt.Errorf("proposals: void proposal %s: %w", p.id, err)
			}
			s.noteReopened(ctx, r, p.rec.PullRequest, reopened, true)
			continue
		case open != nil:
			return nil, fmt.Errorf("proposals: two open proposals, %s and %s", open.id, p.id)
		}
		open = p
	}
	return open, nil
}

func (s *Service) loadApprovals(ctx context.Context, r api.Repo, p *proposal) error {
	for _, id := range p.rec.Approvals {
		a := approved{id: id}
		if err := records.Load(ctx, s.store, tenant(r), records.ApprovalKey(r.FullName, id), &a.rec); err != nil {
			return fmt.Errorf("proposals: read approval %s: %w", id, err)
		}
		if a.rec.State != records.StateOpen {
			continue
		}
		if err := json.Unmarshal(a.rec.Change, &a.change); err != nil {
			return fmt.Errorf("proposals: approval %s holds an unreadable change: %w", id, err)
		}
		p.approvals = append(p.approvals, a)
	}
	return nil
}

// apply writes the changes of the open approvals of cur, in order, into the tip's policy. An approval whose change
// the policy no longer takes (ErrByHand: a module it names is gone, a list the writer cannot place) is voided.
func apply(tipFile []byte, cur *proposal) (file []byte, kept, voided []approved, err error) {
	file = tipFile
	if cur == nil {
		return file, nil, nil, nil
	}
	for _, a := range cur.approvals {
		out, err := requests.Edit(file, a.change, requests.RequestComment(a.change.Request))
		switch {
		case errors.Is(err, requests.ErrByHand):
			voided = append(voided, a)
		case err != nil:
			return nil, nil, nil, fmt.Errorf("proposals: approval %s: %w", a.id, err)
		default:
			file, kept = out, append(kept, a)
		}
	}
	return file, kept, voided, nil
}

// write puts want on the policy branch and returns the pin of the answer that joins: on the open proposal's head, or
// a new proposal with its pull request.
func (s *Service) write(ctx context.Context, r api.Repo, cur *proposal, tip string, want []byte, listed []approved) (*records.Pin, error) {
	digest := policy3.Digest(want)
	if cur != nil {
		head, err := s.moveHead(ctx, r.RepoRef, cur.rec.HeadSHA, tip, want, listed)
		if err != nil {
			return nil, err
		}
		return &records.Pin{Proposal: cur.id, PullRequest: cur.rec.PullRequest, HeadSHA: head, Digest: digest}, nil
	}
	files := []api.FileChange{{Path: policyPath, Content: want}}
	head, err := s.gh.CommitOn(ctx, r.RepoRef, Branch, tip, commitMessage(listed), files, true)
	if err != nil {
		return nil, fmt.Errorf("proposals: write %s: %w", Branch, err)
	}
	pull, err := s.gh.EnsurePull(ctx, r.RepoRef, api.NewPull{Title: pullTitle, Body: pullBody(r.DefaultBranch, listed),
		Head: Branch, Base: r.DefaultBranch})
	if err != nil {
		return nil, fmt.Errorf("proposals: open the policy pull request: %w", err)
	}
	if pull.Draft {
		if err := s.gh.MarkReady(ctx, r.RepoRef, pull.Number); err != nil {
			return nil, fmt.Errorf("proposals: mark pull request %d ready: %w", pull.Number, err)
		}
	}
	return &records.Pin{Proposal: records.NewID(), PullRequest: pull.Number, HeadSHA: head, Digest: digest}, nil
}

// moveHead writes want as one commit on the proposal's head when the default branch's policy did not change since the
// branch's base: a fast-forward, so the branch holds only the App's commits. Otherwise, or when the branch left the
// recorded head, it rewrites the branch as one commit on the tip, with force.
func (s *Service) moveHead(ctx context.Context, ref api.RepoRef, head, tip string, want []byte, listed []approved) (string, error) {
	files := []api.FileChange{{Path: policyPath, Content: want}}
	message := commitMessage(listed)
	cmp, err := s.gh.Compare(ctx, ref, head, tip)
	switch {
	case err == nil && !cmp.Truncated && !slices.Contains(cmp.Paths, policyPath):
		next, err := s.gh.CommitOn(ctx, ref, Branch, head, message, files, false)
		if !errors.Is(err, api.ErrUnprocessable) {
			return next, wrapWrite(err)
		}
	case err != nil && !api.Permanent(err):
		return "", fmt.Errorf("proposals: compare %s with the default branch: %w", Branch, err)
	}
	next, err := s.gh.CommitOn(ctx, ref, Branch, tip, message, files, true)
	return next, wrapWrite(err)
}

func wrapWrite(err error) error {
	if err != nil {
		return fmt.Errorf("proposals: write %s: %w", Branch, err)
	}
	return nil
}

// resync rewrites the proposal after approvals in it were voided and no answer joined: the branch then holds the
// approvals still kept, pinned again through one of them. With none kept, the proposal is void and the branch is the
// tip. It returns the pin to publish, or nil.
func (s *Service) resync(ctx context.Context, r api.Repo, cur *proposal, tip string, file []byte, kept []approved) *records.Pin {
	t := tenant(r)
	head, err := s.moveHead(ctx, r.RepoRef, cur.rec.HeadSHA, tip, file, kept)
	if err != nil {
		logFailure(ctx, r, "rewrite the proposal", err)
		return nil
	}
	if len(kept) == 0 {
		if err := records.VoidProposal(ctx, s.store, t, r.FullName, cur.id); err != nil {
			logFailure(ctx, r, "void the proposal", err)
		}
		return nil
	}
	pin := records.Pin{Proposal: cur.id, PullRequest: cur.rec.PullRequest, HeadSHA: head, Digest: policy3.Digest(file)}
	if err := records.Join(ctx, s.store, t, r.FullName, kept[0].id, cur.id, head, pin.Digest); err != nil {
		logFailure(ctx, r, "pin the proposal", err)
		return nil
	}
	return &pin
}

// voidRecords marks each approval void (records.Void, which takes it out of its proposal and opens its request again)
// and returns the requests that opened again. It stops at the first failure; what is left is done by a later answer,
// which finds the same approvals open.
func (s *Service) voidRecords(ctx context.Context, r api.Repo, approvals []approved) ([]int, error) {
	var reopened []int
	for _, a := range approvals {
		request, opened, err := records.Void(ctx, s.store, tenant(r), r.FullName, a.id)
		if err != nil {
			return nil, fmt.Errorf("proposals: void approval %s: %w", a.id, err)
		}
		if opened {
			reopened = append(reopened, request)
		}
	}
	return reopened, nil
}

// noteReopened posts the note on each request that opened again and reopens its issue. closed says pull was closed
// without a merge; else the default branch's policy changed under it.
func (s *Service) noteReopened(ctx context.Context, r api.Repo, pull int, reopened []int, closed bool) {
	for _, request := range reopened {
		if _, err := s.gh.CreateComment(ctx, r.RepoRef, request, requests.ReopenNote(pull, closed)); err != nil {
			logFailure(ctx, r, "note a reopened request", err)
		}
		if err := s.gh.ReopenIssue(ctx, r.RepoRef, request); err != nil {
			logFailure(ctx, r, "reopen a request", err)
		}
	}
}

// publish describes the pull request, sets archfit/gate on its head when the D13 conditions hold, and merges it
// unless the owner chose to merge later.
func (s *Service) publish(ctx context.Context, r api.Repo, tip string, pin records.Pin, listed []approved, later bool) (state, merge string) {
	if err := s.gh.UpdatePullBody(ctx, r.RepoRef, pin.PullRequest, pullBody(r.DefaultBranch, listed)); err != nil {
		logFailure(ctx, r, "describe the policy pull request", err)
	}
	err := s.check(ctx, r, tip, pin, listed)
	switch {
	case later:
		if err != nil {
			logFailure(ctx, r, "set archfit/gate on the policy pull request", err)
		}
		return requests.PullLater, ""
	case err != nil:
		logFailure(ctx, r, "set archfit/gate on the policy pull request", err)
		return requests.PullRefused, ""
	}
	return s.merge(ctx, r, pin)
}

// merge merges the proposal's pull request at its pinned head (design 9.2, appendix D.2: no bypass actor, so a
// missing or pending check is GitHub's 405). After a refusal it reads the pull request: one that merged anyway (an
// owner, or an earlier attempt) is merged; any other stays open for the sweep.
func (s *Service) merge(ctx context.Context, r api.Repo, pin records.Pin) (state, merge string) {
	s.waitForHead(ctx, r.RepoRef, pin)
	sha, err := s.gh.MergePull(ctx, r.RepoRef, pin.PullRequest, pin.HeadSHA)
	if err == nil {
		return requests.PullMerged, sha
	}
	logFailure(ctx, r, "merge the policy pull request", err)
	if pull, perr := s.gh.Pull(ctx, r.RepoRef, pin.PullRequest); perr == nil && pull.Merged {
		return requests.PullMerged, pull.MergeCommitSHA
	}
	return requests.PullRefused, ""
}

// waitForHead waits, at most headWait, until the pull request shows the head the App wrote.
func (s *Service) waitForHead(ctx context.Context, ref api.RepoRef, pin records.Pin) {
	for range int(headWait / headPoll) {
		pull, err := s.gh.Pull(ctx, ref, pin.PullRequest)
		if err != nil || pull.HeadSHA == pin.HeadSHA || s.sleep(ctx, headPoll) != nil {
			return
		}
	}
}

// close posts the answer on its request issue and closes it.
func (s *Service) close(ctx context.Context, r api.Repo, in Input, c requests.Change, passkey string, out Outcome) {
	note := requests.AnswerNote{Kind: c.Kind, Until: c.Until, Instead: c.Instead, Owner: in.Owner.Login, Passkey: passkey,
		Pull: out.PullRequest, State: out.State}
	if _, err := s.gh.CreateComment(ctx, r.RepoRef, in.Request, note.Text()); err != nil {
		logFailure(ctx, r, "note the answer", err)
	}
	if err := s.gh.CloseIssue(ctx, r.RepoRef, in.Request); err != nil {
		logFailure(ctx, r, "close the request", err)
	}
}

// changeDigest is the digest the passkey step-up of an answer is bound to: the SHA-256 of the change's JSON.
func changeDigest(c requests.Change) string {
	raw, _ := json.Marshal(c) // a struct of strings and ints always encodes
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func tenant(r api.Repo) store.Tenant { return store.Tenant(r.InstallationID) }

func logFailure(ctx context.Context, r api.Repo, step string, err error) {
	slog.Default().WarnContext(ctx, "proposals step failed", "installation_id", r.InstallationID,
		"repository_id", r.ID, "step", step, "error", err.Error())
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// IsProposalPull reports whether pull is the pull request of an open proposal of r. The App sets archfit/gate there
// itself (M2 D13), so an upload for it is stored and sets no verdict: its result is unknown, because the pull request
// changes the policy.
func IsProposalPull(ctx context.Context, s store.Store, r api.Repo, pull int) (bool, error) {
	if pull <= 0 {
		return false, nil
	}
	index := records.Index{Schema: records.SchemaProposalIndex}
	switch err := records.Load(ctx, s, tenant(r), records.ProposalIndexKey(r.FullName), &index); {
	case errors.Is(err, records.ErrNoRecord):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("proposals: read the proposal index: %w", err)
	}
	_, ok := index.Lookup(records.PullEntry(pull))
	return ok, nil
}
