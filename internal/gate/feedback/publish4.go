package feedback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The conclusions of archfit/gate for an Archfit 4 result (design 11.6).
const (
	conclusionSuccess        = "success"
	conclusionFailure        = "failure"
	conclusionNeutral        = "neutral"
	conclusionActionRequired = "action_required"
)

// receiptV2 starts the external_id of an Archfit 4 check run: "v2:<run_id>:<attempt>:<payload_digest>". The check is
// its own receipt, as in v1; nothing else is stored.
const receiptV2 = "v2"

// checkState is the check run status and conclusion of a status (design 11.6, appendix D.2): pass is a success; fail
// a failure, or neutral in advisory mode; waiting is in_progress, which shows as pending and blocks a merge; unknown,
// and any status the App does not know, asks for action, which also blocks.
func checkState(s result.Status, advisory bool) (status, conclusion string) {
	switch s {
	case result.StatusPass:
		return statusCompleted, conclusionSuccess
	case result.StatusFail:
		if advisory {
			return statusCompleted, conclusionNeutral
		}
		return statusCompleted, conclusionFailure
	case result.StatusWaiting:
		return statusInProgress, ""
	}
	return statusCompleted, conclusionActionRequired
}

func externalID4(p Publication4) string {
	return fmt.Sprintf("%s:%d:%d:%s", receiptV2, p.RunID, p.RunAttempt, p.Digest)
}

// newer4 reports whether a check run holds the publication of a newer run, or a newer attempt of the same run, than
// p, an Archfit 4 or a v1 one. A waiting run is in_progress and still counts: its external_id is its receipt.
func newer4(c api.CheckRun, p Publication4) bool {
	_, runID, attempt, ok := externalRun(c.ExternalID)
	return ok && newerRun(runID, attempt, Receipt{RunID: p.RunID, RunAttempt: p.RunAttempt})
}

// reusable4 reports whether an update in place leaves the run showing exactly run. GitHub appends the annotations of
// every request and never removes one: an update sends none, so the run must already hold run's (the same receipt and
// count; one receipt names one result, whose annotations are rendered deterministically) or neither has any. A
// completed run is never set back to in_progress: a waiting result gets a new run.
func reusable4(existing, run api.CheckRun) bool {
	if existing.Status == statusCompleted && run.Status != statusCompleted {
		return false
	}
	if existing.AnnotationCount == 0 && len(run.Annotations) == 0 {
		return true
	}
	return existing.ExternalID == run.ExternalID && existing.AnnotationCount == len(run.Annotations)
}

// Publish4 writes p inside the head's lease, unless p.Current says its status was decided from inputs that are no
// longer current.
func (s *serialized) Publish4(ctx context.Context, p Publication4) error {
	return s.hold(ctx, p.Target, func(ctx context.Context) error { return s.current4(ctx, p, false) })
}

// Withdraw4 resolves a missing record's publication identity from the live check under the same lease.
func (s *serialized) Withdraw4(ctx context.Context, p Publication4) error {
	if p.Result.Status != result.StatusUnknown {
		return errors.New("feedback: a withdrawal must be unknown")
	}
	return s.hold(ctx, p.Target, func(ctx context.Context) error { return s.current4(ctx, p, true) })
}

func (s *serialized) current4(ctx context.Context, p Publication4, withdraw bool) error {
	if p.Current != nil {
		ok, err := p.Current(ctx)
		if err != nil {
			return fmt.Errorf("feedback: read the inputs again: %w: %w", errUnwritten, err)
		}
		if !ok {
			return ErrStaleInputs
		}
	}
	if withdraw {
		existing, found, err := s.next.latestRun(ctx, p.Target.Repo.RepoRef, p.Target.HeadSHA, gateCheck)
		if err != nil {
			return err
		}
		if _, run, attempt, ok := externalRun(existing.ExternalID); found && ok {
			p.RunID, p.RunAttempt = run, attempt
			p.Digest = strings.SplitN(existing.ExternalID, ":", 4)[3]
		}
	}
	return s.next.publish4(ctx, p)
}

func (s *publisher) publish4(ctx context.Context, p Publication4) (err error) {
	start := s.now()
	gate, comment := actionNone, actionNone
	status, conclusion := checkState(p.Result.Status, p.Advisory)
	defer func() {
		slog.Default().InfoContext(ctx, "feedback published",
			"envelope", 2, "repo_id", p.Target.Repo.ID, "pull", p.Target.PullRequest, "head_sha", p.Target.HeadSHA,
			"run_id", p.RunID, "attempt", p.RunAttempt, "digest", p.Digest,
			"status", string(p.Result.Status), "conclusion", conclusion,
			"gate", gate, "comment", comment, "failed", err != nil, "elapsed", s.now().Sub(start))
	}()

	v := newView(p.Result)
	run := api.CheckRun{
		Name:        gateCheck,
		HeadSHA:     p.Target.HeadSHA,
		ExternalID:  externalID4(p),
		DetailsURL:  s.pullLink(p.Target),
		Status:      status,
		Conclusion:  conclusion,
		Title:       v.title(),
		Summary:     v.summary(),
		Annotations: v.annotations(),
		StartedAt:   start,
	}
	if gate, err = s.writeCheck4(ctx, p, run); err != nil || p.Target.PullRequest == 0 {
		return err
	}
	comment, err = s.writeComment4(ctx, p, v)
	return err
}

// writeCheck4 updates the latest archfit/gate run in place when reusable4 allows it, else creates a run with the
// annotations in batches, and writes nothing over a newer publication.
func (s *publisher) writeCheck4(ctx context.Context, p Publication4, run api.CheckRun) (string, error) {
	r := p.Target.Repo.RepoRef
	existing, found, err := s.latestRun(ctx, r, run.HeadSHA, gateCheck)
	if err != nil {
		return actionNone, err
	}
	superseded := func(c api.CheckRun) bool { return newer4(c, p) }
	switch {
	case found && superseded(existing):
		return actionSkippedNewer, nil
	case found && !p.Fresh && reusable4(existing, run):
		run.ID, run.Annotations = existing.ID, nil
		return actionUpdated, s.checks.UpdateCheckRun(ctx, r, run)
	}
	_, err = s.createRun(ctx, r, run, superseded)
	return actionCreated, err
}

// writeComment4 writes the pull request's one comment in place: its markers, then the view's comment. It skips a head
// that is no longer the pull request's and a comment that holds a newer run of the same head.
func (s *publisher) writeComment4(ctx context.Context, p Publication4, v view) (string, error) {
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
	receipt := Receipt{RunID: p.RunID, RunAttempt: p.RunAttempt}
	markers := commentMarker + "\n" + headMarker(t.HeadSHA, receipt) + "\n"
	body := markers + v.comment(maxBody-len(markers))
	if !found {
		_, err = s.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, body)
		return actionCreated, err
	}
	if head, runID, attempt, ok := parseHead(existing.Body); ok && head == t.HeadSHA && newerRun(runID, attempt, receipt) {
		return actionSkippedNewer, nil
	}
	if body == existing.Body {
		return actionUnchanged, nil
	}
	return actionUpdated, s.comments.UpdateComment(ctx, t.Repo.RepoRef, existing.ID, body)
}
