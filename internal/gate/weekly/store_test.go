package weekly

import (
	"context"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestRecordedSummary(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	got, err := Read(ctx, s, 1, "owner/repo", now)
	if err != nil || got.Known {
		t.Fatalf("absent: %+v, %v", got, err)
	}
	if err := Record(ctx, s, 1, "owner/repo", 7, 100, 1, now, "pass"); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, s, 1, "owner/repo", 7, 100, 1, now.Add(time.Hour), "owner"); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, s, 1, "owner/repo", 7, 99, 1, now.Add(time.Hour), "owner"); err != nil {
		t.Fatal(err)
	}
	got, err = Read(ctx, s, 1, "owner/repo", now)
	if err != nil || got != (Summary{Checked: 1, Passed: 1, Known: true}) {
		t.Fatalf("retry: %+v, %v", got, err)
	}
	got, err = Read(ctx, s, 1, "owner/repo", now.Add(7*24*time.Hour))
	if err != nil || got != (Summary{Known: true}) {
		t.Fatalf("expiry: %+v, %v", got, err)
	}
	got, err = Read(ctx, s, 2, "owner/repo", now)
	if err != nil || got.Known {
		t.Fatalf("isolation: %+v, %v", got, err)
	}
	if err := s.Put(ctx, 1, key("owner/repo"), []byte(`{"schema":"bad"}`)); err != nil {
		t.Fatal(err)
	}
	got, err = Read(ctx, s, 1, "owner/repo", now)
	if err == nil || got.Known {
		t.Fatalf("corrupt: %+v, %v", got, err)
	}
}
