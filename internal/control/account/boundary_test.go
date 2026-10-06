package account

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/store"
)

func accountWrites(a Account, id int64, now time.Time) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"Observe": func(ctx context.Context) error { return a.Observe(ctx, id, true, now) },
		"Refresh": func(ctx context.Context) error { return a.Refresh(ctx, id, now) },
		"Grant":   func(ctx context.Context) error { return a.Grant(ctx, id, now.Add(24*time.Hour), now) },
	}
}

func TestStoreFailureNeverSuspends(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"Get", "Put"} {
		for _, op := range []string{"Observe", "Refresh", "Grant"} {
			t.Run(failure+" fails during "+op, func(t *testing.T) {
				s := newSpy()
				a, _, _, _ := newTest(s)
				mustNil(t, a.Observe(ctx, instA, true, day(0)))
				before := rawKey(t, s.Store, instA, gate.EntitlementKey)
				putsBefore := len(s.writes(recordKeyName)) + len(s.writes(gate.EntitlementKey))

				if failure == "Get" {
					s.getErr = errBoom
				} else {
					s.putErr = errBoom
				}
				// day 50: a healthy write would move the record to "suspended".
				err := accountWrites(a, instA, day(50))[op](ctx)
				if !errors.Is(err, errBoom) {
					t.Fatalf("err = %v, want the Store error", err)
				}
				putsAfter := len(s.writes(recordKeyName)) + len(s.writes(gate.EntitlementKey))
				if failure == "Get" && putsAfter != putsBefore {
					t.Errorf("%d Puts followed a failed Get", putsAfter-putsBefore)
				}
				if !bytes.Equal(rawKey(t, s.Store, instA, gate.EntitlementKey), before) {
					t.Error("entitlement bytes changed")
				}
			})
		}
	}

	t.Run("Status returns the Get error", func(t *testing.T) {
		s := newSpy()
		a, _, _, _ := newTest(s)
		s.getErr = errBoom
		if _, err := a.Status(ctx, instA, day(0)); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want the Store error", err)
		}
	})
}

func TestCorruptRecordIsAnError(t *testing.T) {
	ctx := context.Background()
	corrupt := []byte(`{"first_private_at":`)
	s := newSpy()
	mustNil(t, s.Store.Put(ctx, store.Tenant(instA), recordKeyName, corrupt))
	a, _, _, _ := newTest(s)

	ops := accountWrites(a, instA, day(1))
	ops["Status"] = func(ctx context.Context) error {
		_, err := a.Status(ctx, instA, day(1))
		return err
	}
	for name, op := range ops {
		if err := op(ctx); err == nil {
			t.Errorf("%s: no error on a corrupt record", name)
		}
	}
	for _, c := range s.calls {
		if c.op != "Get" {
			t.Errorf("unexpected write: %s %q", c.op, c.key)
		}
	}
	if !bytes.Equal(rawKey(t, s.Store, instA, recordKeyName), corrupt) {
		t.Error("corrupt record was overwritten")
	}
}

func TestInvalidInstallationID(t *testing.T) {
	ctx := context.Background()
	for _, id := range []int64{0, -1} {
		s := newSpy()
		a, c, _, _ := newTest(s)
		ops := accountWrites(a, id, day(0))
		ops["Status"] = func(ctx context.Context) error {
			_, err := a.Status(ctx, id, day(0))
			return err
		}
		ops["Forget"] = func(ctx context.Context) error { return a.Forget(ctx, id) }
		ops["Record"] = func(ctx context.Context) error {
			return c.Record(ctx, id, Consent{Kind: ConsentManagedLLM, By: "octocat", At: day(0)})
		}
		ops["Revoke"] = func(ctx context.Context) error { return c.Revoke(ctx, id, ConsentManagedLLM) }
		for name, op := range ops {
			if err := op(ctx); err == nil {
				t.Errorf("%s(%d): no error", name, id)
			}
		}
		if c.Has(ctx, id, ConsentManagedLLM) {
			t.Errorf("Has(%d) = true", id)
		}
		if n := s.count(); n != 0 {
			t.Errorf("id %d: %d Store calls, want 0: %v", id, n, s.calls)
		}
	}
}

func TestConsentRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	valid := Consent{Kind: ConsentSourceProcessing, By: "octocat", At: day(0)}

	invalid := map[string]func(*Consent){
		"unknown kind": func(c *Consent) { c.Kind = "telemetry" },
		"empty By":     func(c *Consent) { c.By = "" },
		"zero At":      func(c *Consent) { c.At = time.Time{} },
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			s := newSpy()
			_, c, _, _ := newTest(s)
			in := valid
			mutate(&in)
			if err := c.Record(ctx, instA, in); err == nil {
				t.Error("Record: no error")
			}
			if n := s.count(); n != 0 {
				t.Errorf("%d Store calls, want 0", n)
			}
		})
	}

	t.Run("Has with a failing Store", func(t *testing.T) {
		s := newSpy()
		_, c, _, _ := newTest(s)
		mustNil(t, c.Record(ctx, instA, valid))
		s.getErr = errBoom
		if c.Has(ctx, instA, valid.Kind) {
			t.Error("Has = true")
		}
	})

	for name, value := range map[string]string{
		"not JSON":   `{"kind":`,
		"wrong kind": `{"kind":"managed_llm","by":"octocat","at":"2026-09-01T00:00:00Z"}`,
		"no login":   `{"kind":"source_processing","by":"","at":"2026-09-01T00:00:00Z"}`,
	} {
		t.Run("Has with a corrupt value: "+name, func(t *testing.T) {
			s := store.NewMemory()
			_, c, _, _ := newTest(s)
			mustNil(t, s.Put(ctx, store.Tenant(instA), "consent/source_processing", []byte(value)))
			if c.Has(ctx, instA, ConsentSourceProcessing) {
				t.Error("Has = true")
			}
		})
	}
}

func TestBudgetRejectsAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	now := day(3)

	t.Run("non-positive cost", func(t *testing.T) {
		s := newSpy()
		_, _, b, _ := newTest(s)
		for _, cost := range []int64{0, -1} {
			if err := b.Reserve(ctx, cost, now); err == nil || errors.Is(err, ErrBudgetExhausted) {
				t.Errorf("Reserve(%d) = %v, want a plain error", cost, err)
			}
		}
		if n := s.count(); n != 0 {
			t.Errorf("%d Store calls, want 0", n)
		}
	})

	t.Run("Add fails on reserve", func(t *testing.T) {
		s := newSpy()
		_, _, b, _ := newTest(s)
		s.failAddOn = 1
		err := b.Reserve(ctx, 1_000_000, now)
		if !errors.Is(err, errBoom) || errors.Is(err, ErrBudgetExhausted) {
			t.Errorf("err = %v, want the Store error only", err)
		}
		if got := counter(t, s.Store, "2026-09"); got != 0 {
			t.Errorf("counter = %d, want 0", got)
		}
	})

	t.Run("compensating Add fails", func(t *testing.T) {
		s := newSpy()
		_, _, b, _ := newTest(s)
		mustNil(t, b.Reserve(ctx, capMicroUSD, now))
		s.failAddOn = s.adds + 2
		if err := b.Reserve(ctx, 5_000_000, now); err == nil {
			t.Error("no error")
		}
		if got := counter(t, s.Store, "2026-09"); got != capMicroUSD+5_000_000 {
			t.Errorf("counter = %d, want %d (over-reserved, never lower)", got, capMicroUSD+5_000_000)
		}
		if err := b.Reserve(ctx, 1, now); !errors.Is(err, ErrBudgetExhausted) {
			t.Errorf("next reserve: %v, want ErrBudgetExhausted", err)
		}
	})

	t.Run("Settle returns the Store error", func(t *testing.T) {
		s := newSpy()
		_, _, b, _ := newTest(s)
		s.failAddOn = 1
		if err := b.Settle(ctx, 5_000_000, 1_000_000, now); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want the Store error", err)
		}
	})
}

func TestConcurrentReserveNeverExceedsCap(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	_, _, b, _ := newTest(s)
	var ok atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			err := b.Reserve(ctx, 1_000_000, day(3))
			switch {
			case err == nil:
				ok.Add(1)
			case !errors.Is(err, ErrBudgetExhausted):
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()
	if got := ok.Load(); got != 20 {
		t.Errorf("successful reservations = %d, want 20", got)
	}
	if got := counter(t, s, "2026-09"); got != capMicroUSD {
		t.Errorf("counter = %d, want %d", got, capMicroUSD)
	}
}

func TestOnlyDocumentedKeys(t *testing.T) {
	ctx := context.Background()
	s := newSpy()
	a, c, b, _ := newTest(s)
	now := day(3)

	mustNil(t, a.Observe(ctx, instA, false, now))
	mustNil(t, a.Observe(ctx, instA, true, now))
	mustNil(t, a.Grant(ctx, instA, day(90), now))
	mustNil(t, a.Refresh(ctx, instA, now))
	_, err := a.Status(ctx, instA, now)
	mustNil(t, err)
	for _, k := range []ConsentKind{ConsentSourceProcessing, ConsentManagedLLM} {
		mustNil(t, c.Record(ctx, instA, Consent{Kind: k, By: "octocat", At: now}))
		c.Has(ctx, instA, k)
		mustNil(t, c.Revoke(ctx, instA, k))
	}
	mustNil(t, b.Reserve(ctx, capMicroUSD, now))
	if err := b.Reserve(ctx, 1, now); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	mustNil(t, b.Settle(ctx, capMicroUSD, 1_000_000, now))
	mustNil(t, a.Forget(ctx, instA))

	installationKeys := map[string]bool{
		"account": true, "entitlement": true,
		"consent/source_processing": true, "consent/managed_llm": true,
	}
	seen := map[string]bool{}
	for _, call := range s.calls {
		switch {
		case call.op == "DeleteTenant" && call.tenant == store.Tenant(instA):
		case call.tenant == store.Tenant(instA) && installationKeys[call.key]:
			seen[call.key] = true
		case call.tenant == store.SystemTenant && call.op == "Add" && call.key == "llm-budget/2026-09":
			seen[call.key] = true
		default:
			t.Errorf("undocumented call: %s tenant=%d key=%q", call.op, call.tenant, call.key)
		}
	}
	if len(seen) != len(installationKeys)+1 {
		t.Errorf("keys touched = %v, want all documented keys", seen)
	}
}
