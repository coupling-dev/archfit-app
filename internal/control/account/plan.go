package account

import (
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
)

// [default] values, expected to change before launch.
const (
	trialLength = 30 * 24 * time.Hour
	graceLength = 7 * 24 * time.Hour
	warnBefore  = 7 * 24 * time.Hour
	lastDay     = 24 * time.Hour

	// [default: placeholder] must be set before launch.
	supportContact = "support@archfit.example"

	reason = "Archfit subscription inactive — contact " + supportContact +
		" or remove the Archfit requirement in the ruleset"

	dateLayout = "2006-01-02"
)

// record is the private per-installation state; times are UTC.
type record struct {
	FirstPrivateAt time.Time `json:"first_private_at,omitzero"`
	GrantUntil     time.Time `json:"grant_until,omitzero"`
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (r record) status(now time.Time) Status {
	st := Status{Plan: PlanPublic, GrantUntil: r.GrantUntil}
	if r.FirstPrivateAt.IsZero() {
		return st
	}
	st.TrialEnds = r.FirstPrivateAt.Add(trialLength)
	st.SuspendAfter = latest(st.TrialEnds.Add(graceLength), r.GrantUntil)
	switch {
	case now.Before(r.GrantUntil):
		st.Plan = PlanGranted
	case now.Before(st.TrialEnds):
		st.Plan = PlanTrial
	case now.Before(st.SuspendAfter):
		st.Plan = PlanGrace
	default:
		st.Plan = PlanSuspended
	}
	return st
}

func (r record) entitlement(now time.Time) gate.Entitlement {
	st := r.status(now)
	if st.Plan == PlanPublic {
		return gate.Entitlement{}
	}
	return gate.Entitlement{
		SuspendAfter: st.SuspendAfter,
		WarnFrom:     latest(st.TrialEnds, st.GrantUntil).Add(-warnBefore),
		Notice:       notice(st, now),
		Reason:       reason,
	}
}

func notice(st Status, now time.Time) string {
	plan := st.Plan
	if plan == PlanGranted && st.SuspendAfter.After(st.GrantUntil) {
		// The grant ends before the grace period does: the trial timeline is the one that matters.
		plan = PlanGrace
		if now.Before(st.TrialEnds) {
			plan = PlanTrial
		}
	}
	switch plan {
	case PlanTrial:
		return "Trial ends " + st.TrialEnds.Format(dateLayout) + lastDaySuffix(st.TrialEnds, now)
	case PlanGranted:
		return "Archfit beta access ends " + st.GrantUntil.Format(dateLayout) + lastDaySuffix(st.GrantUntil, now)
	case PlanGrace:
		return "Trial ended " + st.TrialEnds.Format(dateLayout) +
			". Archfit checks suspend on " + st.SuspendAfter.Format(dateLayout)
	case PlanSuspended:
		return "Archfit subscription inactive since " + st.SuspendAfter.Format(dateLayout)
	}
	return ""
}

func lastDaySuffix(end, now time.Time) string {
	if end.Sub(now) <= lastDay {
		return " — 1 day left"
	}
	return ""
}
