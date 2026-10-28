package feedback

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	// repairOpen starts the first line of a repair issue: "<!-- archfit:repair <branch> -->". The second line is the head
	// marker of the comment: the head and run the body was written for.
	repairOpen   = "<!-- archfit:repair "
	repairTitle  = "Architecture repair needed on "
	titleRunes   = 200 // of the branch in the title; GitHub takes at most 256 characters
	actionClosed = "closed"
)

type repairer struct {
	lease    *serialized // the head's publication lease, and the state check read inside it
	comments api.Comments
	issues   api.Issues
	repos    api.Repos
}

func (r *repairer) Repair(ctx context.Context, p Publication) {
	action, number := actionNone, 0
	var failure error
	// The work never fails the hold. A failed issue call is no check write that a later publication could meet half
	// done, so the lease is released at once and the head's next publication does not wait for it to expire.
	err := r.lease.hold(ctx, p.Target, func(ctx context.Context) error {
		action, number, failure = r.repair(ctx, p)
		return nil
	})
	if err == nil {
		err = failure
	}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	slog.Default().Log(ctx, level, "feedback repair issue",
		"repo_id", p.Target.Repo.ID, "head_sha", p.Target.HeadSHA,
		"run_id", p.Receipt.RunID, "attempt", p.Receipt.RunAttempt, "conclusion", string(p.Outcome.Conclusion),
		"issue", action, "issue_number", number,
		"failed", err != nil, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
}

// repair runs inside the head's lease. Like the comment, it leaves the issue to newer feedback: nothing changes for a
// head that is no longer the branch's, or for a run older than the one the head's archfit/state receipt names. The
// receipt, not the issue's head marker, orders the runs: a pass leaves no open issue to name its run, and without
// the receipt an older failure could reopen what a newer pass closed.
func (r *repairer) repair(ctx context.Context, p Publication) (string, int, error) {
	t := p.Target
	if t.PullRequest != 0 {
		return actionNone, 0, nil
	}
	ref, branch := t.Repo.RepoRef, t.Repo.DefaultBranch
	head, err := r.repos.BranchHead(ctx, ref, branch)
	if err != nil {
		return actionNone, 0, err
	}
	if head != t.HeadSHA {
		return actionSkippedOldHead, 0, nil
	}
	state, found, err := r.lease.next.latestRun(ctx, ref, t.HeadSHA, stateCheck)
	if err != nil {
		return actionNone, 0, err
	}
	if found && supersedes(state, p.Receipt) {
		return actionSkippedNewer, 0, nil
	}
	existing, found, err := r.findIssue(ctx, ref, branch)
	if err != nil {
		return actionNone, 0, err
	}

	if passed(p) {
		if !found {
			return actionNone, 0, nil
		}
		// The comment goes first: when the close fails, the issue stays open and the next pass closes it.
		if _, err := r.comments.CreateComment(ctx, ref, existing.Number, repairClosing(p)); err != nil {
			return actionNone, existing.Number, err
		}
		return actionClosed, existing.Number, r.issues.CloseIssue(ctx, ref, existing.Number)
	}
	// An advisory repository chose not to be blocked; a repair issue there would stay open for as long as it has no
	// baseline. Only a required one is told. An issue opened before a switch to advisory is still closed on a pass above.
	if !p.Trusted.SettingsFound || p.Trusted.Settings.Gate != settings.GateRequired {
		return actionNone, 0, nil
	}
	body := renderRepair(p)
	switch {
	case found && body == existing.Body:
		return actionUnchanged, existing.Number, nil
	case found:
		return actionUpdated, existing.Number, r.issues.UpdateIssueBody(ctx, ref, existing.Number, body)
	}
	issue, err := r.create(ctx, ref, api.NewIssue{Title: repairTitle + cutRunes(sanitize(branch), titleRunes), Body: body,
		Assignee: p.Trusted.Settings.RepairAssignee})
	return actionCreated, issue.Number, err
}

// create opens the issue. When GitHub refuses it with the assignee (a login that cannot be assigned, or that left), it
// opens the issue unassigned: an issue nobody is assigned to still tells the repository what broke. Only that refusal is
// retried: after a timeout or a 5xx GitHub may have created the issue, and a second one would be a duplicate.
func (r *repairer) create(ctx context.Context, ref api.RepoRef, i api.NewIssue) (api.Issue, error) {
	issue, err := r.issues.CreateIssue(ctx, ref, i)
	if err == nil || i.Assignee == "" || !errors.Is(err, api.ErrUnprocessable) {
		return issue, err
	}
	slog.Default().WarnContext(ctx, "feedback repair issue opened without its assignee",
		"installation_id", ref.InstallationID, "status", api.Status(err))
	i.Assignee = ""
	return r.issues.CreateIssue(ctx, ref, i)
}

// findIssue returns the lowest-numbered open App issue whose body starts with the branch's repair marker.
func (r *repairer) findIssue(ctx context.Context, ref api.RepoRef, branch string) (api.Issue, bool, error) {
	all, err := r.issues.AppIssues(ctx, ref)
	if err != nil {
		return api.Issue{}, false, err
	}
	marker := repairMarker(branch)
	var found api.Issue
	for _, i := range all {
		if strings.HasPrefix(i.Body, marker+"\n") && (found.Number == 0 || i.Number < found.Number) {
			found = i
		}
	}
	return found, found.Number != 0, nil
}

func repairMarker(branch string) string {
	return repairOpen + sanitize(branch) + markerClose
}

// passed reports a push the branch needs no repair for: a success with a report the App shows. Anything else, a
// failure or an outcome that asks for action, is a repair.
func passed(p Publication) bool {
	return p.Outcome.Conclusion == verdict.Success && presentation(p).Report != nil
}

// renderRepair is the issue body: what broke and where, then the blocking findings, and the validation commands and
// agent tasks in the folds of the comment. A plain text first screen, so it reads in a mail client.
func renderRepair(p Publication) string {
	p = presentation(p)
	branch := code(p.Target.Repo.DefaultBranch)
	head := repairMarker(p.Target.Repo.DefaultBranch) + "\n" + headMarker(p.Target.HeadSHA, p.Receipt) + "\n## Archfit: " + headline(p)
	intro := "The newest Archfit result on the default branch " + branch + " asks for repair. This issue is a notification and " +
		"never blocks a merge: Archfit updates it after each push to " + branch + " and closes it when a push passes."
	l := layout{
		head:     join(head, intro, repairCause(p), commentIdentity(p)),
		decision: decisionBlock(p),
		inputs:   fixedTrustSentence,
		receipt:  receiptMarker(p.Receipt),
	}
	if !suspended(p.Outcome) && p.Report != nil {
		l.findings = blockersBlock(p)
		l.commands = commands(p.Report.AgentTasks)
		l.tasks, l.hasTasks = encodeTasks(p.Report.AgentTasks), true
	}
	return l.assemble(maxBody)
}

// repairCause is the deciding reason in words and its recovery; every reason is in the decision fold.
func repairCause(p Publication) string {
	reasons := displayReasons(p)
	if len(reasons) == 0 {
		return ""
	}
	first := reasons[0]
	cause := "Cause: " + sentenceCase(strings.ReplaceAll(string(first.Code), "_", " ")) + "."
	if first.Next != "" {
		cause += "\n\n**Next:** " + text(first.Next)
	}
	return cause
}

// repairClosing is the comment that closes the issue: the branch passes again, at this commit and run.
func repairClosing(p Publication) string {
	return "Archfit passes on " + code(p.Target.Repo.DefaultBranch) + " again. Closing this issue.\n\n" + commentIdentity(p)
}
