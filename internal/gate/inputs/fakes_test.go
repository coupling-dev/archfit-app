package inputs

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/store"
)

// stubStore answers Get only; any other Store method panics, which proves the paths that use it only read.
type stubStore struct {
	store.Store
	get     func(ctx context.Context) ([]byte, error)
	tenants []store.Tenant
	keys    []string
}

func (s *stubStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.tenants = append(s.tenants, t)
	s.keys = append(s.keys, key)
	return s.get(ctx)
}
