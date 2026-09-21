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

const recordKey = "account"

type accounts struct {
	s   store.Store
	log *slog.Logger
}

func (a *accounts) Observe(ctx context.Context, installationID int64, hasPrivate bool, now time.Time) error {
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	r, err := a.load(ctx, t)
	if err != nil {
		return err
	}
	if hasPrivate && r.FirstPrivateAt.IsZero() {
		r.FirstPrivateAt = now.UTC()
		if err := a.save(ctx, t, r); err != nil {
			return err
		}
	}
	return a.writeEntitlement(ctx, t, r, now)
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
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	r, err := a.load(ctx, t)
	if err != nil {
		return err
	}
	r.GrantUntil = until
	if !until.After(now) {
		r.GrantUntil = time.Time{}
	}
	if err := a.save(ctx, t, r); err != nil {
		return err
	}
	return a.writeEntitlement(ctx, t, r, now)
}

func (a *accounts) Refresh(ctx context.Context, installationID int64, now time.Time) error {
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	r, err := a.load(ctx, t)
	if err != nil {
		return err
	}
	return a.writeEntitlement(ctx, t, r, now)
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

// load returns the zero record when none exists. Any other failure is an error, so callers
// write nothing: an outage or a corrupt record never restarts the trial or implies suspension.
func (a *accounts) load(ctx context.Context, t store.Tenant) (record, error) {
	b, err := a.s.Get(ctx, t, recordKey)
	if errors.Is(err, store.ErrNotFound) {
		return record{}, nil
	}
	if err != nil {
		return record{}, fmt.Errorf("account: read record: %w", err)
	}
	var r record
	if err := json.Unmarshal(b, &r); err != nil {
		return record{}, fmt.Errorf("account: decode record: %w", err)
	}
	return r, nil
}

func (a *accounts) save(ctx context.Context, t store.Tenant, r record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("account: encode record: %w", err)
	}
	if err := a.s.Put(ctx, t, recordKey, b); err != nil {
		return fmt.Errorf("account: write record: %w", err)
	}
	return nil
}

func (a *accounts) writeEntitlement(ctx context.Context, t store.Tenant, r record, now time.Time) error {
	b, err := json.Marshal(r.entitlement(now))
	if err != nil {
		return fmt.Errorf("account: encode entitlement: %w", err)
	}
	if err := a.s.Put(ctx, t, gate.EntitlementKey, b); err != nil {
		return fmt.Errorf("account: write entitlement: %w", err)
	}
	return nil
}
