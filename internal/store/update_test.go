package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/alexei-led/archfit-app/internal/store"
)

// racingStore runs interfere before each of its first n CompareAndSwapAll calls: another writer between Update's
// read and its swap.
type racingStore struct {
	store.Store
	n          int
	interfere  func()
	casAllRuns int
}

func (r *racingStore) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	r.casAllRuns++
	if r.casAllRuns <= r.n {
		r.interfere()
	}
	return r.Store.CompareAndSwapAll(ctx, t, swaps)
}

func get(t *testing.T, s store.Store, tn store.Tenant, key string) []byte {
	t.Helper()
	v, err := s.Get(context.Background(), tn, key)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return v
}

func TestUpdateWritesEveryKeyFromTheBytesItRead(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	for k, v := range map[string]string{"keep": "k", "drop": "d", "edit": "1"} {
		if err := s.Put(ctx, 7, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	keys := []string{"new", "keep", "drop", "edit"}
	var seen [][]byte
	err := store.Update(ctx, s, 7, keys, func(cur [][]byte) ([][]byte, error) {
		seen = cur
		return [][]byte{[]byte("n"), cur[1], nil, []byte("2")}, nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if seen[0] != nil || string(seen[1]) != "k" || string(seen[2]) != "d" || string(seen[3]) != "1" {
		t.Errorf("change saw %q, want [nil k d 1]", seen)
	}
	want := map[string]string{"new": "n", "keep": "k", "edit": "2"}
	for k, v := range want {
		if got := get(t, s, 7, k); string(got) != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if got := get(t, s, 7, "drop"); got != nil {
		t.Errorf("drop = %q, want deleted", got)
	}
}

func TestUpdateComparesAgainstWhatItReadNotWhatChangeEdited(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	if err := s.Put(ctx, 7, "a", []byte("old")); err != nil {
		t.Fatal(err)
	}
	err := store.Update(ctx, s, 7, []string{"a"}, func(cur [][]byte) ([][]byte, error) {
		copy(cur[0], "new")
		return cur, nil
	})
	if err != nil {
		t.Fatalf("Update with a change that edits its input = %v, want nil", err)
	}
	if got := get(t, s, 7, "a"); string(got) != "new" {
		t.Errorf("a = %q, want new", got)
	}
}

func TestUpdateChangeErrorWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	boom := errors.New("boom")
	err := store.Update(ctx, s, 7, []string{"a"}, func([][]byte) ([][]byte, error) {
		return [][]byte{[]byte("x")}, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update = %v, want the change error", err)
	}
	if got := get(t, s, 7, "a"); got != nil {
		t.Errorf("a = %q, want missing", got)
	}
}

func TestUpdateRetriesFromFreshBytesAfterALostSwap(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.Put(ctx, 7, "n", []byte("1")); err != nil {
		t.Fatal(err)
	}
	s := &racingStore{Store: mem, n: 2, interfere: func() {
		v := get(t, mem, 7, "n")
		i, _ := strconv.Atoi(string(v))
		if err := mem.Put(ctx, 7, "n", []byte(strconv.Itoa(i+10))); err != nil {
			t.Fatal(err)
		}
	}}
	calls := 0
	err := store.Update(ctx, s, 7, []string{"n"}, func(cur [][]byte) ([][]byte, error) {
		calls++
		i, _ := strconv.Atoi(string(cur[0]))
		return [][]byte{[]byte(strconv.Itoa(i + 1))}, nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if calls != 3 {
		t.Errorf("change ran %d times, want 3", calls)
	}
	if got := get(t, mem, 7, "n"); string(got) != "22" {
		t.Errorf("n = %q, want 22: both interfering writes kept and one increment on top", got)
	}
}

func TestUpdateGivesUpWithErrConflict(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	round := 0
	s := &racingStore{Store: mem, n: 1 << 30, interfere: func() {
		round++
		if err := mem.Put(ctx, 7, "a", []byte(strconv.Itoa(round))); err != nil {
			t.Fatal(err)
		}
	}}
	calls := 0
	err := store.Update(ctx, s, 7, []string{"a"}, func([][]byte) ([][]byte, error) {
		calls++
		return [][]byte{[]byte("mine")}, nil
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Update = %v, want ErrConflict", err)
	}
	if calls != 5 {
		t.Errorf("change ran %d times, want 5", calls)
	}
	if got := get(t, mem, 7, "a"); string(got) == "mine" {
		t.Error("a lost swap was written")
	}
}

func TestUpdateRefusesInvalidKeySetsAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "k" + strconv.Itoa(i)
	}
	tests := []struct {
		name string
		keys []string
	}{
		{"eleven keys", eleven},
		{"a key twice", []string{"a", "b", "a"}},
		{"an empty key", []string{"a", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			err := store.Update(ctx, s, 7, tt.keys, func(cur [][]byte) ([][]byte, error) {
				next := make([][]byte, len(cur))
				for i := range next {
					next[i] = []byte("v")
				}
				return next, nil
			})
			if err == nil {
				t.Fatal("Update = nil, want an error")
			}
			if ts, err := s.Tenants(ctx); err != nil || len(ts) != 0 {
				t.Errorf("Tenants = %v, %v; want nothing written", ts, err)
			}
		})
	}
}

func TestUpdateRefusesAWrongNumberOfValues(t *testing.T) {
	s := store.NewMemory()
	err := store.Update(context.Background(), s, 7, []string{"a", "b"}, func([][]byte) ([][]byte, error) {
		return [][]byte{[]byte("x")}, nil
	})
	if err == nil || errors.Is(err, store.ErrConflict) {
		t.Fatalf("Update = %v, want a usage error", err)
	}
	if got := get(t, s, 7, "a"); got != nil {
		t.Errorf("a = %q, want missing", got)
	}
}

func TestUpdateHonorsACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := store.Update(ctx, store.NewMemory(), 7, []string{"a"}, func(cur [][]byte) ([][]byte, error) {
		called = true
		return cur, nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("Update = %v (change called %v), want context.Canceled before change", err, called)
	}
}
