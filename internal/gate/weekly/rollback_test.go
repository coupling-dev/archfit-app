package weekly

import (
	"context"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestOldPullIndexSurvivesSummaryWrites(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	old := []byte(`{"schema":"archfit-app.pull-index.v1","entries":[{"key":"7","id":"abc"}]}`)
	indexKey := records.PullIndexKey("owner/repo")
	if err := s.Put(ctx, 1, indexKey, old); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, s, 1, "owner/repo", 7, 123, 1, time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), "pass"); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Get(ctx, 1, indexKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(old) {
		t.Fatalf("old index changed: %s", raw)
	}
	index := records.Index{Schema: records.SchemaPullIndex}
	if err := records.Decode(raw, &index); err != nil {
		t.Fatal(err)
	}
	if got, ok := index.Lookup("7"); !ok || got != "abc" {
		t.Fatalf("old index lookup: %q %v", got, ok)
	}
}
