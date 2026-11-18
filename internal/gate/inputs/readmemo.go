package inputs

import (
	"context"
	"errors"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// The reads of one owner review that several of its probes share. The keys are the same for every caller, so a read
// made by one caller answers the others.
type (
	// FileRead is File of Path at At in Repo. Every loader stores the raw answer, so an absent file is nil bytes with
	// api.ErrNotFound: each caller decides whether that is an error.
	FileRead struct {
		Repo api.RepoRef
		At   string
		Path string
	}
	// BranchRead is BranchHead of Branch in Repo.
	BranchRead struct {
		Repo   api.RepoRef
		Branch string
	}
	// PullRead is Pull Number in Repo.
	PullRead struct {
		Repo   api.RepoRef
		Number int
	}
	// storeRead is Get of Key under Tenant.
	storeRead struct {
		tenant store.Tenant
		key    string
	}
)

type readMemoKey struct{}

// readMemo holds one request's reads. Each key has an entry: the first caller runs the read, the others wait for it and
// take its result, a failure included. The wait is a channel receive, not a lock, so it stays visible to the test
// clock (testing/synctest) while the read sleeps.
type readMemo struct {
	mu      sync.Mutex
	entries map[any]*readEntry
}

type readEntry struct {
	done chan struct{}
	val  any
	err  error
}

// errNoResult is what the others get when the read that owns the entry did not return, for example because it panicked.
var errNoResult = errors.New("inputs: the read did not return")

// errKeyType is what a caller gets when another caller stored a different type under its key.
var errKeyType = errors.New("inputs: the key holds another type")

// claim returns the entry of key and whether the caller owns it, so it must run the read.
func (m *readMemo) claim(key any) (*readEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[key]; ok {
		return e, false
	}
	e := &readEntry{done: make(chan struct{}), err: errNoResult}
	m.entries[key] = e
	return e, true
}

// WithReadMemo makes the reads of the owner review that ctx belongs to happen once each, through Memo and MemoStore,
// for the rest of that request. Install it only on a request that writes nothing: a later read would not see the
// change. The memo dies with the request and is never shared between requests. Installing it twice keeps the first.
func WithReadMemo(ctx context.Context) context.Context {
	if _, ok := ctx.Value(readMemoKey{}).(*readMemo); ok {
		return ctx
	}
	return context.WithValue(ctx, readMemoKey{}, &readMemo{entries: map[any]*readEntry{}})
}

// Memo calls load once per key for the request that ctx belongs to and returns its result to every caller. Without
// WithReadMemo it calls load each time. The value is shared: callers must not change it, and every caller of a key must
// use the same T (another type fails with errKeyType).
func Memo[T any](ctx context.Context, key any, load func() (T, error)) (T, error) {
	m, ok := ctx.Value(readMemoKey{}).(*readMemo)
	if !ok {
		return load()
	}
	e, owner := m.claim(key)
	if owner {
		defer close(e.done)
		v, err := load()
		e.val, e.err = v, err
	} else {
		<-e.done
	}
	v, ok := e.val.(T)
	if e.err == nil && e.val != nil && !ok {
		return v, errKeyType
	}
	return v, e.err
}

// MemoStore is s with Get answered through Memo. Every other method is s's.
func MemoStore(s store.Store) store.Store { return memoStore{s} }

type memoStore struct{ store.Store }

func (m memoStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	return Memo(ctx, storeRead{t, key}, func() ([]byte, error) { return m.Store.Get(ctx, t, key) })
}
