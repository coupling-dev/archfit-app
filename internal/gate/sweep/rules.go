package sweep

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const runCompleted = "completed"

// RerunResult is what the re-run guard found or did. It is meaningful only with a nil error.
type RerunResult int

const (
	RerunInFlight   RerunResult = iota // the run, or a newer run of the head, is queued or in progress: wait for its upload
	RerunRequested                     // Actions.Rerun accepted the request
	RerunImpossible                    // a condition of design 10.5 does not hold (Unmet names it)
)

// Unmet names the condition of design 10.5 that kept the guard from requesting a re-run; "" when none did.
type Unmet string

const (
	UnmetNone             Unmet = ""
	UnmetNoRun            Unmet = "no_run"              // no run is known, or GitHub no longer has it
	UnmetNotLatest        Unmet = "not_latest"          // not the latest run of the current head, or a newer attempt completed
	UnmetPolicyNotFromTip Unmet = "policy_not_from_tip" // the run's workflow does not read the policy from the target tip
	UnmetTooOld           Unmet = "too_old"             // the run is 30 days old or older: GitHub re-runs none
	UnmetBaseChanged      Unmet = "base_changed"        // the target changed in more than the policy since the event's base, or that cannot be read
)

// policyFile is the only path a target may change in for a re-run to measure the same code (design 10.5).
const policyFile = ".archfit.yaml"

// rerunMaxAge is GitHub's limit: a run can be re-run up to 30 days after it was created; a re-run keeps that time.
const rerunMaxAge = 30 * 24 * time.Hour

// The compare statuses that put the target tip at or after the event's base.
const (
	compareIdentical = "identical"
	compareAhead     = "ahead"
)

// Runs is what the re-run guard reads and writes on GitHub.
type Runs interface {
	api.Actions
	api.WorkflowRuns
	api.Commits
}

// RerunInput is what the guard judges the four conditions of design 10.5 on, beside the run the receipt names.
type RerunInput struct {
	Receipt   feedback.Receipt // RunID and RunAttempt name the run to re-run
	Head      string           // the head of the pull request (or merge group) now
	EventBase string           // the base of the event the run measured; "" when unknown
	Tip       string           // the default branch's head now; "" when unknown
	ReadsTip  bool             // the run's workflow reads the policy from the target tip, as the caller knows it
}

// Rerun is the re-run guard, the only caller of Actions.Rerun on the gate path. A re-run replays the run's event: the
// same commit, the same base, a fetch of the target tip at run time (design D.2). So it requests one only when all
// four conditions of design 10.5 hold: the workflow reads the policy from the target tip; the run is the latest run of
// the current head, still completed at the receipt's attempt (so a receipt is re-run at most once); it is younger than
// 30 days; and the target changed only in the policy since the event's base. Then the code measured is the same and
// only the policy is new. The first condition that fails is returned with RerunImpossible; a run of the head that is
// queued or in progress is RerunInFlight. A failed read or request is returned with RerunInFlight.
func Rerun(ctx context.Context, gh Runs, r api.RepoRef, in RerunInput, now time.Time) (RerunResult, Unmet, error) {
	rc := in.Receipt
	if rc.RunID == 0 {
		return RerunImpossible, UnmetNoRun, nil
	}
	run, err := gh.Run(ctx, r, rc.RunID)
	if errors.Is(err, api.ErrNotFound) {
		return RerunImpossible, UnmetNoRun, nil
	}
	if err != nil {
		return RerunInFlight, UnmetNone, err
	}
	if run.Status != runCompleted {
		return RerunInFlight, UnmetNone, nil
	}
	if run.Attempt != rc.RunAttempt {
		return RerunImpossible, UnmetNotLatest, nil
	}
	if !in.ReadsTip {
		return RerunImpossible, UnmetPolicyNotFromTip, nil
	}
	if run.HeadSHA == "" || run.HeadSHA != in.Head {
		return RerunImpossible, UnmetNotLatest, nil
	}
	res, unmet, err := latest(ctx, gh, r, run)
	if err != nil || res != RerunRequested {
		return res, unmet, err
	}
	if now.Sub(run.CreatedAt) >= rerunMaxAge {
		return RerunImpossible, UnmetTooOld, nil
	}
	if only, err := onlyPolicy(ctx, gh, r, in.EventBase, in.Tip); err != nil || !only {
		if err != nil {
			return RerunInFlight, UnmetNone, err
		}
		return RerunImpossible, UnmetBaseChanged, nil
	}
	if err := gh.Rerun(ctx, r, rc.RunID); err != nil {
		return RerunInFlight, UnmetNone, err
	}
	return RerunRequested, UnmetNone, nil
}

// latest checks that run is the latest run of its workflow for its head, among the runs of the same event that GitHub
// ties to the same pull request or to none, ordered as ingest orders them: creation time (a re-run keeps it), then id.
// RerunRequested means it is; RerunInFlight means the latest run is still queued or in progress.
func latest(ctx context.Context, gh api.WorkflowRuns, r api.RepoRef, run api.WorkflowRun) (RerunResult, Unmet, error) {
	runs, err := gh.HeadRuns(ctx, r, run.WorkflowPath, run.HeadSHA)
	if errors.Is(err, api.ErrNotFound) {
		return RerunImpossible, UnmetNoRun, nil
	}
	if err != nil {
		return RerunInFlight, UnmetNone, err
	}
	newest := run
	for _, other := range runs {
		if other.ID == run.ID || other.Event != run.Event || !samePull(run.PullRequests, other.PullRequests) {
			continue
		}
		if newer(other, newest) {
			newest = other
		}
	}
	switch {
	case newest.ID == run.ID:
		return RerunRequested, UnmetNone, nil
	case newest.Status != runCompleted:
		return RerunInFlight, UnmetNone, nil // the latest run reads the tip itself; its upload decides
	}
	return RerunImpossible, UnmetNotLatest, nil
}

// newer reports whether a was created after b, or at the same time with a higher id, as ingest orders runs.
func newer(a, b api.WorkflowRun) bool {
	return a.CreatedAt.After(b.CreatedAt) || a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID
}

// samePull reports whether a run tied to the pull requests b counts against a run tied to a: b names none, or one of a.
func samePull(a, b []int) bool {
	return len(b) == 0 || slices.ContainsFunc(b, func(n int) bool { return slices.Contains(a, n) })
}

// onlyPolicy reports whether the target changed only in the policy from base to tip. An unknown base or tip, a base
// GitHub cannot compare, a tip that is not at or after base, and a cut file list are not.
func onlyPolicy(ctx context.Context, gh api.Commits, r api.RepoRef, base, tip string) (bool, error) {
	if base == "" || tip == "" {
		return false, nil
	}
	if base == tip {
		return true, nil
	}
	c, err := gh.Compare(ctx, r, base, tip)
	if api.Permanent(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if c.Truncated || (c.Status != compareAhead && c.Status != compareIdentical) {
		return false, nil
	}
	for _, path := range c.Paths {
		if path != policyFile {
			return false, nil
		}
	}
	return true, nil
}
