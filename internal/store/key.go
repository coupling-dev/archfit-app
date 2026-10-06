package store

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	maxKeyLen   = 512
	maxValueLen = 64 << 10
	maxSwaps    = 10
	tenantSep   = "/"
)

// keyPath is the storage layout <tenant>/<key>. The separator follows the full decimal tenant,
// so the prefix of tenant 1 never matches a key of tenant 12.
func keyPath(t Tenant, key string) string {
	return tenantPrefix(t) + key
}

func tenantPrefix(t Tenant) string {
	return strconv.FormatInt(int64(t), 10) + tenantSep
}

// opError builds every error of this package: tenant and key length only, never the key or the value.
func opError(op string, t Tenant, key, problem string) error {
	return fmt.Errorf("store: %s: tenant %d, key length %d: %s", op, t, len(key), problem)
}

func validateTenant(op string, t Tenant, key string) error {
	if t < 0 {
		return opError(op, t, key, "negative tenant")
	}
	return nil
}

func validateKey(op string, t Tenant, key string) error {
	if err := validateTenant(op, t, key); err != nil {
		return err
	}
	switch {
	case key == "":
		return opError(op, t, key, "empty key")
	case len(key) > maxKeyLen:
		return opError(op, t, key, "key longer than "+strconv.Itoa(maxKeyLen)+" bytes")
	case strings.ContainsRune(key, 0):
		return opError(op, t, key, "key contains a NUL byte")
	}
	return nil
}

// validateSwaps checks the input of CompareAndSwapAll: 1 to maxSwaps swaps, valid keys, no key twice, values
// within the limit.
func validateSwaps(op string, t Tenant, swaps []Swap) error {
	if err := validateTenant(op, t, ""); err != nil {
		return err
	}
	if len(swaps) == 0 || len(swaps) > maxSwaps {
		return fmt.Errorf("store: %s: tenant %d: %d swaps, want 1 to %d", op, t, len(swaps), maxSwaps)
	}
	seen := make(map[string]bool, len(swaps))
	for _, s := range swaps {
		if err := validateKey(op, t, s.Key); err != nil {
			return err
		}
		if seen[s.Key] {
			return opError(op, t, s.Key, "key appears twice")
		}
		seen[s.Key] = true
		if len(s.Value) > maxValueLen {
			return opError(op, t, s.Key, "value longer than "+strconv.Itoa(maxValueLen)+" bytes")
		}
	}
	return nil
}
