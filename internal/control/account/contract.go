// Package account owns subscription status and consents.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/store"
)

type Plan string

const (
	PlanPublic    Plan = "public"  // no private repositories: free
	PlanTrial     Plan = "trial"   // 30 days from the first private-repository installation
	PlanGrace     Plan = "grace"   // 7 days after the trial
	PlanGranted   Plan = "granted" // manual beta grant
	PlanSuspended Plan = "suspended"
)

type Status struct {
	Plan         Plan
	TrialEnds    time.Time
	SuspendAfter time.Time
	GrantUntil   time.Time
}

type Account interface {
	// Observe records that the installation has (or no longer has) private repositories and
	// starts the trial on the first private one. It rewrites the gate's Entitlement record.
	Observe(ctx context.Context, installationID int64, hasPrivate bool, now time.Time) error
	Status(ctx context.Context, installationID int64, now time.Time) (Status, error)
	// Grant sets a manual beta grant. Operator-only; audited in the log by installation ID.
	Grant(ctx context.Context, installationID int64, until time.Time, now time.Time) error
	// Refresh recomputes the Entitlement record of one installation (called by the sweep).
	Refresh(ctx context.Context, installationID int64, now time.Time) error
	// Forget deletes every key of the installation (uninstall), its admission included.
	Forget(ctx context.Context, installationID int64) error
	// Admit records an operator's approval of the installation: the gate serves it from the next event on.
	// Operator-only; audited in the log by installation ID and operator.
	Admit(ctx context.Context, installationID int64, a gate.Admission) error
	// Unadmit deletes the approval: the gate serves nothing for the installation from the next event on.
	// Unadmitting an installation without an approval is not an error.
	Unadmit(ctx context.Context, installationID int64, byID int64) error
}

type ConsentKind string

const (
	ConsentSourceProcessing ConsentKind = "source_processing" // managed discovery
)

type Consent struct {
	Kind ConsentKind
	By   string // GitHub login of the administrator, as it was when the consent was recorded
	ByID int64  // GitHub user ID of the administrator; 0 on a record written before the App stored it
	At   time.Time
}

// ErrNoConsent is returned by callers' guards when Has is false.
var ErrNoConsent = errors.New("account: consent not recorded")

// ErrConsentExists is returned by Record when a consent of that kind is already recorded; the record is unchanged.
var ErrConsentExists = errors.New("account: consent already recorded")

type Consents interface {
	// Record creates the consent atomically; ErrConsentExists if one is recorded, which keeps who and when.
	Record(ctx context.Context, installationID int64, c Consent) error
	Revoke(ctx context.Context, installationID int64, k ConsentKind) error
	// Has is false on any error: no consent, no call.
	Has(ctx context.Context, installationID int64, k ConsentKind) bool
	// Get answers who recorded the consent and when. Unlike Has it tells "not recorded" (false, nil) from "cannot
	// tell" (an error: the Store failed or the record is corrupt), so a page never shows an outage as "not allowed".
	Get(ctx context.Context, installationID int64, k ConsentKind) (Consent, bool, error)
}

func New(s store.Store) (Account, Consents) {
	return newWithLogger(s, slog.Default())
}

func newWithLogger(s store.Store, log *slog.Logger) (Account, Consents) {
	return &accounts{s: s, log: log}, &consents{s: s}
}

func tenantOf(installationID int64) (store.Tenant, error) {
	if installationID <= 0 {
		return 0, fmt.Errorf("account: invalid installation ID %d", installationID)
	}
	return store.Tenant(installationID), nil
}
