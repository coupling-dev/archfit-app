package store

import (
	"context"
	"testing"
)

func TestKeyPathLayout(t *testing.T) {
	tests := []struct {
		tenant Tenant
		key    string
		want   string
	}{
		{4711, "entitlement", "4711/entitlement"},
		{0, "llm-budget/2026-09", "0/llm-budget/2026-09"},
		{1, "consent/managed_llm", "1/consent/managed_llm"},
		{12, "a", "12/a"},
	}
	for _, tt := range tests {
		if got := keyPath(tt.tenant, tt.key); got != tt.want {
			t.Errorf("keyPath(%d, %q) = %q, want %q", tt.tenant, tt.key, got, tt.want)
		}
	}
}

func TestTenantPrefixDoesNotMatchLongerTenant(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	for _, tn := range []Tenant{1, 12, 123} {
		if err := s.Put(ctx, tn, "k", []byte("v")); err != nil {
			t.Fatalf("Put(%d): %v", tn, err)
		}
	}
	if err := s.DeleteTenant(ctx, 1); err != nil {
		t.Fatalf("DeleteTenant(1): %v", err)
	}
	for _, tn := range []Tenant{12, 123} {
		if got, err := s.Get(ctx, tn, "k"); err != nil || string(got) != "v" {
			t.Errorf("Get(%d) = %q, %v, want \"v\", nil", tn, got, err)
		}
	}
}

func TestCounterEncodingIsDecimalASCII(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if _, err := s.Add(ctx, 1, "k", 42); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got, err := s.Get(ctx, 1, "k"); err != nil || string(got) != "42" {
		t.Errorf("Get = %q, %v, want \"42\", nil", got, err)
	}
	if err := s.Put(ctx, 1, "k", []byte("7")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got, err := s.Add(ctx, 1, "k", 1); err != nil || got != 8 {
		t.Errorf("Add = %d, %v, want 8, nil", got, err)
	}
}

func TestMemoryCopiesBytes(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	in := []byte("value")
	if err := s.Put(ctx, 1, "k", in); err != nil {
		t.Fatalf("Put: %v", err)
	}
	in[0] = 'X'
	first, err := s.Get(ctx, 1, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(first) != "value" {
		t.Errorf("Get after mutating the Put slice = %q, want \"value\"", first)
	}
	first[0] = 'Y'
	if got, err := s.Get(ctx, 1, "k"); err != nil || string(got) != "value" {
		t.Errorf("Get after mutating a Get result = %q, %v, want \"value\", nil", got, err)
	}
}
