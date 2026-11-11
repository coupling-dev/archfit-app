package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// updateAttempts bounds the read-and-swap rounds of Update **[default]**. A writer that loses five rounds in a row is
// in a contention the caller should see.
const updateAttempts = 5

// ErrConflict is returned by Update when every attempt lost its compare-and-swap to another writer.
var ErrConflict = errors.New("store: update still in conflict")

// Update reads keys, lets change compute their next bytes, and writes them in one CompareAndSwapAll from the bytes
// it read. change gets the current bytes in the order of keys (nil for a missing key) and returns the next bytes in
// the same order: nil deletes the key, bytes equal to the current ones only check it. When a key changed between
// the read and the swap, Update reads again and calls change again, at most updateAttempts times, then returns
// ErrConflict. An error from change stops Update and is returned as is; nothing is written. The keys follow the
// limits of CompareAndSwapAll. change gets copies: what it does to them never changes what the swap compares against.
// Update is a caller-side loop over the Store, not a transaction inside it: change sees keys read one by one, and only
// the swap is atomic.
func Update(ctx context.Context, s Store, t Tenant, keys []string, change func(cur [][]byte) ([][]byte, error)) error {
	for range updateAttempts {
		cur := make([][]byte, len(keys))
		for i, k := range keys {
			v, err := s.Get(ctx, t, k)
			switch {
			case errors.Is(err, ErrNotFound):
			case err != nil:
				return err
			default:
				cur[i] = v
			}
		}
		view := make([][]byte, len(cur))
		for i, v := range cur {
			view[i] = bytes.Clone(v)
		}
		next, err := change(view)
		if err != nil {
			return err
		}
		if len(next) != len(keys) {
			return fmt.Errorf("store: update: change returned %d values for %d keys", len(next), len(keys))
		}
		swaps := make([]Swap, len(keys))
		for i, k := range keys {
			swaps[i] = Swap{Key: k, Old: cur[i], Value: next[i]}
		}
		ok, err := s.CompareAndSwapAll(ctx, t, swaps)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return ErrConflict
}
