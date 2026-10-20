package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

const consentKeyPrefix = "consent/"

// consentRecord holds the login as it was when the consent was recorded. by_id is absent from a record written
// before the App stored it; such a record stays valid.
type consentRecord struct {
	Kind ConsentKind `json:"kind"`
	By   string      `json:"by"`
	ByID int64       `json:"by_id,omitempty"`
	At   time.Time   `json:"at"`
}

func (c consentRecord) validate() error {
	switch {
	case c.Kind != ConsentSourceProcessing && c.Kind != ConsentManagedLLM:
		return fmt.Errorf("account: unknown consent kind %q", c.Kind)
	case c.By == "":
		return errors.New("account: consent without an administrator login")
	case c.At.IsZero():
		return errors.New("account: consent without a time")
	}
	return nil
}

type consents struct {
	s store.Store
}

func (c *consents) Record(ctx context.Context, installationID int64, in Consent) error {
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	rec := consentRecord{Kind: in.Kind, By: in.By, ByID: in.ByID, At: in.At.UTC()}
	if err := rec.validate(); err != nil {
		return err
	}
	if rec.ByID <= 0 {
		return errors.New("account: consent without an administrator user ID")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("account: encode consent: %w", err)
	}
	if err := c.s.Put(ctx, t, consentKeyPrefix+string(rec.Kind), b); err != nil {
		return fmt.Errorf("account: write consent: %w", err)
	}
	return nil
}

func (c *consents) Revoke(ctx context.Context, installationID int64, k ConsentKind) error {
	t, err := tenantOf(installationID)
	if err != nil {
		return err
	}
	if err := c.s.Delete(ctx, t, consentKeyPrefix+string(k)); err != nil {
		return fmt.Errorf("account: revoke consent: %w", err)
	}
	return nil
}

func (c *consents) Has(ctx context.Context, installationID int64, k ConsentKind) bool {
	_, ok, err := c.Get(ctx, installationID, k)
	return ok && err == nil
}

func (c *consents) Get(ctx context.Context, installationID int64, k ConsentKind) (Consent, bool, error) {
	t, err := tenantOf(installationID)
	if err != nil {
		return Consent{}, false, err
	}
	b, err := c.s.Get(ctx, t, consentKeyPrefix+string(k))
	if errors.Is(err, store.ErrNotFound) {
		return Consent{}, false, nil
	}
	if err != nil {
		return Consent{}, false, fmt.Errorf("account: read consent: %w", err)
	}
	var rec consentRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return Consent{}, false, fmt.Errorf("account: decode consent: %w", err)
	}
	if rec.Kind != k {
		return Consent{}, false, fmt.Errorf("account: consent key %q holds kind %q", k, rec.Kind)
	}
	if err := rec.validate(); err != nil {
		return Consent{}, false, err
	}
	return Consent(rec), true, nil
}
