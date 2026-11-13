package sweep

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const runCompleted = "completed"

// evaluate applies the rule table of module.md to one onboarded pull. The first rule whose condition
// holds ends the evaluation, whether or not it had to write; only rule 6a lets the evaluation go on.
func (p *pass) evaluate(ctx context.Context, t inputs.Target, tr inputs.Trusted, shared []verdict.SharedPull) error {
	if tr.Settings.Gate != settings.GateRequired { // rule 0
		return p.advisory(ctx, t, tr)
	}
	state, err := p.publisher.State(ctx, t)
	if err != nil {
		return err
	}
	rc := state.Receipt

	switch {
	case t.Fork:
		if rc.Reason == verdict.ReasonForkUnsupported {
			return nil
		}
		return p.publish(ctx, "1", t, tr, rc, verdict.ReasonForkUnsupported)
	case tr.Suspended:
		p.sum.Suspended++
		if rc.Reason == verdict.ReasonSuspended {
			return nil
		}
		return p.publish(ctx, "2", t, tr, rc, verdict.ReasonSuspended)
	case !state.Exists:
		if err := p.publisher.Pending(ctx, t); err != nil {
			return err
		}
		p.sum.Pended++
		p.logAction(t, "3", "pending")
		return nil
	case state.Pending:
		if p.now.Sub(state.StartedAt) < p.cfg.MissingUploadAfter {
			return nil
		}
		if err := p.publish(ctx, "4", t, tr, rc, verdict.ReasonMissingUpload); err != nil {
			return err
		}
		p.sum.TimedOut++
		return nil
	case Withdrawn(rc, tr):
		// No re-run: no run can pass while the pull request changes a protected path; rule 7b re-runs once it does not.
		return p.withdraw(ctx, t)
	case stale(rc, tr):
		if moved, err := p.retarget(ctx, "5b", t, rc, true); err != nil || moved {
			return err
		}
		if err := p.publish(ctx, "5b", t, tr, rc, verdict.ReasonStaleInputs); err != nil {
			return err
		}
		_, err := p.rerun(ctx, "5b", t, rc)
		return err
	}
	if rc.Conclusion == verdict.Success && len(shared) > 0 { // rule 5d, ahead of 5c: both withdraw the same success
		return p.sharedHead(ctx, t, rc)
	}
	if moved, err := p.targetMoved(ctx, t, tr, rc); err != nil || moved { // rule 5c
		return err
	}

	e := expiry(rc, tr)
	if err := p.warn(ctx, t, e); err != nil { // rule 6a
		return err
	}
	restored := rc.Reason == verdict.ReasonSuspended // rule 2 already excluded Trusted.Suspended
	// The pull request no longer changes a protected path (a retargeted base, settings that protect other paths).
	cleared := rc.Reason == verdict.ReasonOwnerApprovalMissing && !verdict.Unapproved(tr)
	switch {
	case !e.IsZero() && !p.now.Before(e) && state.StartedAt.Before(e) && rc.Reason != verdict.ReasonTargetMoved:
		if moved, err := p.retarget(ctx, "6b", t, rc, false); err != nil || moved {
			return err
		}
		if err := p.publish(ctx, "6b", t, tr, rc, verdict.ReasonWaiverExpired); err != nil {
			return err
		}
		_, err := p.rerun(ctx, "6b", t, rc)
		return err
	case restored, cleared:
		rule := "7a"
		if cleared {
			rule = "7b"
		}
		if moved, err := p.retarget(ctx, rule, t, rc, false); err != nil || moved {
			return err
		}
		res, err := p.rerun(ctx, rule, t, rc)
		if err != nil || res != RerunImpossible {
			return err
		}
		// Only a new workflow run can bring evidence now; missing_upload is true and names that recovery.
		return p.publish(ctx, rule, t, tr, rc, verdict.ReasonMissingUpload)
	case rc.Reason == verdict.ReasonStaleInputs || rc.Reason == verdict.ReasonWaiverExpired:
		if moved, err := p.retarget(ctx, "7c", t, rc, false); err != nil || moved {
			return err
		}
		_, err := p.rerun(ctx, "7c", t, rc)
		return err
	}
	return nil
}

// Decided reports whether rc is a decision on the report: success, failure, or an action_required whose reason comes
// from the report (missing required coverage, a baseline that is not comparable, a policy defect). The report is shown
// under such a result, and a protected change withdraws it as it withdraws a success. Any other
// action_required result, and a pending, zero or unreadable receipt, already says what to do and is not decided.
func Decided(rc feedback.Receipt) bool {
	return rc.Conclusion == verdict.Success || rc.Conclusion == verdict.Failure ||
		(rc.Conclusion == verdict.ActionRequired && verdict.ReportDerivedAction(rc.Reason))
}

// Withdrawn reports whether rc is Decided although tr now changes a protected path, which nothing approves: the
// protected settings came to protect a path the pull request changes.
func Withdrawn(rc feedback.Receipt, tr inputs.Trusted) bool {
	return Decided(rc) && verdict.Unapproved(tr)
}

// stale reports whether the receipt was computed from inputs that are no longer on the protected ref,
// or cannot be read at all (a readable receipt always has a reason).
func stale(rc feedback.Receipt, tr inputs.Trusted) bool {
	return rc.Reason == "" ||
		!inputs.Accepts(tr.PolicyDigests, rc.PolicyDigest) ||
		!inputs.Accepts(tr.BaselineDigests, rc.BaselineDigest) ||
		!inputs.Accepts(tr.LabelsDigests, rc.LabelsDigest)
}

// expiry is the waiver expiry the receipt was evaluated against, or the trusted one when it has none.
func expiry(rc feedback.Receipt, tr inputs.Trusted) time.Time {
	if !rc.EarliestWaiverExpiry.IsZero() {
		return rc.EarliestWaiverExpiry
	}
	return tr.EarliestWaiverExpiry
}

// advisory is rule 0. An advisory pull request has no archfit/gate, so its receipt is read from archfit/state. When
// the protected inputs changed after it, the old result would stay on the pull request with no sign that it is out of
// date, so the receipt is withdrawn as stale_inputs and the run re-runs, as rules 5b and 7c do in required mode.
// A fork or suspension refusal is left alone, as rules 1 and 2 keep it ahead of rule 5b in required mode: a re-run
// can only refuse again. Nothing is pended and no missing upload is published: advisory feedback never blocks, so a
// pull request without a report stays as it is. Rule 6a runs last whatever happened, as it does in required mode.
func (p *pass) advisory(ctx context.Context, t inputs.Target, tr inputs.Trusted) error {
	state, err := p.publisher.AdvisoryState(ctx, t)
	if err != nil {
		return err
	}
	rc := state.Receipt
	refused := t.Fork || tr.Suspended || rc.Reason == verdict.ReasonForkUnsupported || rc.Reason == verdict.ReasonSuspended
	switch {
	case refused:
	case state.Exists && stale(rc, tr):
		if err := p.publish(ctx, "0b", t, tr, rc, verdict.ReasonStaleInputs); err != nil {
			return err
		}
		if _, err := p.rerun(ctx, "0b", t, rc); err != nil {
			return err
		}
	case rc.Reason == verdict.ReasonStaleInputs:
		if _, err := p.rerun(ctx, "0c", t, rc); err != nil {
			return err
		}
	}
	return p.warn(ctx, t, tr.EarliestWaiverExpiry)
}

// warn is rule 6a. It never ends an evaluation.
func (p *pass) warn(ctx context.Context, t inputs.Target, e time.Time) error {
	if e.IsZero() || !p.now.Before(e) || e.Sub(p.now) > p.cfg.WaiverWarnBefore {
		return nil
	}
	if err := p.publisher.Warn(ctx, t, e); err != nil {
		return err
	}
	p.sum.Warned++
	p.logAction(t, "6a", "warn", "expires_in", e.Sub(p.now).String())
	return nil
}

// Without builds the publication for a target that has no report: verdict.Without(code),
// verdict.Suspended(tr.Entitlement) for ReasonSuspended, or verdict.ApprovalMissing(tr) for
// ReasonOwnerApprovalMissing, with the run identity and base of rc (zero when there is no previous receipt) and the
// current digests of tr.
//
// Keeping the previous run identity keeps the check's external_id, so feedback updates the gate check in
// place; the current digests and the new reason are what makes the next pass find nothing to do.
func Without(code verdict.ReasonCode, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt) feedback.Publication {
	out := verdict.Without(code)
	switch code {
	case verdict.ReasonSuspended:
		out = verdict.Suspended(tr.Entitlement)
	case verdict.ReasonOwnerApprovalMissing:
		out = verdict.ApprovalMissing(tr)
	}
	return withOutcome(out, code, t, tr, rc)
}

// withOutcome is the publication of a target that has no new report: out, over the run identity and base of rc and
// the current digests of tr.
func withOutcome(out verdict.Outcome, code verdict.ReasonCode, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt) feedback.Publication {
	return feedback.Publication{
		Target:  t,
		Outcome: out,
		Receipt: feedback.Receipt{
			RunID:                rc.RunID,
			RunAttempt:           rc.RunAttempt,
			ReportDigest:         rc.ReportDigest,
			BaseSHA:              rc.BaseSHA,
			PolicyDigest:         inputs.First(tr.PolicyDigests),
			BaselineDigest:       inputs.First(tr.BaselineDigests),
			LabelsDigest:         inputs.First(tr.LabelsDigests),
			EarliestWaiverExpiry: tr.EarliestWaiverExpiry,
			Conclusion:           out.Conclusion,
			Reason:               code,
		},
		Trusted: tr,
	}
}

// Withdrawal is rule 5a, decided inside the head's publication lease (feedback.Publisher.Reconcile) from the gate
// check current there and inputs tr resolved there: Without(ReasonOwnerApprovalMissing) over the current receipt
// when a check exists and its receipt is Withdrawn, in either gate mode, else nil. Deciding inside the lease is what
// makes it safe: a newer run that turned green while the caller waited is the receipt it replaces, and settings that
// stopped protecting the path meanwhile leave the check as it is.
func Withdrawal(t inputs.Target, current feedback.CheckState, tr inputs.Trusted) *feedback.Publication {
	if !current.Exists || !Withdrawn(current.Receipt, tr) {
		return nil
	}
	pub := Without(verdict.ReasonOwnerApprovalMissing, t, tr, current.Receipt)
	return &pub
}

// GroupWithdrawal is the correction of a merge group's required check when its trusted inputs tr, read now, block it
// (verdict.GroupBlocked): the group was destroyed or rebuilt, a member changed, or a member changes a protected path.
// Only a pending check or a decided result is replaced; one that already asks for action stays. nil writes nothing.
func GroupWithdrawal(t inputs.Target, current feedback.CheckState, tr inputs.Trusted) *feedback.Publication {
	if tr.Settings.Gate != settings.GateRequired || !current.Exists || (!current.Pending && !Decided(current.Receipt)) {
		return nil
	}
	out, blocked := verdict.GroupBlocked(tr)
	if !blocked {
		return nil
	}
	pub := withOutcome(out, out.Reasons[0].Code, t, tr, current.Receipt)
	return &pub
}

// recheckPull is rule 5c alone, for the pass a default-branch push starts. A pull request that another rule acts on
// (a fork, a suspension, a withdrawn result, stale inputs, and with them a missing, pending or unreadable check) is
// left to the full pass: target_moved must not replace the reason that rule publishes, nor its re-run.
func (p *pass) recheckPull(ctx context.Context, t inputs.Target, tr inputs.Trusted) error {
	if tr.Settings.Gate != settings.GateRequired || t.Fork || tr.Suspended {
		return nil
	}
	state, err := p.publisher.State(ctx, t)
	if err != nil {
		return err
	}
	if Withdrawn(state.Receipt, tr) || stale(state.Receipt, tr) { // stale holds for the zero receipt of a missing or pending check
		return nil
	}
	_, err = p.targetMoved(ctx, t, tr, state.Receipt)
	return err
}

// SharedWith lists the open pull requests of pulls, other than number, whose head is head and whose base is not the
// default branch, by number. A check is written per head commit, so each of them shows the result of the pull request
// into the default branch, on a base whose protected inputs the agent that opened it may have loosened.
func SharedWith(pulls []api.Pull, number int, head, defaultBranch string) []verdict.SharedPull {
	var out []verdict.SharedPull
	for _, pl := range pulls {
		if pl.Number != number && pl.HeadSHA == head && pl.BaseRef != defaultBranch {
			out = append(out, verdict.SharedPull{Number: pl.Number, BaseRef: pl.BaseRef})
		}
	}
	slices.SortFunc(out, func(a, b verdict.SharedPull) int { return cmp.Compare(a.Number, b.Number) })
	return out
}

// sharedHead is rule 5d: shared_head over a success receipt, no re-run, decided inside the head's publication lease
// by SharedHeadPublication. Only a success is withdrawn here; a pending check is the upload's to refuse (it lists the
// open pull requests inside the same lease) or the event's to withdraw (gate: PullOpened into another base). A failed
// listing is an error of the unit, so the success is never touched on a guess.
func (p *pass) sharedHead(ctx context.Context, t inputs.Target, rc feedback.Receipt) error {
	var pub *feedback.Publication
	err := p.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		if current.Receipt != rc {
			return nil, nil
		}
		var err error
		pub, err = SharedHeadPublication(ctx, p.pulls, p.resolver, p.now, t, current)
		return pub, err
	})
	if err != nil || pub == nil {
		return err
	}
	p.logPublished("5d", *pub, rc)
	return nil
}

// SharedHeadPublication is the shared-head decision of a default-branch pull request t, made inside its head's
// publication lease from the gate check current there: shared_head over the current receipt (the zero receipt of a
// pending check, which any upload replaces) when the repository is in required mode, the check is Pending or a success
// whose inputs still hold, and a fresh OpenPulls listing still names a pull request on the head with
// another base. nil otherwise. A pending check is written only to refuse the upload that is about to decide it: that
// upload lists inside the lease too, so it sees this publication or is seen by it.
func SharedHeadPublication(ctx context.Context, pulls api.Pulls, res inputs.Resolver, now time.Time, t inputs.Target, current feedback.CheckState) (*feedback.Publication, error) {
	rc := current.Receipt
	if !current.Exists || (!current.Pending && rc.Conclusion != verdict.Success) {
		return nil, nil
	}
	tr, err := res.Resolve(ctx, t, now)
	if err != nil {
		return nil, err
	}
	// A pull request's withdrawn or stale success is left to rules 5a and 5b. The sweep never visits a merge group, so
	// its success becomes shared_head whatever else is wrong with it.
	if tr.Settings.Gate != settings.GateRequired || (!current.Pending && !t.MergeGroup && (Withdrawn(rc, tr) || stale(rc, tr))) {
		return nil, nil
	}
	open, err := pulls.OpenPulls(ctx, t.Repo.RepoRef)
	if err != nil {
		return nil, err
	}
	shared := SharedWith(open, t.PullRequest, t.HeadSHA, t.Repo.DefaultBranch)
	if len(shared) == 0 {
		return nil, nil
	}
	pub := withOutcome(verdict.SharedHead(shared), verdict.ReasonSharedHead, t, tr, rc)
	return &pub, nil
}

// withdraw is rule 5a. The pass saw a withdrawn receipt outside the lease; Reconcile decides again inside it.
func (p *pass) withdraw(ctx context.Context, t inputs.Target) error {
	var pub *feedback.Publication
	var was feedback.Receipt
	err := p.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		tr, err := p.resolver.Resolve(ctx, t, p.now)
		if err != nil {
			return nil, err
		}
		pub, was = Withdrawal(t, current, tr), current.Receipt
		return pub, nil
	})
	if err != nil || pub == nil {
		return err
	}
	p.logPublished("5a", *pub, was)
	return nil
}

func (p *pass) publish(ctx context.Context, rule string, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt, code verdict.ReasonCode) error {
	pub := Without(code, t, tr, rc)
	if err := p.publisher.Publish(ctx, pub); err != nil {
		return err
	}
	p.logPublished(rule, pub, rc)
	return nil
}

func (p *pass) logPublished(rule string, pub feedback.Publication, rc feedback.Receipt) {
	p.logAction(pub.Target, rule, "publish", "reason", string(pub.Receipt.Reason),
		"run_id", pub.Receipt.RunID, "run_attempt", pub.Receipt.RunAttempt,
		"policy_digest", pub.Receipt.PolicyDigest, "baseline_digest", pub.Receipt.BaselineDigest,
		"labels_digest", pub.Receipt.LabelsDigest,
		"was_policy_digest", rc.PolicyDigest, "was_baseline_digest", rc.BaselineDigest,
		"was_labels_digest", rc.LabelsDigest)
}

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

// rerun counts and logs what Rerun requested. The pass's receipts are Archfit 3's: the event's base is the base the
// receipt's run measured, else the pull request's base, and the Archfit 3 Action reads the protected inputs from the
// target tip (design D.2: run.sh input_tip). A tip that cannot be read is an error, as any failed read of the guard:
// the pull counts in Errors and the next pass tries again. A branch GitHub does not have is an unknown tip.
func (p *pass) rerun(ctx context.Context, rule string, t inputs.Target, rc feedback.Receipt) (RerunResult, error) {
	in := RerunInput{Receipt: rc, Head: t.HeadSHA, EventBase: rc.BaseSHA, ReadsTip: true}
	if in.EventBase == "" {
		in.EventBase = p.bases[pullRef{t.Repo.RepoRef, t.PullRequest}]
	}
	if rc.RunID != 0 {
		tip, err := p.tip(ctx, t.Repo)
		if err != nil {
			return RerunInFlight, err
		}
		in.Tip = tip.sha
	}
	res, unmet, err := Rerun(ctx, p.actions, t.Repo.RepoRef, in, p.now)
	switch {
	case err == nil && res == RerunRequested:
		p.sum.Rerun++
		p.logAction(t, rule, "rerun", "run_id", rc.RunID, "run_attempt", rc.RunAttempt)
	case err == nil && unmet != UnmetNone && rc.RunID != 0:
		slog.Info("sweep: no re-run", append(targetAttrs(t), "rule", rule, "run_id", rc.RunID, "unmet", string(unmet))...)
	}
	return res, err
}

func (p *pass) logAction(t inputs.Target, rule, action string, attrs ...any) {
	slog.Info("sweep: repaired", append(append(targetAttrs(t), "rule", rule, "action", action), attrs...)...)
}
