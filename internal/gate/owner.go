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
	// CheckGone.
	Check string
}

// How the approved head gets its verdict.
const (
	CheckRerun   = "rerun"   // the App re-ran the head's run
	CheckPending = "pending" // the head's run has not uploaded yet, or is running: its upload gets the verdict
	CheckGone    = "gone"    // the head's run cannot run again: the check says to run it again
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
// then gets that head its verdict: the App re-runs the head's stored run, whose upload now passes the approval
// (manager, 2026-11-13). A first approval of the default branch's policy and a confirm re-run every open pull request
// that has no verdict.
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
		out.Check = g.checkApproved(after, t.Repo, c.Pull, c.HeadSHA)
	} else {
		g.rerunUnknown(after, t.Repo)
	}
	return out, nil
}

// ownerAfterTimeout bounds what follows an owner's approval: the re-runs.
const ownerAfterTimeout = 25 * time.Second

// checkApproved gets the approved head of pull request pull its verdict. The pull record of its newest run (M2 D6)
// names the run: a completed one is re-run, and its upload passes the approval. A head with no stored run, or a run
// still running, gets its verdict from that run's upload. A run GitHub can no longer re-run leaves the check unknown,
// and the App says so on it, with the way out.
func (g *Gate) checkApproved(ctx context.Context, r api.Repo, pull int, head string) string {
	var rec records.Pull
	err := records.Load(ctx, g.v4.store, store.Tenant(r.InstallationID), records.PullKey(r.FullName, pull), &rec)
	switch {
	case errors.Is(err, records.ErrNoRecord) || err == nil && rec.HeadSHA != head:
		return CheckPending
	case err != nil:
		logOwner(ctx, r, "read the pull record", err)
		return CheckPending
	}
	res, err := sweep.Rerun(ctx, g.actions, r.RepoRef, feedback.Receipt{RunID: rec.RunID, RunAttempt: rec.RunAttempt})
	switch {
	case err != nil:
		logOwner(ctx, r, "re-run the approved head", err)
		return CheckPending
	case res == sweep.RerunRequested:
		return CheckRerun
	case res == sweep.RerunInFlight:
		return CheckPending
	}
	err = g.v4.publisher.Publish4(ctx, feedback.Publication4{
		Target:     inputs.Target{Repo: r, PullRequest: pull, HeadSHA: head, BaseRef: r.DefaultBranch},
		RunID:      rec.RunID,
		RunAttempt: rec.RunAttempt,
		Digest:     rec.PayloadDigest,
		Result: result.Result{Status: result.StatusUnknown, Summary: summaryUnknown, Unknown: []result.Unknown{{
			Cause: "tool",
			What:  "this head is approved, and its run cannot run again",
			Why:   "only a run of the approved head gives it a verdict",
			Next:  result.Next{Kind: "fix", Text: "run the Archfit check again: re-run the workflow, or push a commit and approve it again"},
		}}},
	})
	if err != nil {
		logOwner(ctx, r, "say the approved head's run is gone", err)
	}
	return CheckGone
}

// rerunUnknown re-runs the newest run of every open pull request whose stored status is unknown: a first approval or a
// confirm gives them a verdict again. A run that cannot run again keeps its record, which names the way out.
func (g *Gate) rerunUnknown(ctx context.Context, r api.Repo) {
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
		if _, err := sweep.Rerun(ctx, g.actions, r.RepoRef, feedback.Receipt{RunID: rec.RunID, RunAttempt: rec.RunAttempt}); err != nil {
			logOwner(ctx, r, "re-run an open pull request", err)
		}
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
