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

// Reactions, as logged.
const (
	reactionNone         = "none"
	reactionPending      = "pending"
	reactionForkRefused  = "fork_refused"
	reactionRepoSweep    = "repo_sweep"
	reactionSharedHead   = "shared_head" // a pull request into another base: the pull requests into the default branch on its head are checked
	reactionRecheck      = "recheck"     // a default-branch push moved the target: open pull requests are checked for a module cycle with it
	reactionNotOnboarded = "not_onboarded"
	reactionNotAdmitted  = "not_admitted"  // no operator approved the installation: nothing is read or written
	reactionOtherBase    = "other_base"    // a pull request into a branch other than the default branch: nothing is read or written
	reactionPullClosed   = "pull_closed"   // a closed pull request left the Archfit 4 pull index
	reactionGroupChecked = "group_checked" // merge groups were re-read inside their leases; a blocked one was corrected
	// groupMerged is the destroyed reason of a merge group that landed: its result stands.
	groupMerged = "merged"
)

// HandleEvent reacts to a verified webhook event: a pending check on a new head, a fork refusal, a request line or a
// request issue, a closed pull request, the checks of a merge group, and a default-branch push, whose policy is judged
// before a re-check or a one-repository sweep. No event approves a protected change.
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
		if err := g.retireForeignProposal(ctx, eventTarget(e)); err != nil {
			return reactionNone, err
		}
		return g.newHead(ctx, eventTarget(e))
	case webhook.PullClosed:
		return reactionPullClosed, g.pullClosed(ctx, e)
	case webhook.PullCommented:
		if asks.IsRequest(e.Body) {
			return g.requestCommented(ctx, e)
		}
		return reactionNone, nil
	case webhook.IssueOpened:
		return g.requestFiled(ctx, e)
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
	case webhook.DefaultBranchPush:
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

// branchPushed reacts to a push to the default branch: it is judged for the policy it brings (policyPushed, M2 task
// 20), and one that changes a protected path also runs the repository sweep. A push approves nothing: a local coding
// agent pushes with its owner's credentials.
func (g *Gate) branchPushed(ctx context.Context, e webhook.Event) (string, error) {
	t := eventTarget(e)
	// Judged before the sweep reads the new policy: a consume or "outside the flow" is recorded first.
	if err := g.policyPushed(ctx, e); err != nil {
		return reactionNone, err
	}
	if err := g.workflowPushed(ctx, e); err != nil {
		return reactionNone, err
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
	switch trusted.Settings.Gate {
	case settings.GateRequired:
		if err := g.publisher.Pending(ctx, t); err != nil {
			return reactionPending, fmt.Errorf("gate: pending check: %w", err)
		}
		return reactionPending, nil
	}
	return reactionNone, nil
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
