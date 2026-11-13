package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// pusherTypeUser is the pusher_type of a push made by a user account.
const pusherTypeUser = "user"

// Reactions, as logged.
const (
	reactionNone         = "none"
	reactionPending      = "pending"
	reactionForkRefused  = "fork_refused"
	reactionRerun        = "rerun"
	reactionRepoSweep    = "repo_sweep"
	reactionSharedHead   = "shared_head" // a pull request into another base: the pull requests into the default branch on its head are checked
	reactionRecheck      = "recheck"     // a default-branch push moved the target: open pull requests are checked for a module cycle with it
	reactionNotOnboarded = "not_onboarded"
	reactionNotAdmitted  = "not_admitted" // no operator approved the installation: nothing is read or written
	reactionOtherBase    = "other_base"   // a pull request into a branch other than the default branch: nothing is read or written
	// reactionApprovalWithdrawn is owner_approval_missing published over a receipt that asked for no action.
	reactionApprovalWithdrawn = "approval_withdrawn"
	reactionDecisionStale     = "decision_stale"   // pending decision requests went stale with a new head or a closed pull request
	reactionDecisionIgnored   = "decision_ignored" // a request on a pull request the gate does not decide: nothing is recorded or written
	reactionGroupChecked      = "group_checked"    // merge groups were re-read inside their leases; a blocked one was corrected
	// groupMerged is the destroyed reason of a merge group that landed: its result stands.
	groupMerged = "merged"
)

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// owner_approval_missing on a review change that withdraws the approval a receipt relied on, re-run on a
// review change when the last receipt was owner_approval_missing, a pusher record (attribution only, never an
// approval) and a re-evaluation of the pull request on a pushed head, and a one-repository sweep when a
// default-branch push changes a protected path.
func (g *Gate) HandleEvent(ctx context.Context, e webhook.Event) error {
	reaction, err := g.react(ctx, e)
	slog.Default().InfoContext(ctx, "gate event",
		"kind", string(e.Kind), "delivery_id", e.DeliveryID, "installation_id", e.InstallationID,
		"pr", e.PullRequest, "head_sha", e.HeadSHA, "reaction", reaction, "failed", err != nil)
	return err
}

func (g *Gate) react(ctx context.Context, e webhook.Event) (string, error) {
	if e.Repo != "" {
		// An unreadable admission is an error, so the delivery is retried rather than dropped.
		admitted, err := g.admissions.Admitted(ctx, e.InstallationID, inputs.Owner(e.Repo))
		if err != nil {
			return reactionNotAdmitted, fmt.Errorf("gate: read admission: %w", err)
		}
		if !admitted {
			return reactionNotAdmitted, nil
		}
	}
	switch e.Kind {
	case webhook.PullOpened, webhook.PullSynchronized:
		if otherBase(e.DefaultBranch, e.BaseRef) {
			return g.sharedHeadOpened(ctx, e)
		}
		reaction, err := g.newHead(ctx, eventTarget(e))
		if e.Kind == webhook.PullSynchronized {
			err = errors.Join(err, g.requests.HeadMoved(ctx, eventTarget(e), g.now()))
		}
		return reaction, err
	case webhook.PullClosed:
		if err := g.requests.Closed(ctx, eventTarget(e), g.now()); err != nil {
			return reactionDecisionStale, fmt.Errorf("gate: close decision requests: %w", err)
		}
		return reactionDecisionStale, g.pullClosed(ctx, e)
	case webhook.PullCommented:
		if asks.IsRequest(e.Body) {
			return g.requestCommented(ctx, e)
		}
		return g.commented(ctx, e)
	case webhook.IssueOpened:
		return g.requestFiled(ctx, e)
	case webhook.ReviewChanged:
		if otherBase(e.DefaultBranch, e.BaseRef) {
			return reactionOtherBase, nil
		}
		return g.refreshOwnerDecision(ctx, eventTarget(e))
	case webhook.MergeGroupRequested:
		if otherBase(e.DefaultBranch, e.BaseRef) || e.DefaultBranch == "" {
			return reactionOtherBase, nil
		}
		return g.groupRequested(ctx, groupTarget(e))
	case webhook.MergeGroupDestroyed:
		if otherBase(e.DefaultBranch, e.BaseRef) || e.DefaultBranch == "" || e.GroupReason == groupMerged {
			return reactionNone, nil
		}
		return reactionGroupChecked, g.groupCheck(ctx, groupTarget(e))
	case webhook.BranchPush, webhook.DefaultBranchPush:
		return g.branchPushed(ctx, e)
	}
	return reactionNone, nil
}

// sharedHeadOpened reacts to a pull request into another base. Nothing is decided for it, but its head may be the head
// of a pull request into the default branch, whose checks it would show. For each such pull request the shared-head
// decision runs inside the head's publication lease (sweep.SharedHeadPublication): it withdraws a success, and
// refuses a pending check, so an upload that listed the open pull requests before this one was visible cannot write a
// success after it (the upload lists again inside the same lease). Only failures of these units are returned, so the
// queue retries the delivery; an unrelated pull request of the repository is never read. A base changed after opening
// sends no event (the webhook does not map `edited`), so the hourly sweep (rule 5d) is the net for it.
func (g *Gate) sharedHeadOpened(ctx context.Context, e webhook.Event) (string, error) {
	repo := eventTarget(e).Repo
	open, err := g.pulls.OpenPulls(ctx, repo.RepoRef)
	if err != nil {
		return reactionSharedHead, fmt.Errorf("gate: list open pull requests: %w", err)
	}
	var errs error
	for _, pl := range open {
		if pl.Number == e.PullRequest || pl.HeadSHA != e.HeadSHA || pl.BaseRef != e.DefaultBranch {
			continue
		}
		t := inputs.Target{Repo: repo, PullRequest: pl.Number, HeadSHA: pl.HeadSHA, BaseRef: pl.BaseRef, Fork: pl.HeadRepo != repo.FullName}
		err := g.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
			return sweep.SharedHeadPublication(ctx, g.pulls, g.resolver, g.now(), t, current)
		})
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("gate: shared head of pull request %d: %w", pl.Number, err))
		}
	}
	owned := slices.ContainsFunc(open, func(pl api.Pull) bool { return pl.HeadSHA == e.HeadSHA && pl.BaseRef == e.DefaultBranch })
	if !owned {
		errs = errors.Join(errs, g.sharedGroupHead(ctx, repo, e.HeadSHA))
	}
	return reactionSharedHead, errs
}

// sharedGroupHead withdraws a success on head, as sharedHeadOpened does for a pull request into the default branch,
// when no open pull request into the default branch has head: the check there is a merge group's, queued or already
// merged (the default branch then fast-forwards to it, and a pull request from it into another base is routine). It
// is decided as a merge group target inside the head's lease, so a commit the queue no longer lists still gets
// shared_head over its success. Without the Merge queues permission no group result exists, so nothing is withdrawn.
func (g *Gate) sharedGroupHead(ctx context.Context, repo api.Repo, head string) error {
	if repo.DefaultBranch == "" {
		return nil
	}
	t := inputs.Target{Repo: repo, HeadSHA: head, BaseRef: repo.DefaultBranch, MergeGroup: true}
	err := g.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		return sweep.SharedHeadPublication(ctx, g.pulls, g.resolver, g.now(), t, current)
	})
	if errors.Is(err, api.ErrQueueForbidden) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("gate: shared head of merge group %s: %w", head, err)
	}
	return nil
}

// branchPushed records who moved the branch to e.HeadSHA, then re-evaluates the pull request on that head as a review
// event does. The record approves nothing: a local coding agent pushes with its owner's credentials. A default-branch
// push is judged for the policy it brings (policyPushed, M2 task 20); one that changes a protected path also runs the
// repository sweep.
func (g *Gate) branchPushed(ctx context.Context, e webhook.Event) (string, error) {
	t := eventTarget(e)
	if err := g.recordPusher(ctx, t, e); err != nil {
		return reactionNone, err
	}
	reaction, err := g.pullOnHead(ctx, t, e.Branch, e.DefaultBranch)
	if err != nil || e.Kind == webhook.BranchPush {
		return reaction, err
	}
	// Judged before the sweep reads the new policy: a consume or "outside the flow" is recorded first.
	if err := g.policyPushed(ctx, e); err != nil {
		return reaction, err
	}
	if !protectedPush(e.ChangedPaths) {
		// A rate limit is an error that wraps api.ErrRateLimited: the queue retries the delivery.
		if _, err := g.sweeper.Recheck(ctx, t.Repo, e.HeadSHA, g.now()); err != nil {
			return reactionRecheck, fmt.Errorf("gate: recheck: %w", err)
		}
		return reactionRecheck, nil
	}
	// A rate limit is an error that wraps api.ErrRateLimited: the queue retries the delivery.
	if _, err := g.sweeper.RunRepo(ctx, t.Repo, g.now()); err != nil {
		return reactionRepoSweep, fmt.Errorf("gate: repository sweep: %w", err)
	}
	return reactionRepoSweep, nil
}

// recordPusher records e.Pusher as a pusher of the head, for attribution. A push whose pusher_type is present and not
// "user" (a deploy key) records nothing: GitHub does not say whose login sender carries then. An absent pusher_type
// still trusts sender.
func (g *Gate) recordPusher(ctx context.Context, t inputs.Target, e webhook.Event) error {
	if e.PusherType != "" && e.PusherType != pusherTypeUser {
		slog.Default().InfoContext(ctx, "gate pusher not recorded",
			"delivery_id", e.DeliveryID, "installation_id", e.InstallationID, "head_sha", e.HeadSHA)
		return nil
	}
	if err := g.resolver.RecordPusher(ctx, t, e.Pusher); err != nil {
		return fmt.Errorf("gate: record pusher: %w", err)
	}
	return nil
}

// pullOnHead re-evaluates the newest pull request whose head is branch, when that head is the commit just pushed.
// A pull request from a fork never has a head in this repository, so it is never found here.
func (g *Gate) pullOnHead(ctx context.Context, t inputs.Target, branch, defaultBranch string) (string, error) {
	pr, err := g.pulls.PullByHead(ctx, t.Repo.RepoRef, branch)
	if errors.Is(err, api.ErrNotFound) {
		return reactionNone, nil
	}
	if err != nil {
		return reactionNone, fmt.Errorf("gate: pull request by head: %w", err)
	}
	if pr.State != "open" || pr.HeadSHA != t.HeadSHA {
		return reactionNone, nil
	}
	if otherBase(defaultBranch, pr.BaseRef) {
		return reactionOtherBase, nil
	}
	t.PullRequest, t.BaseRef = pr.Number, pr.BaseRef
	return g.approvalChanged(ctx, t)
}

// otherBase reports a pull request into a branch other than the default branch. The App decides only against the
// default branch (the upload does not accept another base), so nothing is read or written for such a pull request. An
// event whose payload names no default branch is not skipped: no event ever publishes a success.
func otherBase(defaultBranch, base string) bool {
	return defaultBranch != "" && base != defaultBranch
}

// eventTarget builds the target from the signed payload. ID stays zero: pull request targets do not need it, and
// that saves one API call. The default branch comes from the payload: the repository sweep a push starts decides
// only the pull requests into it.
func eventTarget(e webhook.Event) inputs.Target {
	return inputs.Target{
		Repo: api.Repo{
			RepoRef:       api.RepoRef{InstallationID: e.InstallationID, FullName: e.Repo},
			Private:       e.RepoPrivate,
			DefaultBranch: e.DefaultBranch, // the repository sweep a push starts decides only pulls into it
		},
		PullRequest: e.PullRequest,
		HeadSHA:     e.HeadSHA,
		BaseRef:     e.BaseRef,
		Fork:        e.HeadRepo != e.Repo, // a deleted fork has HeadRepo "" and still reads as a fork
	}
}

// groupTarget is the merge group commit of a merge_group event, in the default branch's queue. Its members are never
// read from the payload: gate/inputs reads them from the queue.
func groupTarget(e webhook.Event) inputs.Target {
	t := eventTarget(e)
	t.PullRequest, t.BaseRef, t.Fork, t.MergeGroup = 0, e.DefaultBranch, false, true
	return t
}

// groupRequested creates the pending required check on a new merge group commit, so that a group whose upload never
// arrives waits for GitHub's status check timeout and fails, never passes. Only the protected settings are read.
func (g *Gate) groupRequested(ctx context.Context, t inputs.Target) (string, error) {
	settingsOnly := t
	settingsOnly.MergeGroup = false
	trusted, err := g.resolver.Resolve(ctx, settingsOnly, g.now())
	if err != nil {
		return reactionNone, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	switch {
	case !trusted.SettingsFound:
		return reactionNotOnboarded, nil
	case trusted.Settings.Gate != settings.GateRequired:
		return reactionNone, nil
	}
	if err := g.publisher.Pending(ctx, t); err != nil {
		return reactionPending, fmt.Errorf("gate: pending check: %w", err)
	}
	return reactionPending, nil
}

// groupCheck re-reads the merge group t inside its publication lease and corrects a pending or decided required check
// that its inputs now block (sweep.GroupWithdrawal): a destroyed or rebuilt group, a changed member, or a member whose
// owner approval is gone. It never writes a success.
func (g *Gate) groupCheck(ctx context.Context, t inputs.Target) error {
	return g.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		if !current.Exists {
			return nil, nil
		}
		trusted, err := g.resolver.Resolve(ctx, t, g.now())
		if err != nil {
			return nil, fmt.Errorf("gate: resolve merge group: %w", err)
		}
		return sweep.GroupWithdrawal(t, current, trusted), nil
	})
}

// groupsOf re-checks every merge group of the default branch's queue that holds the pull request t: its own entry's
// group and every group behind it. A review of a queued pull request can withdraw the approval such a group relied on.
// A pull request that is not queued reads the queue only.
func (g *Gate) groupsOf(ctx context.Context, t inputs.Target) error {
	if t.Repo.DefaultBranch == "" || t.PullRequest == 0 {
		return nil
	}
	entries, err := g.queues.MergeQueue(ctx, t.Repo.RepoRef, t.Repo.DefaultBranch)
	if errors.Is(err, api.ErrQueueForbidden) {
		// Without the Merge queues permission no merge-group upload is ever accepted (ingest reads the same queue), so
		// no group result exists to withdraw. The review itself is handled; it is not retried for this.
		return nil
	}
	if err != nil {
		return fmt.Errorf("gate: merge queue: %w", err)
	}
	at := slices.IndexFunc(entries, func(e api.QueueEntry) bool { return e.PullRequest == t.PullRequest })
	if at < 0 {
		return nil
	}
	var errs error
	for _, e := range entries[at:] {
		if e.HeadSHA == "" {
			continue // not built yet: no group commit, no check
		}
		gt := inputs.Target{Repo: t.Repo, HeadSHA: e.HeadSHA, BaseRef: t.Repo.DefaultBranch, MergeGroup: true}
		if err := g.groupCheck(ctx, gt); err != nil {
			errs = errors.Join(errs, fmt.Errorf("gate: merge group of pull request %d: %w", e.PullRequest, err))
		}
	}
	return errs
}

func (g *Gate) pinProtectedRef(ctx context.Context, t inputs.Target) (inputs.Target, error) {
	if t.PullRequest == 0 || t.BaseRef == "" || t.BaseSHA != "" {
		return t, nil
	}
	sha, err := g.repos.BranchHead(ctx, t.Repo.RepoRef, t.BaseRef)
	if errors.Is(err, api.ErrNotFound) {
		return t, nil // Resolve will fail closed on the missing protected settings/ref.
	}
	if err != nil {
		return inputs.Target{}, fmt.Errorf("gate: protected branch head: %w", err)
	}
	if sha == "" {
		return inputs.Target{}, errors.New("gate: protected branch has no head commit")
	}
	t.BaseSHA = sha
	return t, nil
}

func (g *Gate) checkProtectedRef(ctx context.Context, t inputs.Target) error {
	if t.PullRequest == 0 || t.BaseRef == "" || t.BaseSHA == "" {
		return nil
	}
	sha, err := g.repos.BranchHead(ctx, t.Repo.RepoRef, t.BaseRef)
	if err != nil {
		return fmt.Errorf("gate: protected branch head: %w", err)
	}
	if sha != t.BaseSHA {
		return fmt.Errorf("gate: protected branch moved: %w", feedback.ErrStaleInputs)
	}
	return nil
}

func (g *Gate) newHead(ctx context.Context, t inputs.Target) (string, error) {
	var err error
	t, err = g.pinProtectedRef(ctx, t)
	if err != nil {
		return reactionNone, fmt.Errorf("gate: read protected ref head: %w", err)
	}
	trusted, err := g.resolver.Resolve(ctx, t, g.now())
	if err != nil {
		return reactionNone, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	switch {
	case !trusted.SettingsFound:
		return reactionNotOnboarded, nil
	case t.Fork:
		// The zero receipt: no workflow run reported for this head, so any later upload replaces it.
		if err := g.publisher.Publish(ctx, sweep.Without(verdict.ReasonForkUnsupported, t, trusted, feedback.Receipt{})); err != nil {
			return reactionForkRefused, fmt.Errorf("gate: publish fork refusal: %w", err)
		}
		return reactionForkRefused, nil
	}
	if len(trusted.ProtectedPaths) > 0 {
		if err := g.publisher.DecisionBanner(ctx, t, trusted); err != nil {
			return reactionNone, fmt.Errorf("gate: publish owner decision banner: %w", err)
		}
		if trusted.Approval != inputs.ApprovalGranted {
			g.requestReviewers(ctx, t)
		}
	}
	switch trusted.Settings.Gate {
	case settings.GateRequired:
		if err := g.publisher.Pending(ctx, t); err != nil {
			return reactionPending, fmt.Errorf("gate: pending check: %w", err)
		}
		return reactionPending, nil
	}
	return reactionNone, nil
}

// requestReviewers best-effort requests eligible configured principals on a new protected-path head. It never changes
// authority or event success; the stored per-head/protected-settings-snapshot state prevents repeated requests.
func (g *Gate) requestReviewers(ctx context.Context, t inputs.Target) {
	var pull api.Pull
	var err error
	if summary, ok := g.pulls.(interface {
		PullSummary(context.Context, api.RepoRef, int) (api.Pull, error)
	}); ok {
		pull, err = summary.PullSummary(ctx, t.Repo.RepoRef, t.PullRequest)
	} else {
		pull, err = g.pulls.Pull(ctx, t.Repo.RepoRef, t.PullRequest)
	}
	if err != nil {
		slog.Default().WarnContext(ctx, "gate reviewer request unavailable", "installation_id", t.Repo.InstallationID,
			"repository_id", t.Repo.ID, "pr", t.PullRequest, "outcome", "pull_read_failed")
		return
	}
	if pull.HeadSHA != t.HeadSHA || pull.State != "open" {
		return // a late webhook must not request reviewers for a moved or closed candidate
	}
	if g.resolver.RequestEligibleReviewers(ctx, t, pull.Author, g.now()) {
		slog.Default().WarnContext(ctx, "gate reviewer request unavailable", "installation_id", t.Repo.InstallationID,
			"repository_id", t.Repo.ID, "pr", t.PullRequest, "head_sha", t.HeadSHA, "outcome", "notification_problem")
	}
}

// approvalChanged reacts to an event that may withdraw or grant an owner approval: a review, an approval recorded in
// the App (Reevaluate), or a push of t's head. It reads the gate check, decides and writes inside the head's
// publication lease (Publisher.Reconcile), so no other publication for the head lands in between. A receipt that asks for no action
// while the approval it needs is gone becomes owner_approval_missing at once, as sweep rule 5a does it. After
// owner_approval_missing, an event once no approval is missing re-runs the workflow; the check stays as it is until
// the new upload, so nothing here can turn it green.
func (g *Gate) approvalChanged(ctx context.Context, t inputs.Target) (string, error) {
	reaction := reactionNone
	var err error
	t, err = g.pinProtectedRef(ctx, t)
	if err != nil {
		return reaction, fmt.Errorf("gate: read protected ref head: %w", err)
	}
	err = g.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		if err := g.checkProtectedRef(ctx, t); err != nil {
			return nil, err
		}
		trusted, err := g.resolver.Resolve(ctx, t, g.now())
		if err != nil {
			return nil, fmt.Errorf("gate: resolve inputs: %w", err)
		}
		if t.PullRequest != 0 && trusted.SettingsFound && trusted.Settings.Gate != settings.GateRequired {
			current, err = g.publisher.AdvisoryState(ctx, t)
			if err != nil {
				return nil, fmt.Errorf("gate: read advisory state: %w", err)
			}
		}
		rc := current.Receipt
		if !sweep.Decided(rc) && rc.Reason != verdict.ReasonOwnerApprovalMissing {
			return nil, nil // the check already asks for action, or there is none yet
		}
		if pub := sweep.Withdrawal(t, current, trusted); pub != nil {
			reaction = reactionApprovalWithdrawn
			return pub, nil
		}
		if rc.Reason != verdict.ReasonOwnerApprovalMissing || verdict.Unapproved(trusted) {
			return nil, nil // no re-run while an owner approval is still missing
		}
		// The sweep owns the guard and its four conditions (design 10.5). A run that cannot be re-run is skipped, not an
		// error: the check keeps owner_approval_missing until a new upload, as the sweep's rule 7b leaves it.
		in, err := g.rerunInput(ctx, t, rc)
		if err != nil {
			return nil, err
		}
		res, _, err := sweep.Rerun(ctx, g.actions, t.Repo.RepoRef, in, g.now())
		if err != nil {
			return nil, fmt.Errorf("gate: re-run: %w", err)
		}
		if res == sweep.RerunRequested {
			reaction = reactionRerun
		}
		return nil, nil
	})
	if err != nil {
		return reaction, fmt.Errorf("gate: approval: %w", err)
	}
	return reaction, nil
}

// rerunInput is what the re-run guard judges an Archfit 3 receipt rc of t on: the event's base is the base the
// receipt's run measured, else the pull request's base; the Archfit 3 Action reads the protected inputs from the target
// tip (design D.2: run.sh input_tip). A base or tip that cannot be read stays unknown, so no re-run is requested; only a
// rate limit is returned.
func (g *Gate) rerunInput(ctx context.Context, t inputs.Target, rc feedback.Receipt) (sweep.RerunInput, error) {
	in := sweep.RerunInput{Receipt: rc, Head: t.HeadSHA, EventBase: rc.BaseSHA, ReadsTip: true}
	if rc.RunID == 0 {
		return in, nil
	}
	if in.EventBase == "" && t.PullRequest != 0 {
		pull, err := g.pulls.Pull(ctx, t.Repo.RepoRef, t.PullRequest)
		switch {
		case errors.Is(err, api.ErrRateLimited):
			return in, fmt.Errorf("gate: read the pull request base: %w", err)
		case err == nil:
			in.EventBase = pull.BaseSHA
		}
	}
	tip, err := g.repos.BranchHead(ctx, t.Repo.RepoRef, t.Repo.DefaultBranch)
	switch {
	case errors.Is(err, api.ErrRateLimited):
		return in, fmt.Errorf("gate: read the default branch head: %w", err)
	case err == nil:
		in.Tip = tip
	}
	return in, nil
}

// protectedPush reports whether a default-branch push may have changed a trusted input.
// No paths means "unknown", which fails towards the repository pass: the pass is idempotent.
func protectedPush(paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	return slices.ContainsFunc(paths, inputs.MayBeProtected)
}

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error) {
	return g.sweeper.Run(ctx, g.now())
}

// refreshOwnerDecision refreshes authorization on the PR and its queued groups before attempting reason comments.
func (g *Gate) refreshOwnerDecision(ctx context.Context, t inputs.Target) (string, error) {
	reaction, err := g.approvalChanged(ctx, t)
	err = errors.Join(err, g.groupsOf(ctx, t))
	if mirrorErr := g.resolver.MirrorDecisions(ctx, t, g.comments); mirrorErr != nil {
		err = errors.Join(err, fmt.Errorf("gate: mirror owner decisions: %w", mirrorErr))
	}
	return reaction, err
}
