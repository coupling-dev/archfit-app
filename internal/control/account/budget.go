package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	budgetKeyPrefix = "llm-budget/"
	// [default] USD 20 per UTC calendar month.
	budgetCapMicroUSD int64 = 20_000_000
)

type budget struct {
	s   store.Store
	log *slog.Logger
}

func budgetKey(now time.Time) string {
	return budgetKeyPrefix + now.UTC().Format("2006-01")
}

func (b *budget) Reserve(ctx context.Context, maxCostMicroUSD int64, now time.Time) error {
	if maxCostMicroUSD <= 0 {
		return fmt.Errorf("account: invalid reservation %d", maxCostMicroUSD)
	}
	key := budgetKey(now)
	total, err := b.s.Add(ctx, store.SystemTenant, key, maxCostMicroUSD)
	if err != nil {
		return fmt.Errorf("account: reserve budget: %w", err)
	}
	if total <= budgetCapMicroUSD {
		return nil
	}
	// Store has only Add, so the cap is enforced by reserve-then-compensate. A failed
	// compensation leaves the counter too high: less spend until the month rolls over.
	if _, err := b.s.Add(ctx, store.SystemTenant, key, -maxCostMicroUSD); err != nil {
		b.log.ErrorContext(ctx, "account budget compensation failed", "month", key,
			"over_reserved_micro_usd", maxCostMicroUSD, "outcome", "error", "error", err.Error())
		return errors.Join(ErrBudgetExhausted, fmt.Errorf("account: compensate reservation: %w", err))
	}
	return ErrBudgetExhausted
}

func (b *budget) Settle(ctx context.Context, reservedMicroUSD, actualMicroUSD int64, now time.Time) error {
	if reservedMicroUSD <= 0 || actualMicroUSD < 0 {
		return fmt.Errorf("account: invalid settlement reserved=%d actual=%d", reservedMicroUSD, actualMicroUSD)
	}
	if _, err := b.s.Add(ctx, store.SystemTenant, budgetKey(now), actualMicroUSD-reservedMicroUSD); err != nil {
		return fmt.Errorf("account: settle budget: %w", err)
	}
	return nil
}
