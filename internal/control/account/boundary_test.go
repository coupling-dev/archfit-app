package account

import (
	"bytes"
	"context"
	"errors"
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
				a, _ := newTest(s)
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
		a, _ := newTest(s)
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
	a, _ := newTest(s)

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
		a, _ := newTest(s)
		ops := accountWrites(a, id, day(0))
		ops["Status"] = func(ctx context.Context) error {
			_, err := a.Status(ctx, id, day(0))
			return err
		}
		ops["Forget"] = func(ctx context.Context) error { return a.Forget(ctx, id) }
		for name, op := range ops {
			if err := op(ctx); err == nil {
				t.Errorf("%s(%d): no error", name, id)
			}
		}
		if n := s.count(); n != 0 {
			t.Errorf("id %d: %d Store calls, want 0: %v", id, n, s.calls)
		}
	}
}

func TestOnlyDocumentedKeys(t *testing.T) {
	ctx := context.Background()
	s := newSpy()
	a, _ := newTest(s)
	now := day(3)

	mustNil(t, a.Observe(ctx, instA, false, now))
	mustNil(t, a.Observe(ctx, instA, true, now))
	mustNil(t, a.Grant(ctx, instA, day(90), now))
	mustNil(t, a.Refresh(ctx, instA, now))
	_, err := a.Status(ctx, instA, now)
	mustNil(t, err)
	mustNil(t, a.Forget(ctx, instA))

	installationKeys := map[string]bool{
		"account": true, "entitlement": true,
	}
	seen := map[string]bool{}
	for _, call := range s.calls {
		switch {
		case call.op == "DeleteTenant" && call.tenant == store.Tenant(instA):
		case call.tenant == store.Tenant(instA) && installationKeys[call.key]:
			seen[call.key] = true
		default:
			t.Errorf("undocumented call: %s tenant=%d key=%q", call.op, call.tenant, call.key)
		}
	}
	if len(seen) != len(installationKeys) {
		t.Errorf("keys touched = %v, want all documented keys", seen)
	}
}

func TestHistoricalConsentsRemainUnreadAndUnchanged(t *testing.T) {
	s := newSpy()
	original := []byte("opaque historical consent")
	key := "consent/source_processing"
	mustNil(t, s.Store.Put(t.Context(), store.Tenant(instA), key, original))
	a, _ := newTest(s)
	for _, op := range accountWrites(a, instA, day(0)) {
		mustNil(t, op(t.Context()))
	}
	_, err := a.Status(t.Context(), instA, day(0))
	mustNil(t, err)
	mustNil(t, a.Admit(t.Context(), instA, gate.Admission{Account: "acme", ByID: 1, By: "alice", At: day(0)}))
	mustNil(t, a.Unadmit(t.Context(), instA, 1))
	for _, call := range s.calls {
		if call.key == key {
			t.Errorf("historical consent accessed: %+v", call)
		}
	}
	got := rawKey(t, s.Store, instA, key)
	if !bytes.Equal(got, original) {
		t.Errorf("historical consent changed: %q", got)
	}
}
