package sweep

import (
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
		if err != nil || res != rerunImpossible {
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
		digestStale(tr.PolicyDigests, rc.PolicyDigest) ||
		digestStale(tr.BaselineDigests, rc.BaselineDigest) ||
		digestStale(tr.LabelsDigests, rc.LabelsDigest)
}

// digestStale treats "nothing trusted, nothing claimed" as fresh: that keeps the no-policy window quiet.
func digestStale(trusted []string, claimed string) bool {
	if len(trusted) == 0 {
		return claimed != ""
	}
	return !slices.Contains(trusted, claimed)
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

// without builds the publication for a target that has no report. Keeping the previous run identity
// keeps the check's external_id, so feedback updates in place; the current digests and the new reason
// are what makes the next pass find nothing to do.
func without(code verdict.ReasonCode, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt) feedback.Publication {
	out := verdict.Without(code)
	return feedback.Publication{
		Target:  t,
		Outcome: out,
		Receipt: feedback.Receipt{
			RunID:                rc.RunID,
			RunAttempt:           rc.RunAttempt,
			ReportDigest:         rc.ReportDigest,
			PolicyDigest:         first(tr.PolicyDigests),
			BaselineDigest:       first(tr.BaselineDigests),
			LabelsDigest:         first(tr.LabelsDigests),
			EarliestWaiverExpiry: tr.EarliestWaiverExpiry,
			Conclusion:           out.Conclusion,
			Reason:               code,
		},
		Trusted: tr,
	}
}

func first(digests []string) string {
	if len(digests) == 0 {
		return ""
	}
	return digests[0]
}

func (p *pass) publish(ctx context.Context, rule string, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt, code verdict.ReasonCode) error {
	pub := without(code, t, tr, rc)
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

type rerunResult int

const (
	rerunInFlight   rerunResult = iota // queued or in progress: wait for its upload
	rerunRequested                     // Actions.Rerun accepted the request
	rerunImpossible                    // no run, run gone, or a newer attempt completed without an upload
)

// rerun is the guard that keeps re-runs to one per receipt: GitHub advances the attempt on every
// accepted request, so a receipt's (run, attempt) can be re-run only while it is still the latest.
func (p *pass) rerun(ctx context.Context, rule string, t inputs.Target, rc feedback.Receipt) (rerunResult, error) {
	if rc.RunID == 0 {
		return rerunImpossible, nil
	}
	run, err := p.actions.Run(ctx, t.Repo.RepoRef, rc.RunID)
	if errors.Is(err, api.ErrNotFound) {
		return rerunImpossible, nil
	}
	if err != nil {
		return rerunInFlight, err
	}
	if run.Status != runCompleted {
		return rerunInFlight, nil
	}
	if run.Attempt != rc.RunAttempt {
		return rerunImpossible, nil
	}
	if err := p.actions.Rerun(ctx, t.Repo.RepoRef, rc.RunID); err != nil {
		return rerunInFlight, err
	}
	p.sum.Rerun++
	p.logAction(t, rule, "rerun", "run_id", rc.RunID, "run_attempt", rc.RunAttempt)
	return rerunRequested, nil
}

func (p *pass) logAction(t inputs.Target, rule, action string, attrs ...any) {
	slog.Info("sweep: repaired", append(append(targetAttrs(t), "rule", rule, "action", action), attrs...)...)
}
