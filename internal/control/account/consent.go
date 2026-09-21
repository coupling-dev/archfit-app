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

type consentRecord struct {
	Kind ConsentKind `json:"kind"`
	By   string      `json:"by"`
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
	rec := consentRecord{Kind: in.Kind, By: in.By, At: in.At.UTC()}
	if err := rec.validate(); err != nil {
		return err
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
	t, err := tenantOf(installationID)
	if err != nil {
		return false
	}
	b, err := c.s.Get(ctx, t, consentKeyPrefix+string(k))
	if err != nil {
		return false
	}
	var rec consentRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return false
	}
	return rec.Kind == k && rec.validate() == nil
}
