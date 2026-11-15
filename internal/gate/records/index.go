package records

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/alexei-led/archfit-app/internal/store"
)

// ErrExists is returned by Index.Add when the key is in the index with another id.
var ErrExists = errors.New("records: the index has the key with another id")

// Index stands in for the Store's missing list: one value that names the records of one kind, so no lookup scans
// keys. Its schema says which index it is:
//
//	RequestIndexKey   SchemaRequestIndex   request key → issue
//	ProposalIndexKey  SchemaProposalIndex  PullEntry(pr) → proposal id
//	PullIndexKey      SchemaPullIndex      PullEntry(pr) → head SHA
//	ReposKey          SchemaRepos          repository full name → (empty)
type Index struct {
	Schema  string  `json:"schema"`
	Entries []Entry `json:"entries,omitempty"` // sorted by Key, each key once
}

// Entry is one key of an index and the id of its record.
type Entry struct {
	Key string `json:"key"`
	ID  string `json:"id,omitempty"`
}

// stamp keeps the index's own schema: the caller names the index it reads.
func (x *Index) stamp() string { return x.Schema }

func (x *Index) check() error {
	if !slices.IsSortedFunc(x.Entries, compareEntries) ||
		len(slices.CompactFunc(slices.Clone(x.Entries), func(a, b Entry) bool { return a.Key == b.Key })) != len(x.Entries) {
		return errors.New("records: index entries are not sorted and unique")
	}
	return nil
}

func compareEntries(a, b Entry) int { return cmp.Compare(a.Key, b.Key) }

func (x *Index) find(key string) (int, bool) {
	return slices.BinarySearchFunc(x.Entries, Entry{Key: key}, compareEntries)
}

// Lookup returns the id of key.
func (x *Index) Lookup(key string) (string, bool) {
	i, ok := x.find(key)
	if !ok {
		return "", false
	}
	return x.Entries[i].ID, true
}

// Add puts key → id. The same entry again changes nothing; the key with another id is ErrExists.
func (x *Index) Add(key, id string) error {
	i, ok := x.find(key)
	if ok {
		if x.Entries[i].ID != id {
			return ErrExists
		}
		return nil
	}
	x.Entries = slices.Insert(x.Entries, i, Entry{Key: key, ID: id})
	return nil
}

// Remove deletes key; a missing key is no change.
func (x *Index) Remove(key string) {
	if i, ok := x.find(key); ok {
		x.Entries = slices.Delete(x.Entries, i, i+1)
	}
}

// readIndex decodes the index bytes of a Store read: nil is an empty index of that schema.
func readIndex(raw []byte, schema string) (*Index, error) {
	x := &Index{Schema: schema}
	if raw == nil {
		return x, nil
	}
	if err := Decode(raw, x); err != nil {
		return nil, err
	}
	return x, nil
}

// writeIndex returns the next bytes of an index: the current bytes when its entries did not change, so an
// unchanged index is only checked, and a missing one stays missing.
func writeIndex(cur []byte, before []Entry, x *Index) ([]byte, error) {
	if slices.Equal(before, x.Entries) {
		return cur, nil
	}
	return Encode(x)
}

// Indexed changes one record and its index together, in one CompareAndSwapAll of two keys. edit gets the current
// record bytes (nil when missing) and the index (empty when missing); it changes the index in place and returns the
// next record bytes (nil deletes; the current bytes only check it). A lost swap reads again and calls edit again
// (store.Update); an error from edit writes nothing.
func Indexed(ctx context.Context, s store.Store, t store.Tenant, recordKey, indexKey, indexSchema string,
	edit func(record []byte, index *Index) ([]byte, error)) error {
	return store.Update(ctx, s, t, []string{recordKey, indexKey}, func(cur [][]byte) ([][]byte, error) {
		x, err := readIndex(cur[1], indexSchema)
		if err != nil {
			return nil, err
		}
		before := slices.Clone(x.Entries)
		rec, err := edit(cur[0], x)
		if err != nil {
			return nil, err
		}
		idx, err := writeIndex(cur[1], before, x)
		if err != nil {
			return nil, err
		}
		return [][]byte{rec, idx}, nil
	})
}

// Load reads the value at key into v; a missing key is ErrNoRecord.
func Load(ctx context.Context, s store.Store, t store.Tenant, key string, v Value) error {
	raw, err := s.Get(ctx, t, key)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoRecord
	}
	if err != nil {
		return err
	}
	return Decode(raw, v)
}
