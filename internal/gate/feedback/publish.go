package feedback

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// stateNeutral is allowed on archfit/state only, which is never a required check, and only for an outcome that
// does not ask for action. It is deliberately not a verdict.Conclusion.
const stateNeutral = "neutral"

const (
	pendingTitle   = "Waiting for report"
	pendingSummary = "Waiting for the Archfit report. If no report arrives in time, this check asks for action."
)

// Actions, as logged.
const (
	actionNone           = "none"
	actionCreated        = "created"
	actionUpdated        = "updated"
	actionUnchanged      = "unchanged"
	actionSkippedNewer   = "skipped_newer"
	actionSkippedOldHead = "skipped_old_head"
)

type publisher struct {
	checks   api.Checks
	comments api.Comments
	pulls    api.Pulls
	appURL   string
	now      func() time.Time
}

// pullLink is the App's page of the target's pull request; "" without an App URL.
func (s *publisher) pullLink(t inputs.Target) string {
	if s.appURL == "" || t.PullRequest == 0 {
		return ""
	}
	owner, name, _ := strings.Cut(t.Repo.FullName, "/")
	return s.appURL + "/r/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pull/" + strconv.Itoa(t.PullRequest)
}

func (s *publisher) Publish(ctx context.Context, p Publication) (err error) {
	now := s.now()
	gate, state, comment := actionNone, actionNone, actionNone
	defer func() {
		codes := make([]string, len(p.Outcome.Reasons))
		for i, r := range p.Outcome.Reasons {
			codes[i] = string(r.Code)
		}
		slog.Default().InfoContext(ctx, "feedback published",
			"repo_id", p.Target.Repo.ID, "pull", p.Target.PullRequest, "head_sha", p.Target.HeadSHA,
			"run_id", p.Receipt.RunID, "attempt", p.Receipt.RunAttempt,
			"report_digest", p.Receipt.ReportDigest, "policy_digest", p.Receipt.PolicyDigest,
			"baseline_digest", p.Receipt.BaselineDigest, "labels_digest", p.Receipt.LabelsDigest,
			"conclusion", string(p.Outcome.Conclusion), "reasons", codes,
			"gate", gate, "state", state, "comment", comment,
			"failed", err != nil, "elapsed", s.now().Sub(now))
	}()

	isPull := p.Target.PullRequest != 0 && p.Trusted.SettingsFound
	same, err := s.unchanged(ctx, p, isPull)
	if err != nil {
		return err
	}
	if same {
		gate, state, comment = actionUnchanged, actionUnchanged, actionUnchanged
		return nil
	}
	run := api.CheckRun{
		HeadSHA:    p.Target.HeadSHA,
		ExternalID: externalID(p.Receipt),
		DetailsURL: s.pullLink(p.Target), // GitHub's "Details" opens the App's pull request page
		Status:     statusCompleted,
		StartedAt:  now,
	}
	if required(p) { // the same predicate the headline reads, so a title never names a gate that was not written
		run.Name, run.Conclusion = gateCheck, string(p.Outcome.Conclusion)
		run.Title, run.Summary = gateTitle(p), renderGate(p)
		if gate, _, err = s.writeCheck(ctx, p.Target.Repo.RepoRef, run, p.Receipt); err != nil {
			return err
		}
	}
	// The file list only filters annotations, so it is read after the required check is written. Without it the
	// state check carries no annotation; a rate limit is still returned so that the caller retries.
	changed, listErr := s.changedFiles(ctx, p)
	run.Name, run.Conclusion = stateCheck, stateConclusion(p.Outcome, p.Report)
	run.Title, run.Summary = stateTitle(p), renderState(p)
	run.Annotations = nil
	if listErr == nil {
		run.Annotations = annotations(p, changed)
	}
	if state, p.stateCheckID, err = s.writeCheck(ctx, p.Target.Repo.RepoRef, run, p.Receipt); err != nil {
		return err
	}
	if isPull {
		if comment, err = s.writeComment(ctx, p, now); err != nil {
			return err
		}
	}
	if listErr != nil {
		slog.Default().WarnContext(ctx, "feedback published without annotations: changed files unavailable",
			"repo_id", p.Target.Repo.ID, "pull", p.Target.PullRequest, "head_sha", p.Target.HeadSHA,
			"rate_limited", errors.Is(listErr, api.ErrRateLimited))
	}
	if errors.Is(listErr, api.ErrRateLimited) {
		return listErr
	}
	return nil
}

// unchanged reports whether publishing p would change nothing a reader sees: the checks it would write are already on
// the head with the same identity key, outcome and inputs, and the comment is already there for this head. A re-run on
// unchanged inputs measures the same thing, so it adds no check run and edits no comment. A report without an identity
// has no key, and is always published.
func (s *publisher) unchanged(ctx context.Context, p Publication, isPull bool) (bool, error) {
	key := p.Receipt.IdentityKey
	if key == "" {
		return false, nil
	}
	names := []string{stateCheck}
	if required(p) {
		names = append(names, gateCheck)
	}
	for _, name := range names {
		existing, found, err := s.latestRun(ctx, p.Target.Repo.RepoRef, p.Target.HeadSHA, name)
		if err != nil || !found || existing.Status != statusCompleted {
			return false, err
		}
		rc, ok := parseReceipt(existing.Summary)
		if !ok || rc.IdentityKey != key || rc.Conclusion != p.Receipt.Conclusion || rc.Reason != p.Receipt.Reason ||
			rc.PolicyDigest != p.Receipt.PolicyDigest || rc.BaselineDigest != p.Receipt.BaselineDigest ||
			rc.LabelsDigest != p.Receipt.LabelsDigest || !rc.EarliestWaiverExpiry.Equal(p.Receipt.EarliestWaiverExpiry) {
			return false, nil
		}
	}
	if !isPull {
		return true, nil
	}
	existing, found, err := s.findComment(ctx, p.Target)
	if err != nil || !found {
		return false, err
	}
	head, _, _, ok := parseHead(existing.Body)
	return ok && head == p.Target.HeadSHA, nil
}

// stateConclusion fails closed like the gate: missing, unverifiable or ineligible evidence asks for action and is
// never neutral. Only advisory attention and architecture findings, which this check never enforces, are neutral.
func stateConclusion(o verdict.Outcome, r *report.Report) string {
	switch {
	case o.Conclusion == verdict.ActionRequired:
		return string(verdict.ActionRequired)
	case o.Conclusion == verdict.Success && r != nil && r.Verdict == "healthy":
		return string(verdict.Success)
	}
	return stateNeutral
}

// latestRun returns the run with the highest ID. Two concurrent first publications may create two
// runs; every later call settles on the same one.
func (s *publisher) latestRun(ctx context.Context, r api.RepoRef, headSHA, name string) (api.CheckRun, bool, error) {
	runs, err := s.checks.CheckRuns(ctx, r, headSHA, name)
	if err != nil || len(runs) == 0 {
		return api.CheckRun{}, false, err
	}
	latest := runs[0]
	for _, run := range runs[1:] {
		if run.ID > latest.ID {
			latest = run
		}
	}
	return latest, true, nil
}

// writeCheck updates the latest run in place when that shows exactly this publication, else creates a run.
func (s *publisher) writeCheck(ctx context.Context, r api.RepoRef, run api.CheckRun, receipt Receipt) (string, int64, error) {
	existing, found, err := s.latestRun(ctx, r, run.HeadSHA, run.Name)
	if err != nil {
		return actionNone, 0, err
	}
	switch {
	case found && supersedes(existing, receipt):
		return actionSkippedNewer, 0, nil
	case found && reusable(existing, run):
		run.ID, run.Annotations = existing.ID, nil
		if err := s.checks.UpdateCheckRun(ctx, r, run); err != nil {
			return actionUpdated, 0, err
		}
		return actionUpdated, existing.ID, nil
	}
	id, err := s.createRun(ctx, r, run, receipt)
	return actionCreated, id, err
}

// reusable reports whether an update in place leaves the run showing exactly run. GitHub appends the annotations of
// every request and never removes one, so annotations go only on a new run. archfit/gate carries none, so its latest
// run, the pending one included, is always updated. An archfit/state run is updated only when it already holds this
// publication's annotations: the same external_id and as many annotations as run would send. An equal count stands
// in for equal annotations, because one external_id names one report and annotations are rendered
// deterministically from it.
func reusable(existing, run api.CheckRun) bool {
	if run.Name == gateCheck {
		return true
	}
	return existing.ExternalID == run.ExternalID && existing.AnnotationCount == len(run.Annotations)
}

// createRun creates the run with the first annotationsPerRequest annotations and sends the rest in updates of the
// same run: GitHub takes 50 per request and appends each batch. Before each further batch it reads the latest run
// again and stops when another run took its place or a newer publication superseded it, so an older publication
// never overwrites a newer summary.
func (s *publisher) createRun(ctx context.Context, r api.RepoRef, run api.CheckRun, receipt Receipt) (int64, error) {
	var rest []api.Annotation
	run.Annotations, rest = batch(run.Annotations)
	created, err := s.checks.CreateCheckRun(ctx, r, run)
	if err != nil {
		return 0, err
	}
	run.ID = created.ID
	for len(rest) > 0 {
		latest, found, err := s.latestRun(ctx, r, run.HeadSHA, run.Name)
		if err != nil {
			return 0, err
		}
		if !found || latest.ID != run.ID || supersedes(latest, receipt) {
			return 0, nil
		}
		run.Annotations, rest = batch(rest)
		if err := s.checks.UpdateCheckRun(ctx, r, run); err != nil {
			return 0, err
		}
	}
	return run.ID, nil
}

// batch splits off the annotations of one request.
func batch(all []api.Annotation) (now, later []api.Annotation) {
	n := min(len(all), annotationsPerRequest)
	return all[:n], all[n:]
}

// changedFiles is the annotation filter: on a pull request the files it changed, everywhere else every file.
// GitHub's list is read only when some annotation is possible. A truncated list means any file may have changed.
func (s *publisher) changedFiles(ctx context.Context, p Publication) (func(string) bool, error) {
	if p.Target.PullRequest == 0 || len(annotations(p, anyPath)) == 0 {
		return anyPath, nil
	}
	paths, truncated, err := s.pulls.ChangedFiles(ctx, p.Target.Repo.RepoRef, p.Target.PullRequest)
	if err != nil || truncated {
		return anyPath, err
	}
	set := make(map[string]bool, len(paths))
	for _, path := range paths {
		set[path] = true
	}
	return func(path string) bool { return set[path] }, nil
}

// findComment returns the lowest-ID App comment that starts with the comment marker.
func (s *publisher) findComment(ctx context.Context, t inputs.Target) (api.Comment, bool, error) {
	all, err := s.comments.AppComments(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return api.Comment{}, false, err
	}
	var found api.Comment
	for _, c := range all {
		if strings.HasPrefix(c.Body, commentMarker) && (found.ID == 0 || c.ID < found.ID) {
			found = c
		}
	}
	return found, found.ID != 0, nil
}

func (s *publisher) writeComment(ctx context.Context, p Publication, now time.Time) (string, error) {
	t := p.Target
	pull, err := s.pulls.Pull(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return actionNone, err
	}
	if pull.HeadSHA != t.HeadSHA {
		return actionSkippedOldHead, nil
	}
	existing, found, err := s.findComment(ctx, t)
	if err != nil {
		return actionNone, err
	}
	if !found {
		_, err := s.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, renderComment(p, now, "", s.pullLink(t)))
		return actionCreated, err
	}
	if head, runID, attempt, ok := parseHead(existing.Body); ok && head == t.HeadSHA && newerRun(runID, attempt, p.Receipt) {
		return actionSkippedNewer, nil
	}
	waiver := ""
	if date, ok := currentWaiver(existing.Body); ok && !p.Trusted.EarliestWaiverExpiry.IsZero() && date == waiverDate(p.Trusted.EarliestWaiverExpiry) {
		waiver = date
	}
	body := renderComment(p, now, waiver, s.pullLink(t))
	if body == existing.Body {
		return actionUnchanged, nil
	}
	return actionUpdated, s.comments.UpdateComment(ctx, t.Repo.RepoRef, existing.ID, body)
}

func (s *publisher) Pending(ctx context.Context, t inputs.Target) error {
	action := actionNone
	_, found, err := s.latestRun(ctx, t.Repo.RepoRef, t.HeadSHA, gateCheck)
	if err == nil && !found {
		action = actionCreated
		_, err = s.checks.CreateCheckRun(ctx, t.Repo.RepoRef, api.CheckRun{
			Name:       gateCheck,
			HeadSHA:    t.HeadSHA,
			DetailsURL: s.pullLink(t),
			Status:     statusInProgress,
			Title:      pendingTitle,
			Summary:    pendingSummary,
			StartedAt:  s.now(),
		})
	}
	slog.Default().InfoContext(ctx, "feedback pending",
		"repo_id", t.Repo.ID, "pull", t.PullRequest, "head_sha", t.HeadSHA, "gate", action, "failed", err != nil)
	return err
}

func (s *publisher) State(ctx context.Context, t inputs.Target) (CheckState, error) {
	return s.checkState(ctx, t, gateCheck)
}

func (s *publisher) AdvisoryState(ctx context.Context, t inputs.Target) (CheckState, error) {
	return s.checkState(ctx, t, stateCheck)
}

func (s *publisher) checkState(ctx context.Context, t inputs.Target, name string) (CheckState, error) {
	run, found, err := s.latestRun(ctx, t.Repo.RepoRef, t.HeadSHA, name)
	if err != nil || !found {
		return CheckState{}, err
	}
	state := CheckState{Exists: true, Pending: run.Status != statusCompleted, StartedAt: run.StartedAt}
	if !state.Pending {
		state.Receipt, _ = parseReceipt(run.Summary)
	}
	return state, nil
}

func (s *publisher) Warn(ctx context.Context, t inputs.Target, expiry time.Time) (err error) {
	if t.PullRequest == 0 || expiry.IsZero() {
		return nil
	}
	action := actionNone
	defer func() {
		slog.Default().InfoContext(ctx, "feedback waiver notice",
			"repo_id", t.Repo.ID, "pull", t.PullRequest, "head_sha", t.HeadSHA,
			"expiry", waiverDate(expiry), "comment", action, "failed", err != nil)
	}()

	date := waiverDate(expiry)
	existing, found, err := s.findComment(ctx, t)
	if err != nil {
		return err
	}
	if !found {
		action = actionCreated
		_, err = s.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, placeWaiver(commentMarker, waiverSection(date)))
		return err
	}
	if current, ok := currentWaiver(existing.Body); ok && current == date {
		action = actionUnchanged
		return nil
	}
	action = actionUpdated
	body := placeWaiver(removeWaiver(existing.Body), waiverSection(date))
	return s.comments.UpdateComment(ctx, t.Repo.RepoRef, existing.ID, body)
}
