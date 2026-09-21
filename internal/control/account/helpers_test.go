package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	instA int64 = 42
	instB int64 = 77

	recordKeyName = "account"
	capMicroUSD   = 20_000_000
)

var (
	t0      = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	errBoom = errors.New("store down")
)

func day(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

func newTest(s store.Store) (Account, Consents, Budget, *bytes.Buffer) {
	var buf bytes.Buffer
	a, c, b := newWithLogger(s, slog.New(slog.NewJSONHandler(&buf, nil)))
	return a, c, b, &buf
}

type call struct {
	op     string
	tenant store.Tenant
	key    string
	value  []byte
}

// spyStore records every call and fails on demand.
type spyStore struct {
	store.Store
	mu        sync.Mutex
	calls     []call
	getErr    error
	putErr    error
	failAddOn int // 1-based index of the Add call that fails; 0 = never
	adds      int
}

func newSpy() *spyStore { return &spyStore{Store: store.NewMemory()} }

func (s *spyStore) record(op string, t store.Tenant, key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{op, t, key, value})
}

func (s *spyStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.record("Get", t, key, nil)
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.Store.Get(ctx, t, key)
}

func (s *spyStore) Put(ctx context.Context, t store.Tenant, key string, value []byte) error {
	s.record("Put", t, key, value)
	if s.putErr != nil {
		return s.putErr
	}
	return s.Store.Put(ctx, t, key, value)
}

func (s *spyStore) Delete(ctx context.Context, t store.Tenant, key string) error {
	s.record("Delete", t, key, nil)
	return s.Store.Delete(ctx, t, key)
}

func (s *spyStore) Add(ctx context.Context, t store.Tenant, key string, delta int64) (int64, error) {
	s.record("Add", t, key, nil)
	s.mu.Lock()
	s.adds++
	fail := s.adds == s.failAddOn
	s.mu.Unlock()
	if fail {
		return 0, errBoom
	}
	return s.Store.Add(ctx, t, key, delta)
}

func (s *spyStore) DeleteTenant(ctx context.Context, t store.Tenant) error {
	s.record("DeleteTenant", t, "", nil)
	return s.Store.DeleteTenant(ctx, t)
}

func (s *spyStore) Tenants(ctx context.Context) ([]store.Tenant, error) {
	s.record("Tenants", 0, "", nil)
	return s.Store.Tenants(ctx)
}

func (s *spyStore) puts(key string) []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []call
	for _, c := range s.calls {
		if c.op == "Put" && c.key == key {
			out = append(out, c)
		}
	}
	return out
}

func (s *spyStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func rawKey(t *testing.T, s store.Store, id int64, key string) []byte {
	t.Helper()
	b, err := s.Get(context.Background(), store.Tenant(id), key)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	return b
}

func readEntitlement(t *testing.T, s store.Store, id int64) gate.Entitlement {
	t.Helper()
	b := rawKey(t, s, id, gate.EntitlementKey)
	if b == nil {
		t.Fatalf("no entitlement record for %d", id)
	}
	var e gate.Entitlement
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("decode entitlement: %v", err)
	}
	return e
}

func counter(t *testing.T, s store.Store, month string) int64 {
	t.Helper()
	v, err := s.Add(context.Background(), store.SystemTenant, "llm-budget/"+month, 0)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return v
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantTime(t *testing.T, name string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// gateSuspended restates the gate's reading rule.
func gateSuspended(e gate.Entitlement, private bool, now time.Time) bool {
	return private && !e.SuspendAfter.IsZero() && !now.Before(e.SuspendAfter)
}
