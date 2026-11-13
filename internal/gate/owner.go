package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// OwnerActions are the owner actions outside requests (design 7.1, 9.2, 10.1; M2 task 23), each made with a passkey:
// the first approval of an onboarding pull request or of the policy already on the default branch, the approval of a
// changed workflow file, and the confirm or revert of a policy changed outside the flow. *Gate implements it.
type OwnerActions interface {
	// OwnerDigest is the change digest of action a now: the owner's passkey step-up is bound to it, with the action's
	// kind as its action.
	OwnerDigest(ctx context.Context, t Target, a OwnerAction) (string, error)
	// OwnerAct makes in. An error means nothing was recorded; a token Redeem used stays used.
	OwnerAct(ctx context.Context, t Target, in OwnerInput) (OwnerOutcome, error)
}

// The owner action kinds.
const (
	OwnerFirst    = records.KindFirst
	OwnerWorkflow = records.KindWorkflow
	OwnerConfirm  = records.KindConfirm
	OwnerRevert   = records.KindRevert
)

type (
	// OwnerAction names an action: Kind is one of the owner action kinds; Pull the pull request of a first approval of
	// an onboarding pull request and of a workflow approval, else 0.
	OwnerAction = approvals.Action
	// OwnerUser is the signed-in user who acts, as control authorized them.
	OwnerUser = approvals.Owner
)

// OwnerInput is one owner action as the owner made it.
type OwnerInput struct {
	Action OwnerAction
	User   OwnerUser
	Digest string // the digest the owner's step-up is bound to: what the owner saw (OwnerDigest)
	Later  bool   // revert: the policy pull request stays open
	Redeem func(ctx context.Context, digest string) (passkeyID string, err error)
}

// OwnerOutcome is what an owner action did.
type OwnerOutcome struct {
	Approval    string
	PullRequest int    // revert: the policy pull request
	State       string // revert: AnswerMerged, AnswerLater or AnswerRefused
	Merge       string // revert: the merge commit, when merged
	Issue       int    // the issue that records a first approval of the default branch's policy, or a confirm ended
	// Check says how the approved head of a first or workflow approval gets its verdict: CheckRerun, CheckPending or
	// CheckBlocked.
	Check string
}

// How the approved head gets its verdict.
const (
	CheckRerun   = "rerun"   // the App re-ran the head's run
	CheckPending = "pending" // the head's run has not uploaded yet, or is running: its upload gets the verdict
	CheckBlocked = "blocked" // the App could not re-run it: the check says why and what to do
)

// The refusals of an owner action, besides the passkey's, which Redeem returns as they are.
var (
	ErrOwnerNotAdmin     = approvals.ErrNotAdmin
	ErrOwnerNotOwner     = approvals.ErrNotOwner
	ErrOwnerNotDue       = approvals.ErrNotDue
	ErrOwnerNotPossible  = approvals.ErrNotApprovable
	ErrOwnerMoved        = approvals.ErrMoved
	ErrOwnerNeedsPasskey = approvals.ErrNeedsPasskey
	ErrNotOutsideFlow    = proposals.ErrNotOutside
	ErrNothingToRevert   = proposals.ErrNothingToRevert
	ErrRevertMoved       = proposals.ErrMoved
	ErrRevertNotOwner    = proposals.ErrNotOwner
	ErrOutsideFlow       = proposals.ErrOutsideFlow
)

// OwnerDigest is the change digest of action a of t's repository.
func (g *Gate) OwnerDigest(ctx context.Context, t Target, a OwnerAction) (string, error) {
	if err := g.admitted(ctx, t); err != nil {
		return "", err
	}
	if a.Kind == OwnerRevert {
		return g.proposals.RevertDigest(ctx, t.Repo)
	}
	c, err := g.approvals.Bind(ctx, t.Repo, a)
	if err != nil {
		return "", err
	}
	return c.Digest(), nil
}

// OwnerAct makes an owner action on t's repository. A revert goes through the App's policy pull request
// (gate/proposals); the others are recorded at once (gate/approvals). A first or a workflow approval of a pull request
// then gets that head its verdict: the App re-runs the head's stored run through the re-run guard, and the upload passes
// the approval (manager, 2026-11-13). A first approval of the default branch's policy and a confirm do the same for
// every open pull request whose stored status is unknown.
func (g *Gate) OwnerAct(ctx context.Context, t Target, in OwnerInput) (OwnerOutcome, error) {
	if err := g.admitted(ctx, t); err != nil {
		return OwnerOutcome{}, err
	}
	if in.Action.Kind == OwnerRevert {
		out, err := g.proposals.Revert(ctx, t.Repo, proposals.RevertInput{Owner: proposals.Owner{ID: in.User.ID, Login: in.User.Login},
			Digest: in.Digest, Later: in.Later, Redeem: in.Redeem})
		return OwnerOutcome{Approval: out.Approval, PullRequest: out.PullRequest, State: out.State, Merge: out.Merge}, err
	}
	done, err := g.approvals.Act(ctx, t.Repo, approvals.Input{Action: in.Action, Owner: in.User, Digest: in.Digest,
		Redeem: in.Redeem})
	if err != nil {
		return OwnerOutcome{}, err
	}
	out := OwnerOutcome{Approval: done.Approval, Issue: done.Issue}
	// The approval stands: its verdict follows on a context the caller cannot cancel.
	after, cancel := context.WithTimeout(context.WithoutCancel(ctx), ownerAfterTimeout)
	defer cancel()
	if c := done.Change; c.Pull > 0 {
		out.Check = g.recheckPull(after, t.Repo, c.Pull, c.HeadSHA)
	} else {
		g.recheckUnknown(after, t.Repo)
	}
	return out, nil
}

// ownerAfterTimeout bounds what follows an owner's approval: the re-runs.
const ownerAfterTimeout = 25 * time.Second

// recheckPull gets pull request pull its verdict after an owner action, on head: the approved head of a first or
// workflow approval, or "" for the head its pull record names. The re-run guard (sweep.Rerun, design 10.5) re-runs the
// newest stored run of the head when every condition holds; a run of the head still running uploads by itself, and so
// does the first run of a head with no stored run. Otherwise the check stays unknown and the App says on it which
// condition kept the re-run back and what to do (manager, 2026-11-13). The run's workflow is taken as reading the
// policy from the target tip: it is the workflow of the default branch or the one an owner approved for this head, and
// the upload of the re-run is decided by every rule again.
func (g *Gate) recheckPull(ctx context.Context, r api.Repo, pull int, head string) string {
	var rec records.Pull
	err := records.Load(ctx, g.v4.store, store.Tenant(r.InstallationID), records.PullKey(r.FullName, pull), &rec)
	switch {
	case errors.Is(err, records.ErrNoRecord):
		return CheckPending
	case err != nil:
		logOwner(ctx, r, "read the pull record", err)
		return CheckBlocked // nothing can say why on the check: the record is unreadable
	}
	bound := head != ""
	if !bound {
		head = rec.HeadSHA
	}
	if rec.HeadSHA != head {
		return CheckPending // the head's own run uploads
	}
	pl, err := g.pulls.Pull(ctx, r.RepoRef, pull)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return CheckPending
	case err != nil:
		return g.cannotRecheck(ctx, r, pull, rec, bound, "", err)
	case pl.State != "open" || pl.HeadSHA != head:
		return CheckPending // closed, or a newer head whose run decides
	}
	tip, err := g.repos.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return g.cannotRecheck(ctx, r, pull, rec, bound, "", err)
	}
	in := sweep.RerunInput{Receipt: feedback.Receipt{RunID: rec.RunID, RunAttempt: rec.RunAttempt}, Head: head,
		EventBase: pl.BaseSHA, Tip: tip, ReadsTip: true}
	res, unmet, err := sweep.Rerun(ctx, g.actions, r.RepoRef, in, g.now())
	switch {
	case err != nil:
		return g.cannotRecheck(ctx, r, pull, rec, bound, "", err)
	case res == sweep.RerunRequested:
		return CheckRerun
	case res == sweep.RerunInFlight:
		return CheckPending
	}
	return g.cannotRecheck(ctx, r, pull, rec, bound, unmet, nil)
}

// cannotRecheck says on the check of rec's head that the App could not re-run it after an owner action: the condition of
// design 10.5 that is not met, or the failure, and what to do. bound says the action approved this head, so a new
// head needs a new approval.
func (g *Gate) cannotRecheck(ctx context.Context, r api.Repo, pull int, rec records.Pull, bound bool, unmet sweep.Unmet, cause error) string {
	if cause != nil {
		logOwner(ctx, r, "re-run after an owner action", cause)
	}
	what, next := recheckWords(unmet, bound)
	err := g.v4.publisher.Publish4(ctx, feedback.Publication4{
		Target:     inputs.Target{Repo: r, PullRequest: pull, HeadSHA: rec.HeadSHA, BaseRef: r.DefaultBranch},
		RunID:      rec.RunID,
		RunAttempt: rec.RunAttempt,
		Digest:     rec.PayloadDigest,
		Result: result.Result{Status: result.StatusUnknown, Summary: summaryUnknown, Unknown: []result.Unknown{{
			Cause: "tool", What: what, Why: "only a new run of this head gives it a verdict",
			Next: result.Next{Kind: "fix", Text: next},
		}}},
	})
	if err != nil {
		logOwner(ctx, r, "say why the re-run did not start", err)
	}
	return CheckBlocked
}

// recheckWords is the record of a re-run that did not start: what happened, with the unmet condition's code, and what
// to do. bound says a new head needs a new approval.
func recheckWords(unmet sweep.Unmet, bound bool) (what, next string) {
	approved, newHead := "the policy is approved", "push a commit to run the check again"
	if bound {
		approved, newHead = "this head is approved", "push a commit; an owner then approves the new head"
	}
	rerun := "re-run the Archfit workflow of this head"
	switch unmet {
	case sweep.UnmetNoRun:
		return approved + ", and its run is gone (" + string(unmet) + ")", newHead
	case sweep.UnmetNotLatest:
		return approved + ", and its stored run is not the latest run of the head (" + string(unmet) + ")", rerun
	case sweep.UnmetTooOld:
		return approved + ", and its run is too old to run again (" + string(unmet) + ")", newHead
	case sweep.UnmetBaseChanged:
		update := "update the branch"
		if bound {
			update += "; an owner then approves the new head"
		}
		return approved + ", and the default branch changed in more than the policy since its run (" + string(unmet) + ")", update
	case sweep.UnmetPolicyNotFromTip:
		return approved + ", and its run did not read the policy from the default branch (" + string(unmet) + ")", newHead
	}
	return approved + ", and the App could not start its run again", rerun
}

// recheckUnknown runs recheckPull for every open pull request whose stored status is unknown: a first approval of the
// default branch's policy or a confirm gives them a verdict again.
func (g *Gate) recheckUnknown(ctx context.Context, r api.Repo) {
	t, name := store.Tenant(r.InstallationID), r.FullName
	index := records.Index{Schema: records.SchemaPullIndex}
	if err := records.Load(ctx, g.v4.store, t, records.PullIndexKey(name), &index); err != nil {
		if !errors.Is(err, records.ErrNoRecord) {
			logOwner(ctx, r, "read the pull index", err)
		}
		return
	}
	for _, e := range index.Entries {
		pull, err := strconv.Atoi(e.Key)
		if err != nil {
			continue
		}
		var rec records.Pull
		if err := records.Load(ctx, g.v4.store, t, records.PullKey(name, pull), &rec); err != nil || rec.Status != string(result.StatusUnknown) {
			continue
		}
		g.recheckPull(ctx, r, pull, "")
	}
}

// workflowPushed judges a default-branch push for the workflow approval it uses (M2 task 23, gate/approvals): the merge
// of an approved workflow change consumes its approval, a merge of another file voids it. A failed read or write is
// returned, so the delivery is retried.
func (g *Gate) workflowPushed(ctx context.Context, e webhook.Event) error {
	t := eventTarget(e)
	out, err := g.approvals.WorkflowPushed(ctx, approvals.Push{Repo: t.Repo, Before: e.Before, After: e.HeadSHA,
		ChangedPaths: e.ChangedPaths})
	if err != nil {
		return fmt.Errorf("gate: judge the workflow push: %w", err)
	}
	if out.Judged != approvals.JudgedNone || out.Voided != "" {
		slog.Default().InfoContext(ctx, "gate workflow push", "delivery_id", e.DeliveryID, "installation_id", e.InstallationID,
			"head_sha", e.HeadSHA, "judged", out.Judged, "proposal", out.Proposal, "voided", out.Voided)
	}
	return nil
}

func logOwner(ctx context.Context, r api.Repo, step string, err error) {
	slog.Default().WarnContext(ctx, "gate owner action step failed", "installation_id", r.InstallationID,
		"repository_id", r.ID, "step", step, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
}
