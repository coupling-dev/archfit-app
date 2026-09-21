// Package storetest is the conformance suite every store.Store implementation must pass.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	maxKeyLen   = 512
	maxValueLen = 64 << 10
)

// Run runs every conformance case against a fresh store per case.
func Run(t *testing.T, newStore func() store.Store) {
	cases := []struct {
		name string
		run  func(*testing.T, store.Store)
	}{
		{"GetPutDelete", testGetPutDelete},
		{"Add", testAdd},
		{"AddIsAtomic", testAddIsAtomic},
		{"TenantIsolation", testTenantIsolation},
		{"DeleteTenant", testDeleteTenant},
		{"Tenants", testTenants},
		{"ConcurrentUse", testConcurrentUse},
		{"InvalidInput", testInvalidInput},
		{"AddRejectsNonCounterAndOverflow", testAddRejectsNonCounterAndOverflow},
		{"DeleteSystemTenantRefused", testDeleteSystemTenantRefused},
		{"CancelledContext", testCancelledContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newStore()) })
	}
}

func mustPut(t *testing.T, s store.Store, tn store.Tenant, key, value string) {
	t.Helper()
	if err := s.Put(context.Background(), tn, key, []byte(value)); err != nil {
		t.Fatalf("Put(%d, %q): %v", tn, key, err)
	}
}

func mustAdd(t *testing.T, s store.Store, tn store.Tenant, key string, delta int64) {
	t.Helper()
	if _, err := s.Add(context.Background(), tn, key, delta); err != nil {
		t.Fatalf("Add(%d, %q, %d): %v", tn, key, delta, err)
	}
}

func wantValue(t *testing.T, s store.Store, tn store.Tenant, key, want string) {
	t.Helper()
	got, err := s.Get(context.Background(), tn, key)
	if err != nil {
		t.Fatalf("Get(%d, %q): %v", tn, key, err)
	}
	if string(got) != want {
		t.Errorf("Get(%d, %q) = %q, want %q", tn, key, got, want)
	}
}

func wantNotFound(t *testing.T, s store.Store, tn store.Tenant, key string) {
	t.Helper()
	if _, err := s.Get(context.Background(), tn, key); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(%d, %q) error = %v, want ErrNotFound", tn, key, err)
	}
}

func wantTenants(t *testing.T, s store.Store, want ...store.Tenant) {
	t.Helper()
	got, err := s.Tenants(context.Background())
	if err != nil {
		t.Fatalf("Tenants: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Tenants = %v, want %v", got, want)
	}
}

func testGetPutDelete(t *testing.T, s store.Store) {
	ctx := context.Background()
	const tn = store.Tenant(1)

	wantNotFound(t, s, tn, "missing")

	mustPut(t, s, tn, "k", "v1")
	wantValue(t, s, tn, "k", "v1")

	mustPut(t, s, tn, "k", "v2")
	wantValue(t, s, tn, "k", "v2")

	if err := s.Put(ctx, tn, "nil", nil); err != nil {
		t.Fatalf("Put nil: %v", err)
	}
	wantValue(t, s, tn, "nil", "")

	if err := s.Put(ctx, tn, "empty", []byte{}); err != nil {
		t.Fatalf("Put empty: %v", err)
	}
	wantValue(t, s, tn, "empty", "")

	if err := s.Delete(ctx, tn, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantNotFound(t, s, tn, "k")

	if err := s.Delete(ctx, tn, "missing"); err != nil {
		t.Errorf("Delete missing = %v, want nil", err)
	}
}

func testAdd(t *testing.T, s store.Store) {
	ctx := context.Background()
	const tn = store.Tenant(1)
	steps := []struct {
		delta, want int64
	}{{5, 5}, {-2, 3}, {0, 3}, {-10, -7}}
	for _, step := range steps {
		got, err := s.Add(ctx, tn, "n", step.delta)
		if err != nil {
			t.Fatalf("Add(%d): %v", step.delta, err)
		}
		if got != step.want {
			t.Errorf("Add(%d) = %d, want %d", step.delta, got, step.want)
		}
		wantValue(t, s, tn, "n", strconv.FormatInt(step.want, 10))
	}
}

func testAddIsAtomic(t *testing.T, s store.Store) {
	const workers, perWorker = 50, 100
	ctx := context.Background()
	const tn = store.Tenant(1)
	results := make([][]int64, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for range perWorker {
				n, err := s.Add(ctx, tn, "n", 1)
				if err != nil {
					errs[w] = err
					return
				}
				results[w] = append(results[w], n)
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Add: %v", err)
	}
	seen := make(map[int64]bool, workers*perWorker)
	for _, r := range results {
		for _, n := range r {
			if seen[n] {
				t.Errorf("Add returned %d twice", n)
			}
			seen[n] = true
		}
	}
	wantValue(t, s, tn, "n", strconv.Itoa(workers*perWorker))
}

func testTenantIsolation(t *testing.T, s store.Store) {
	ctx := context.Background()
	const a, b = store.Tenant(1), store.Tenant(2)
	mustPut(t, s, a, "k", "a-value")

	wantNotFound(t, s, b, "k")
	if err := s.Delete(ctx, b, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantValue(t, s, a, "k", "a-value")
	if n, err := s.Add(ctx, b, "k", 1); err != nil || n != 1 {
		t.Errorf("Add under B = %d, %v, want 1, nil", n, err)
	}
	wantValue(t, s, a, "k", "a-value")
}

func testDeleteTenant(t *testing.T, s store.Store) {
	ctx := context.Background()
	const a, b = store.Tenant(1), store.Tenant(12)
	aKeys := []string{"entitlement", "consent/managed_llm", "n"}
	mustPut(t, s, a, aKeys[0], "x")
	mustPut(t, s, a, aKeys[1], "y")
	mustAdd(t, s, a, aKeys[2], 4)
	mustPut(t, s, b, "entitlement", "b-value")
	mustAdd(t, s, store.SystemTenant, "llm-budget/2026-09", 10)

	for i := range 2 {
		if err := s.DeleteTenant(ctx, a); err != nil {
			t.Fatalf("DeleteTenant call %d: %v", i+1, err)
		}
	}
	for _, k := range aKeys {
		wantNotFound(t, s, a, k)
	}
	wantValue(t, s, b, "entitlement", "b-value")
	wantValue(t, s, store.SystemTenant, "llm-budget/2026-09", "10")
}

func testTenants(t *testing.T, s store.Store) {
	ctx := context.Background()
	got, err := s.Tenants(ctx)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("Tenants on an empty store = %v, %v, want empty slice, nil", got, err)
	}

	mustPut(t, s, 7, "k", "v")
	mustPut(t, s, 3, "k", "v")
	mustAdd(t, s, store.SystemTenant, "llm-budget/2026-09", 1)
	wantTenants(t, s, 3, 7)

	mustPut(t, s, 3, "k2", "v")
	wantTenants(t, s, 3, 7)

	if err := s.Delete(ctx, 7, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantTenants(t, s, 3)

	if err := s.DeleteTenant(ctx, 3); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	wantTenants(t, s)

	mustAdd(t, s, 9, "n", 1)
	wantTenants(t, s, 9)
}

func testConcurrentUse(t *testing.T, s store.Store) {
	const workers, rounds = 20, 50
	ctx := context.Background()
	values := []string{"v0", "v1", "v2", "v3", "v4"}
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			tn := store.Tenant(w%3 + 1)
			for i := range rounds {
				var err error
				switch (w + i) % 5 {
				case 0:
					err = s.Put(ctx, tn, "k", []byte(values[i%len(values)]))
				case 1:
					var got []byte
					got, err = s.Get(ctx, tn, "k")
					if errors.Is(err, store.ErrNotFound) {
						err = nil
					} else if err == nil && !slices.Contains(values, string(got)) {
						err = fmt.Errorf("got %q from Get, which was never put", got)
					}
				case 2:
					err = s.Delete(ctx, tn, "k")
				case 3:
					err = s.DeleteTenant(ctx, tn)
				case 4:
					_, err = s.Tenants(ctx)
				}
				if err != nil {
					errs[w] = err
					return
				}
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func testInvalidInput(t *testing.T, s store.Store) {
	ctx := context.Background()
	const tn = store.Tenant(5)
	mustPut(t, s, tn, "k", "v")

	// Every operation that takes the input, per invalid input.
	keyOps := map[string]func(store.Tenant, string) error{
		"Get":    func(tn store.Tenant, k string) error { _, err := s.Get(ctx, tn, k); return err },
		"Put":    func(tn store.Tenant, k string) error { return s.Put(ctx, tn, k, []byte("x")) },
		"Delete": func(tn store.Tenant, k string) error { return s.Delete(ctx, tn, k) },
		"Add":    func(tn store.Tenant, k string) error { _, err := s.Add(ctx, tn, k, 1); return err },
	}
	inputs := []struct {
		name   string
		tenant store.Tenant
		key    string
	}{
		{"empty key", tn, ""},
		{"513-byte key", tn, strings.Repeat("a", maxKeyLen+1)},
		{"key with NUL", tn, "a\x00b"},
		{"negative tenant", -1, "k"},
	}
	for _, in := range inputs {
		for op, call := range keyOps {
			if err := call(in.tenant, in.key); err == nil {
				t.Errorf("%s with %s: want an error", op, in.name)
			} else if errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s with %s: got ErrNotFound, want a validation error", op, in.name)
			}
		}
	}
	if err := s.DeleteTenant(ctx, -1); err == nil {
		t.Error("DeleteTenant with negative tenant: want an error")
	}

	big := bytes.Repeat([]byte("x"), maxValueLen+1)
	if err := s.Put(ctx, tn, "k", big); err == nil {
		t.Error("Put with a 64 KiB + 1 value over a key: want an error")
	}
	if err := s.Put(ctx, tn, "new", big); err == nil {
		t.Error("Put with a 64 KiB + 1 value: want an error")
	}

	wantValue(t, s, tn, "k", "v")
	wantNotFound(t, s, tn, "new")
	wantTenants(t, s, tn)

	// The limits themselves are valid.
	longKey := strings.Repeat("a", maxKeyLen)
	if err := s.Put(ctx, tn, longKey, big[:maxValueLen]); err != nil {
		t.Errorf("Put with a 512-byte key and a 64 KiB value: %v", err)
	}
}

func testAddRejectsNonCounterAndOverflow(t *testing.T, s store.Store) {
	ctx := context.Background()
	const tn = store.Tenant(1)
	cases := []struct {
		stored string
		delta  int64
	}{
		{"abc", 1},
		{"", 1},
		{"+7", 1},
		{"007", 1},
		{"-0", 1},
		{" 7", 1},
		{strconv.FormatInt(math.MaxInt64, 10), 1},
		{strconv.FormatInt(math.MinInt64, 10), -1},
	}
	for _, tc := range cases {
		mustPut(t, s, tn, "k", tc.stored)
		if n, err := s.Add(ctx, tn, "k", tc.delta); err == nil {
			t.Errorf("Add(%d) over %q = %d, want an error", tc.delta, tc.stored, n)
		}
		wantValue(t, s, tn, "k", tc.stored)
	}
}

func testDeleteSystemTenantRefused(t *testing.T, s store.Store) {
	mustAdd(t, s, store.SystemTenant, "llm-budget/2026-09", 10)
	if err := s.DeleteTenant(context.Background(), store.SystemTenant); err == nil {
		t.Error("DeleteTenant(SystemTenant): want an error")
	}
	wantValue(t, s, store.SystemTenant, "llm-budget/2026-09", "10")
}

func testCancelledContext(t *testing.T, s store.Store) {
	const tn = store.Tenant(1)
	mustPut(t, s, tn, "k", "v")
	mustAdd(t, s, tn, "n", 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ops := map[string]func() error{
		"Get":          func() error { _, err := s.Get(ctx, tn, "k"); return err },
		"Put":          func() error { return s.Put(ctx, tn, "k", []byte("changed")) },
		"Put new":      func() error { return s.Put(ctx, 2, "k", []byte("created")) },
		"Delete":       func() error { return s.Delete(ctx, tn, "k") },
		"Add":          func() error { _, err := s.Add(ctx, tn, "n", 1); return err },
		"DeleteTenant": func() error { return s.DeleteTenant(ctx, tn) },
		"Tenants":      func() error { _, err := s.Tenants(ctx); return err },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s error = %v, want context.Canceled", name, err)
		}
	}
	wantValue(t, s, tn, "k", "v")
	wantValue(t, s, tn, "n", "1")
	wantTenants(t, s, tn)
}
