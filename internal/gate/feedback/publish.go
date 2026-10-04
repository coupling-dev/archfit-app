package feedback

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
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
	now      func() time.Time
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

	// Read before any write, so that a failure leaves every check as it was.
	changed, err := s.changedFiles(ctx, p)
	if err != nil {
		return err
	}
	isPull := p.Target.PullRequest != 0 && p.Trusted.SettingsFound
	run := api.CheckRun{
		HeadSHA:    p.Target.HeadSHA,
		ExternalID: externalID(p.Receipt),
		Status:     statusCompleted,
		StartedAt:  now,
	}
	if isPull && p.Trusted.Settings.Gate == settings.GateRequired {
		run.Name, run.Conclusion = gateCheck, string(p.Outcome.Conclusion)
		run.Title, run.Summary = gateTitle(p), renderGate(p)
		if gate, err = s.writeCheck(ctx, p.Target.Repo.RepoRef, run, p.Receipt); err != nil {
			return err
		}
	}
	run.Name, run.Conclusion = stateCheck, stateConclusion(p.Outcome, p.Report)
	run.Title, run.Summary = stateTitle(p), renderState(p)
	run.Annotations = annotations(p, changed)
	if state, err = s.writeCheck(ctx, p.Target.Repo.RepoRef, run, p.Receipt); err != nil {
		return err
	}
	if isPull {
		comment, err = s.writeComment(ctx, p, now)
	}
	return err
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

// writeCheck creates or updates the run with the first annotationsPerRequest annotations, then sends the rest in
// further updates of the same run: GitHub takes 50 per request and appends each batch.
func (s *publisher) writeCheck(ctx context.Context, r api.RepoRef, run api.CheckRun, receipt Receipt) (string, error) {
	existing, found, err := s.latestRun(ctx, r, run.HeadSHA, run.Name)
	if err != nil {
		return actionNone, err
	}
	if found && supersedes(existing, receipt) {
		return actionSkippedNewer, nil
	}
	var rest []api.Annotation
	run.Annotations, rest = batch(run.Annotations)
	action := actionUpdated
	if found {
		run.ID = existing.ID
		err = s.checks.UpdateCheckRun(ctx, r, run)
	} else {
		var created api.CheckRun
		action = actionCreated
		created, err = s.checks.CreateCheckRun(ctx, r, run)
		run.ID = created.ID
	}
	for err == nil && len(rest) > 0 {
		run.Annotations, rest = batch(rest)
		err = s.checks.UpdateCheckRun(ctx, r, run)
	}
	return action, err
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
		_, err := s.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, renderComment(p, now, ""))
		return actionCreated, err
	}
	if head, runID, attempt, ok := parseHead(existing.Body); ok && head == t.HeadSHA && newerRun(runID, attempt, p.Receipt) {
		return actionSkippedNewer, nil
	}
	waiver := ""
	if date, ok := currentWaiver(existing.Body); ok && !p.Trusted.EarliestWaiverExpiry.IsZero() && date == waiverDate(p.Trusted.EarliestWaiverExpiry) {
		waiver = date
	}
	body := renderComment(p, now, waiver)
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
			Name:      gateCheck,
			HeadSHA:   t.HeadSHA,
			Status:    statusInProgress,
			Title:     pendingTitle,
			Summary:   pendingSummary,
			StartedAt: s.now(),
		})
	}
	slog.Default().InfoContext(ctx, "feedback pending",
		"repo_id", t.Repo.ID, "pull", t.PullRequest, "head_sha", t.HeadSHA, "gate", action, "failed", err != nil)
	return err
}

func (s *publisher) State(ctx context.Context, t inputs.Target) (CheckState, error) {
	run, found, err := s.latestRun(ctx, t.Repo.RepoRef, t.HeadSHA, gateCheck)
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
