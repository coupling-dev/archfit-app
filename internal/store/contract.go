// Package store is the only persistence seam of the App: a tenant-scoped key-value store.
package store

import (
	"context"
	"errors"
)

// Tenant is the authenticated GitHub installation ID that owns a key.
// Every key is stored under its tenant prefix; no operation crosses tenants.
type Tenant int64

// SystemTenant holds App-wide keys (the managed-LLM budget counter). It is never a GitHub installation ID.
const SystemTenant Tenant = 0

// ErrNotFound is returned by Get when the key does not exist for the tenant.
var ErrNotFound = errors.New("store: key not found")

// Store is the only persistence seam of the App. Values are opaque bytes.
// Implementations must be safe for concurrent use.
type Store interface {
	Get(ctx context.Context, t Tenant, key string) ([]byte, error)
	Put(ctx context.Context, t Tenant, key string, value []byte) error
	Delete(ctx context.Context, t Tenant, key string) error // deleting a missing key is not an error
	// Add atomically adds delta to the integer at key (missing = 0) and returns the new value.
	Add(ctx context.Context, t Tenant, key string, delta int64) (int64, error)
	// CompareAndSwap atomically sets key to value when it currently holds exactly old, and reports whether it did.
	// A nil old matches only a missing key; a nil value deletes the key. A false result changes nothing.
	CompareAndSwap(ctx context.Context, t Tenant, key string, old, value []byte) (bool, error)
	// DeleteTenant removes every key of the tenant. Idempotent.
	DeleteTenant(ctx context.Context, t Tenant) error
	// Tenants lists every tenant that owns at least one key, SystemTenant excluded.
	// Used only by the sweep to forget installations whose uninstall webhook was missed.
	Tenants(ctx context.Context) ([]Tenant, error)
}
