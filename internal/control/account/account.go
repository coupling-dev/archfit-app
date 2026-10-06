package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	recordKey = "account"
	// maxUpdateAttempts bounds the read-compute-swap loop of update when other writers keep changing the keys.
	// [default]
	maxUpdateAttempts = 5
)

type accounts struct {
	s   store.Store
	log *slog.Logger
}

func (a *accounts) Observe(ctx context.Context, installationID int64, hasPrivate bool, now time.Time) error {
	return a.update(ctx, installationID, now, func(r *record) {
		if hasPrivate && r.FirstPrivateAt.IsZero() {
			r.FirstPrivateAt = now.UTC()
		}
	})
}

func (a *accounts) Status(ctx context.Context, installationID int64, now time.Time) (Status, error) {
	t, err := tenantOf(installationID)
	if err != nil {
		return Status{}, err
	}
	r, err := a.load(ctx, t)
	if err != nil {
		return Status{}, err
	}
	return r.status(now), nil
}

func (a *accounts) Grant(ctx context.Context, installationID int64, until time.Time, now time.Time) error {
	until = until.UTC()
	err := a.grant(ctx, installationID, until, now)
	if err != nil {
		a.log.ErrorContext(ctx, "account grant", "installation_id", installationID, "until", until,
			"outcome", "error", "error", err.Error())
		return err
	}
	a.log.InfoContext(ctx, "account grant", "installation_id", installationID, "until", until, "outcome", "ok")
	return nil
}

func (a *accounts) grant(ctx context.Context, installationID int64, until, now time.Time) error {
	return a.update(ctx, installationID, now, func(r *record) {
		r.GrantUntil = until
		if !until.After(now) {
			r.GrantUntil = time.Time{}
		}
	})
}

func (a *accounts) Refresh(ctx context.Context, installationID int64, now time.Time) error {
	return a.update(ctx, installationID, now, func(*record) {})
}

func (a *accounts) Forget(ctx context.Context, installationID int64) error {
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	if err := a.s.DeleteTenant(ctx, t); err != nil {
		return fmt.Errorf("account: forget installation: %w", err)
	}
	return nil
}

// update applies change to the record and writes the record and its derived entitlement in one
// CompareAndSwapAll, from the bytes it read. When another writer changed either key in between, it reads again,
// at most maxUpdateAttempts times. So the entitlement always matches the record it was derived from, and a
// concurrent change is never dropped. A record that change leaves equal is only checked, not rewritten.
func (a *accounts) update(ctx context.Context, installationID int64, now time.Time, change func(*record)) error {
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	for range maxUpdateAttempts {
		oldRecord, r, err := a.read(ctx, t)
		if err != nil {
			return err
		}
		oldEntitlement, err := a.s.Get(ctx, t, gate.EntitlementKey)
		if errors.Is(err, store.ErrNotFound) {
			oldEntitlement = nil
		} else if err != nil {
			return fmt.Errorf("account: read entitlement: %w", err)
		}
		before := r
		change(&r)
		newRecord := oldRecord
		if r != before {
			if newRecord, err = json.Marshal(r); err != nil {
				return fmt.Errorf("account: encode record: %w", err)
			}
		}
		newEntitlement, err := json.Marshal(r.entitlement(now))
		if err != nil {
			return fmt.Errorf("account: encode entitlement: %w", err)
		}
		swapped, err := a.s.CompareAndSwapAll(ctx, t, []store.Swap{
			{Key: recordKey, Old: oldRecord, Value: newRecord},
			{Key: gate.EntitlementKey, Old: oldEntitlement, Value: newEntitlement},
		})
		if err != nil {
			return fmt.Errorf("account: write record and entitlement: %w", err)
		}
		if swapped {
			return nil
		}
	}
	return fmt.Errorf("account: record changed concurrently %d times; nothing written by this call", maxUpdateAttempts)
}

// load returns the zero record when none exists. Any other failure is an error, so callers
// write nothing: an outage or a corrupt record never restarts the trial or implies suspension.
func (a *accounts) load(ctx context.Context, t store.Tenant) (record, error) {
	_, r, err := a.read(ctx, t)
	return r, err
}

// read is load that also returns the stored bytes: nil when the record is missing.
func (a *accounts) read(ctx context.Context, t store.Tenant) ([]byte, record, error) {
	b, err := a.s.Get(ctx, t, recordKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil, record{}, nil
	}
	if err != nil {
		return nil, record{}, fmt.Errorf("account: read record: %w", err)
	}
	var r record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, record{}, fmt.Errorf("account: decode record: %w", err)
	}
	return b, r, nil
}
