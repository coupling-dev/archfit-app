package store

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// memory keeps every tenant in one map under one lock. DeleteTenant and Tenants scan all keys:
// O(keys) per call is the accepted ceiling of a driver for tests and local runs. Shard per tenant
// if it ever serves real traffic.
type memory struct {
	mu   sync.Mutex
	data map[string][]byte
}

// NewMemory returns an in-process Store for tests and local runs. Data is lost on exit.
func NewMemory() Store {
	return &memory{data: make(map[string][]byte)}
}

func (m *memory) Get(ctx context.Context, t Tenant, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateKey("get", t, key); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[keyPath(t, key)]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte{}, v...), nil
}

func (m *memory) Put(ctx context.Context, t Tenant, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKey("put", t, key); err != nil {
		return err
	}
	if len(value) > maxValueLen {
		return opError("put", t, key, "value longer than "+strconv.Itoa(maxValueLen)+" bytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[keyPath(t, key)] = append([]byte{}, value...)
	return nil
}

func (m *memory) Delete(ctx context.Context, t Tenant, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKey("delete", t, key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, keyPath(t, key))
	return nil
}

func (m *memory) Add(ctx context.Context, t Tenant, key string, delta int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateKey("add", t, key); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path := keyPath(t, key)
	var cur int64
	if v, ok := m.data[path]; ok {
		var err error
		// The strconv error quotes its input, so it is dropped: errors never carry a value.
		// Only the canonical form counts: "+7" and "007" are not what Add writes.
		if cur, err = strconv.ParseInt(string(v), 10, 64); err != nil || strconv.FormatInt(cur, 10) != string(v) {
			return 0, opError("add", t, key, "value is not a counter")
		}
	}
	if (delta > 0 && cur > math.MaxInt64-delta) || (delta < 0 && cur < math.MinInt64-delta) {
		return 0, opError("add", t, key, "counter overflow")
	}
	cur += delta
	m.data[path] = strconv.AppendInt(nil, cur, 10)
	return cur, nil
}

func (m *memory) DeleteTenant(ctx context.Context, t Tenant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTenant("delete tenant", t, ""); err != nil {
		return err
	}
	if t == SystemTenant {
		return opError("delete tenant", t, "", "the system tenant cannot be deleted")
	}
	prefix := tenantPrefix(t)
	m.mu.Lock()
	defer m.mu.Unlock()
	for path := range m.data {
		if strings.HasPrefix(path, prefix) {
			delete(m.data, path)
		}
	}
	return nil
}

func (m *memory) Tenants(ctx context.Context) ([]Tenant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[Tenant]bool)
	for path := range m.data {
		prefix, _, _ := strings.Cut(path, tenantSep)
		id, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("store: tenants: corrupt key prefix of length %d", len(prefix))
		}
		if t := Tenant(id); t != SystemTenant {
			seen[t] = true
		}
	}
	tenants := make([]Tenant, 0, len(seen))
	for t := range seen {
		tenants = append(tenants, t)
	}
	slices.Sort(tenants)
	return tenants, nil
}
