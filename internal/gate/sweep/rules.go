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
		return p.warn(ctx, t, tr.EarliestWaiverExpiry)
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
	case stale(rc, tr):
		if err := p.publish(ctx, "5", t, tr, rc, verdict.ReasonStaleInputs); err != nil {
			return err
		}
		_, err := p.rerun(ctx, "5", t, rc)
		return err
	}

	e := expiry(rc, tr)
	if err := p.warn(ctx, t, e); err != nil { // rule 6a
		return err
	}
	restored := rc.Reason == verdict.ReasonSuspended // rule 2 already excluded Trusted.Suspended
	approvedLate := rc.Reason == verdict.ReasonOwnerApprovalMissing && tr.Approval == inputs.ApprovalGranted
	switch {
	case !e.IsZero() && !p.now.Before(e) && state.StartedAt.Before(e):
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

// Without builds the publication for a target that has no report: verdict.Without(code), or
// verdict.Suspended(tr.Entitlement) for ReasonSuspended, with the run identity of rc (zero when there
// is no previous receipt) and the current digests of tr.
//
// Keeping the previous run identity keeps the check's external_id, so feedback updates the gate check in
// place; the current digests and the new reason are what makes the next pass find nothing to do.
func Without(code verdict.ReasonCode, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt) feedback.Publication {
	out := verdict.Without(code)
	if code == verdict.ReasonSuspended {
		out = verdict.Suspended(tr.Entitlement)
	}
	return feedback.Publication{
		Target:  t,
		Outcome: out,
		Receipt: feedback.Receipt{
			RunID:                rc.RunID,
			RunAttempt:           rc.RunAttempt,
			ReportDigest:         rc.ReportDigest,
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

func (p *pass) publish(ctx context.Context, rule string, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt, code verdict.ReasonCode) error {
	pub := Without(code, t, tr, rc)
	if err := p.publisher.Publish(ctx, pub); err != nil {
		return err
	}
	p.logAction(t, rule, "publish", "reason", string(code),
		"run_id", pub.Receipt.RunID, "run_attempt", pub.Receipt.RunAttempt,
		"policy_digest", pub.Receipt.PolicyDigest, "baseline_digest", pub.Receipt.BaselineDigest,
		"labels_digest", pub.Receipt.LabelsDigest,
		"was_policy_digest", rc.PolicyDigest, "was_baseline_digest", rc.BaselineDigest,
		"was_labels_digest", rc.LabelsDigest)
	return nil
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
