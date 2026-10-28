package sweep

import (
	"context"
	"errors"
	"log/slog"
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
func (p *pass) evaluate(ctx context.Context, t inputs.Target, tr inputs.Trusted) error {
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
		// No re-run: no run can pass before an owner approves again, and rule 7b re-runs then.
		return p.withdraw(ctx, t)
	case stale(rc, tr):
		if err := p.publish(ctx, "5b", t, tr, rc, verdict.ReasonStaleInputs); err != nil {
			return err
		}
		_, err := p.rerun(ctx, "5b", t, rc)
		return err
	}
	if moved, err := p.targetMoved(ctx, t, tr, rc); err != nil || moved { // rule 5c
		return err
	}

	e := expiry(rc, tr)
	if err := p.warn(ctx, t, e); err != nil { // rule 6a
		return err
	}
	restored := rc.Reason == verdict.ReasonSuspended // rule 2 already excluded Trusted.Suspended
	// Granted, or no longer needed (a retargeted base, a repointed settings workflow): either way the reason is gone.
	approvedLate := rc.Reason == verdict.ReasonOwnerApprovalMissing && !verdict.Unapproved(tr)
	switch {
	case !e.IsZero() && !p.now.Before(e) && state.StartedAt.Before(e) && rc.Reason != verdict.ReasonTargetMoved:
		if err := p.publish(ctx, "6b", t, tr, rc, verdict.ReasonWaiverExpired); err != nil {
			return err
		}
		_, err := p.rerun(ctx, "6b", t, rc)
		return err
	case restored, approvedLate:
		rule := "7a"
		if approvedLate {
			rule = "7b"
		}
		res, err := p.rerun(ctx, rule, t, rc)
		if err != nil || res != RerunImpossible {
			return err
		}
		// Only a new workflow run can bring evidence now; missing_upload is true and names that recovery.
		return p.publish(ctx, rule, t, tr, rc, verdict.ReasonMissingUpload)
	case rc.Reason == verdict.ReasonStaleInputs || rc.Reason == verdict.ReasonWaiverExpired:
		_, err := p.rerun(ctx, "7c", t, rc)
		return err
	}
	return nil
}

// Decided reports whether rc is a decision on the report: success, failure, or an action_required whose reason comes
// from the report (missing required coverage, a baseline that is not comparable, a policy defect). The report is shown
// under such a result, and the owner approval that admitted the head's inputs matters to it as to success. Any other
// action_required result, and a pending, zero or unreadable receipt, already says what to do and is not decided.
func Decided(rc feedback.Receipt) bool {
	return rc.Conclusion == verdict.Success || rc.Conclusion == verdict.Failure ||
		(rc.Conclusion == verdict.ActionRequired && verdict.ReportDerivedAction(rc.Reason))
}

// Withdrawn reports whether rc is Decided although tr now lacks the owner approval that verdict.Decide demands: the
// approval it relied on was dismissed or followed by a request for changes, the approver left the owners of the
// protected settings, or the pull now changes a protected path.
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
// when the repository is in required mode and that receipt is Withdrawn, else nil. Deciding inside the lease is what
// makes it safe: a newer run that turned green while the caller waited is the receipt it replaces, and an owner who
// approved again meanwhile leaves the check as it is.
func Withdrawal(t inputs.Target, current feedback.CheckState, tr inputs.Trusted) *feedback.Publication {
	if tr.Settings.Gate != settings.GateRequired || !Withdrawn(current.Receipt, tr) {
		return nil
	}
	pub := Without(verdict.ReasonOwnerApprovalMissing, t, tr, current.Receipt)
	return &pub
}

// recheckPull is rule 5c alone, for the pass a default-branch push starts. A pull request that another rule acts on
// (a fork, a suspension, a withdrawn approval, stale inputs, and with them a missing, pending or unreadable check) is
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
	RerunInFlight   RerunResult = iota // the run is queued or in progress: wait for its upload
	RerunRequested                     // Actions.Rerun accepted the request
	RerunImpossible                    // rc.RunID is 0, the run is gone, or a newer attempt already completed
)

// Rerun is the re-run guard, the only caller of Actions.Rerun on the gate path. It requests a re-run of
// rc.RunID only while that run is completed and still at rc.RunAttempt, so a receipt is re-run at most once.
// rc.RunID == 0 and ErrNotFound from Actions.Run are RerunImpossible with a nil error.
func Rerun(ctx context.Context, actions api.Actions, r api.RepoRef, rc feedback.Receipt) (RerunResult, error) {
	if rc.RunID == 0 {
		return RerunImpossible, nil
	}
	run, err := actions.Run(ctx, r, rc.RunID)
	if errors.Is(err, api.ErrNotFound) {
		return RerunImpossible, nil
	}
	if err != nil {
		return RerunInFlight, err
	}
	if run.Status != runCompleted {
		return RerunInFlight, nil
	}
	if run.Attempt != rc.RunAttempt {
		return RerunImpossible, nil
	}
	if err := actions.Rerun(ctx, r, rc.RunID); err != nil {
		return RerunInFlight, err
	}
	return RerunRequested, nil
}

// rerun counts and logs what Rerun requested.
func (p *pass) rerun(ctx context.Context, rule string, t inputs.Target, rc feedback.Receipt) (RerunResult, error) {
	res, err := Rerun(ctx, p.actions, t.Repo.RepoRef, rc)
	if err == nil && res == RerunRequested {
		p.sum.Rerun++
		p.logAction(t, rule, "rerun", "run_id", rc.RunID, "run_attempt", rc.RunAttempt)
	}
	return res, err
}

func (p *pass) logAction(t inputs.Target, rule, action string, attrs ...any) {
	slog.Info("sweep: repaired", append(append(targetAttrs(t), "rule", rule, "action", action), attrs...)...)
}
