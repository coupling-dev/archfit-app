package gate

import (
	"context"
	"sync"

	"github.com/coupling-dev/archfit-app/internal/gate/inputs"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

type recordMemoKey struct{}

type repoRecordRead struct {
	installation int64
	fullName     string
}

type repoRecordEntry struct {
	once sync.Once
	rec  records.Repo
	err  error
}

type recordMemo struct {
	mu      sync.Mutex
	entries map[repoRecordRead]*repoRecordEntry
}

func (m *recordMemo) entry(k repoRecordRead) *repoRecordEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[k] == nil {
		m.entries[k] = &repoRecordEntry{}
	}
	return m.entries[k]
}

// WithRequestMemo makes the reads of the installation's admission and of a repository's record happen once for the
// rest of the request that ctx belongs to. A failed read is remembered too, so the request fails closed the same way
// at every step. Install it only on a request that writes neither: a later read would not see the change. The memo
// dies with the request and is never shared between requests. Installing it twice keeps the first.
func WithRequestMemo(ctx context.Context) context.Context {
	ctx = inputs.WithAdmissionMemo(ctx)
	if _, ok := ctx.Value(recordMemoKey{}).(*recordMemo); ok {
		return ctx
	}
	return context.WithValue(ctx, recordMemoKey{}, &recordMemo{entries: map[repoRecordRead]*repoRecordEntry{}})
}

// loadRepoRecord reads the repository's record, once per request under WithRequestMemo. The record is shared by the
// callers of that request: they must not change it. A missing record is records.ErrNoRecord with a zero record.
func loadRepoRecord(ctx context.Context, s store.Store, r api.RepoRef) (records.Repo, error) {
	load := func() (records.Repo, error) {
		var rec records.Repo
		err := records.Load(ctx, s, store.Tenant(r.InstallationID), records.RepoKey(r.FullName), &rec)
		return rec, err
	}
	memo, ok := ctx.Value(recordMemoKey{}).(*recordMemo)
	if !ok {
		return load()
	}
	e := memo.entry(repoRecordRead{r.InstallationID, r.FullName})
	e.once.Do(func() { e.rec, e.err = load() })
	return e.rec, e.err
}
