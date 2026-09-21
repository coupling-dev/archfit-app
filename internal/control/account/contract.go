// Package account owns subscription status, consents and the managed-LLM budget.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
	// Forget deletes every key of the installation (uninstall).
	Forget(ctx context.Context, installationID int64) error
}

type ConsentKind string

const (
	ConsentSourceProcessing ConsentKind = "source_processing" // managed discovery
	ConsentManagedLLM       ConsentKind = "managed_llm"
)

type Consent struct {
	Kind ConsentKind
	By   string // GitHub login of the administrator
	At   time.Time
}

// ErrNoConsent is returned by callers' guards when Has is false.
var ErrNoConsent = errors.New("account: consent not recorded")

type Consents interface {
	Record(ctx context.Context, installationID int64, c Consent) error
	Revoke(ctx context.Context, installationID int64, k ConsentKind) error
	// Has is false on any error: no consent, no call.
	Has(ctx context.Context, installationID int64, k ConsentKind) bool
}

var ErrBudgetExhausted = errors.New("account: managed LLM budget exhausted")

// Budget is the App-wide managed-LLM hard cap: USD 20 per calendar month (UTC), in micro-USD.
type Budget interface {
	// Reserve atomically books maxCost; ErrBudgetExhausted if the cap would be exceeded.
	Reserve(ctx context.Context, maxCostMicroUSD int64, now time.Time) error
	// Settle returns the unused part of a reservation.
	Settle(ctx context.Context, reservedMicroUSD, actualMicroUSD int64, now time.Time) error
}

func New(s store.Store) (Account, Consents, Budget) {
	return newWithLogger(s, slog.Default())
}

func newWithLogger(s store.Store, log *slog.Logger) (Account, Consents, Budget) {
	return &accounts{s: s, log: log}, &consents{s: s}, &budget{s: s, log: log}
}

func tenantOf(installationID int64) (store.Tenant, error) {
	if installationID <= 0 {
		return 0, fmt.Errorf("account: invalid installation ID %d", installationID)
	}
	return store.Tenant(installationID), nil
}
