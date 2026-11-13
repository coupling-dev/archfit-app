package sweep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The paths of the re-check after an answer, as the pull record keeps them (records.Recheck.Path).
const (
	PathRerun      = "rerun"       // the latest run of the head runs again and reads the new policy
	PathRunning    = "running"     // a run of the head is queued or in progress: its upload decides
	PathUpdated    = "updated"     // the App updated the branch: the run of that push decides
	PathNotUpdated = "not_updated" // GitHub refused the update: the owner or the author updates the branch
	PathHeadMoved  = "head_moved"  // the author pushed meanwhile: the run of that push decides
	PathFork       = "fork"        // the App cannot push to a fork: the author updates the branch
)

// The results the pull record holds for an upload the App accepted as it is: an envelope 2 run of the fixed workflow,
// which reads the policy from the target tip (design 9.1). An unknown one may be a workflow or a policy refusal.
var acceptedStatuses = []string{string(result.StatusPass), string(result.StatusFail), string(result.StatusWaiting)}

// AnswerGitHub is what the re-check after an answer reads and writes on GitHub.
type AnswerGitHub interface {
	Runs
	Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
	BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error)
	UpdateBranch(ctx context.Context, r api.RepoRef, number int, headSHA string) error
	CreateComment(ctx context.Context, r api.RepoRef, number int, body string) (api.Comment, error)
}

// Consumed is a proposal whose merge consumed its approvals (gate/approvals): its requests and the pull requests
// waiting on any of them.
type Consumed struct {
	Proposal string
	Requests []int
	Pulls    []int
}

// AfterAnswer gets every pull request waiting on a request of the consumed proposal c a fresh result without an action
// by its author (design 10.5): a re-run of its latest run when the four conditions hold (Rerun), else an update of its
// branch with the App's token and the head it read. It records the path in each pull record and tells each request
// issue about the pull requests it acted on. A pull request whose record already names c is skipped, so a delivery of
// the same push again does nothing twice. An error is a failed read or write that may pass later: the pull requests
// done before it are told, and the caller retries the delivery.
func AfterAnswer(ctx context.Context, gh AnswerGitHub, s store.Store, r api.Repo, c Consumed, now time.Time) error {
	a := &answer{gh: gh, store: s, repo: r, proposal: c.Proposal, now: now}
	done := map[int]records.Recheck{}
	pulls := slices.Clone(c.Pulls)
	slices.Sort(pulls)
	var failed error
	for _, n := range slices.Compact(pulls) {
		rc, err := a.pull(ctx, n)
		if rc.Path != "" {
			done[n] = rc
		}
		if err != nil {
			failed = fmt.Errorf("sweep: re-check pull request %d: %w", n, err)
			break
		}
	}
	if len(done) > 0 {
		failed = errors.Join(failed, a.tell(ctx, c.Requests, done))
	}
	return failed
}

// answer is one re-check after an answer. Nothing of it outlives AfterAnswer.
type answer struct {
	gh       AnswerGitHub
	store    store.Store
	repo     api.Repo
	proposal string
	now      time.Time
	tip      string
	tipRead  bool
}

// pull re-checks pull request n and records the path in its pull record, where uploads keep it. An empty path did
// nothing. A pull request without a pull record is not recorded.
func (a *answer) pull(ctx context.Context, n int) (records.Recheck, error) {
	t, name := store.Tenant(a.repo.InstallationID), a.repo.FullName
	var rec records.Pull
	err := records.Load(ctx, a.store, t, records.PullKey(name, n), &rec)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return records.Recheck{}, err
	}
	if rec.Rechecked(a.proposal) {
		return records.Recheck{}, nil
	}
	pl, err := a.gh.Pull(ctx, a.repo.RepoRef, n)
	if errors.Is(err, api.ErrNotFound) {
		return records.Recheck{}, nil
	}
	if err != nil {
		return records.Recheck{}, err
	}
	if pl.State != "open" || pl.BaseRef != a.repo.DefaultBranch {
		return records.Recheck{}, nil // a closed pull request leaves its requests; another base is never decided
	}
	rc, err := a.path(ctx, pl, rec)
	if err != nil || rc.Path == "" {
		return records.Recheck{}, err
	}
	a.log(ctx, n, rc)
	if _, err := records.Rechecked(ctx, a.store, t, name, n, rc); err != nil {
		return rc, err
	}
	return rc, nil
}

// path runs the re-check of the open pull request pl, whose pull record is rec. An empty path did nothing.
func (a *answer) path(ctx context.Context, pl api.Pull, rec records.Pull) (records.Recheck, error) {
	rc := records.Recheck{Proposal: a.proposal, HeadSHA: pl.HeadSHA}
	if pl.HeadRepo != a.repo.FullName {
		rc.Path = PathFork
		return rc, nil
	}
	tip, err := a.readTip(ctx)
	if err != nil {
		return rc, err
	}
	in := RerunInput{
		Receipt: feedback.Receipt{RunID: rec.RunID, RunAttempt: rec.RunAttempt},
		Head:    pl.HeadSHA,
		// The pull request's base stays at the base of the event of its latest head: it does not follow the base
		// branch (design D.2).
		EventBase: pl.BaseSHA,
		Tip:       tip,
		ReadsTip:  slices.Contains(acceptedStatuses, rec.Status),
	}
	res, unmet, err := Rerun(ctx, a.gh, a.repo.RepoRef, in, a.now)
	switch {
	case err != nil:
		return rc, err
	case res == RerunRequested:
		rc.Path = PathRerun
		return rc, nil
	case res == RerunInFlight:
		rc.Path = PathRunning
		return rc, nil
	}
	rc.Unmet = string(unmet)
	rc.Path, err = a.update(ctx, pl)
	return rc, err
}

// update merges the base branch into pl's branch with the App's token, when its head is still the one read. GitHub
// answers 202 before the new head exists, so nothing reads the head after it: the pull_request run of that push
// decides. A refusal (422; 403, as for a merge that brings workflow changes the App may not push) is recorded, not
// retried: the head is read once more, to tell an author's push from a refusal. A pull request GitHub no longer has
// does nothing.
func (a *answer) update(ctx context.Context, pl api.Pull) (string, error) {
	err := a.gh.UpdateBranch(ctx, a.repo.RepoRef, pl.Number, pl.HeadSHA)
	switch {
	case err == nil:
		return PathUpdated, nil
	case errors.Is(err, api.ErrNotFound):
		return "", nil
	case !refused(err):
		return "", err
	}
	now, err := a.gh.Pull(ctx, a.repo.RepoRef, pl.Number)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return "", err
	}
	if err == nil && now.HeadSHA != pl.HeadSHA {
		return PathHeadMoved, nil
	}
	return PathNotUpdated, nil
}

// refused reports whether GitHub refused an update for good: 422, or 403 that is not a rate limit.
func refused(err error) bool {
	return errors.Is(err, api.ErrUnprocessable) || (api.Status(err) == 403 && !errors.Is(err, api.ErrRateLimited))
}

// readTip reads the default branch's head once per re-check; a branch GitHub does not have is unknown ("").
func (a *answer) readTip(ctx context.Context) (string, error) {
	if a.tipRead {
		return a.tip, nil
	}
	tip, err := a.gh.BranchHead(ctx, a.repo.RepoRef, a.repo.DefaultBranch)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return "", err
	}
	a.tip, a.tipRead = tip, true
	return tip, nil
}

// tell posts one comment on each request issue, listing the pull requests waiting on it that this call re-checked and
// their path. A comment is a notification: a failure is logged.
func (a *answer) tell(ctx context.Context, requests []int, done map[int]records.Recheck) error {
	t, name := store.Tenant(a.repo.InstallationID), a.repo.FullName
	for _, id := range requests {
		var req records.Request
		if err := records.Load(ctx, a.store, t, records.RequestKey(name, id), &req); err != nil {
			return fmt.Errorf("sweep: read request %d: %w", id, err)
		}
		var lines []string
		for _, n := range slices.Sorted(slices.Values(req.Waiting)) {
			if rc, ok := done[n]; ok {
				lines = append(lines, "- #"+strconv.Itoa(n)+": "+pathText(rc, a.repo.DefaultBranch))
			}
		}
		if len(lines) == 0 {
			continue
		}
		body := "The policy changed with the answer. The pull requests that waited on this request are checked again:\n\n" +
			strings.Join(lines, "\n") + "\n"
		if _, err := a.gh.CreateComment(ctx, a.repo.RepoRef, id, body); err != nil {
			slog.Default().WarnContext(ctx, "sweep: re-check note failed", "installation_id", a.repo.InstallationID,
				"repository_id", a.repo.ID, "issue", id, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
		}
	}
	return nil
}

// pathText says what happened to one pull request, in the words of design section 5.
func pathText(rc records.Recheck, branch string) string {
	switch rc.Path {
	case PathRerun:
		return "its check runs again with the new policy."
	case PathRunning:
		return "a check of its head is already running; its result decides."
	case PathHeadMoved:
		return "its head moved; the check of the new commit decides."
	case PathFork:
		return "not updated: it comes from a fork, and the App cannot push to it. The author updates the branch."
	case PathNotUpdated:
		return "not updated: GitHub refused the update. An owner or the author updates the branch. " + unmetText(rc.Unmet, branch)
	}
	return "the App updated its branch; the check of the new commit decides. " + unmetText(rc.Unmet, branch)
}

// unmetText says why the check could not simply run again.
func unmetText(u string, branch string) string {
	switch Unmet(u) {
	case UnmetNoRun:
		return "No run of its check is known."
	case UnmetNotLatest:
		return "Its last run is not the latest run of its head."
	case UnmetPolicyNotFromTip:
		return "Its last run did not read the policy from " + branch + "."
	case UnmetTooOld:
		return "Its last run is 30 days old or older."
	case UnmetBaseChanged:
		return branch + " changed in more than the policy since its last run."
	}
	return ""
}

func (a *answer) log(ctx context.Context, n int, rc records.Recheck) {
	slog.Default().InfoContext(ctx, "sweep: re-check after an answer", "installation_id", a.repo.InstallationID,
		"repository_id", a.repo.ID, "pr", n, "head_sha", rc.HeadSHA, "proposal", a.proposal, "path", rc.Path, "unmet", rc.Unmet)
}
