package records_test

import (
	"context"
	"maps"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestDriftIssue(t *testing.T) {
	forEachBackend(t, 1, func(t *testing.T, clients []store.Store) {
		ctx, s := context.Background(), clients[0]
		put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "d1", ApprovedCommit: "c1"})

		got, err := records.DriftOpened(ctx, s, tenant, repoName, 11, "h1")
		if err != nil || got != 11 {
			t.Fatalf("DriftOpened = %d, %v; want 11", got, err)
		}
		// A second delivery that opened its own issue finds the first.
		if got, err := records.DriftOpened(ctx, s, tenant, repoName, 12, "h1"); err != nil || got != 11 {
			t.Fatalf("second DriftOpened = %d, %v; want 11", got, err)
		}
		if err := records.DriftUpdated(ctx, s, tenant, repoName, 11, "h2"); err != nil {
			t.Fatal(err)
		}
		// Another issue's update changes nothing.
		if err := records.DriftUpdated(ctx, s, tenant, repoName, 12, "h3"); err != nil {
			t.Fatal(err)
		}
		r := load[records.Repo](t, s, records.RepoKey(repoName))
		if r.DriftIssue != 11 || r.DriftHead != "h2" || r.ApprovedDigest != "d1" || r.ApprovedCommit != "c1" {
			t.Errorf("repo = %+v", r)
		}

		if err := records.DriftClosed(ctx, s, tenant, repoName, 12); err != nil {
			t.Fatal(err)
		}
		if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.DriftIssue != 11 {
			t.Errorf("closing another issue forgot the recorded one: %+v", r)
		}
		if err := records.DriftClosed(ctx, s, tenant, repoName, 11); err != nil {
			t.Fatal(err)
		}
		r = load[records.Repo](t, s, records.RepoKey(repoName))
		if r.DriftIssue != 0 || r.DriftHead != "" || r.ApprovedDigest != "d1" {
			t.Errorf("repo after close = %+v", r)
		}
		if _, err := records.DriftOpened(ctx, s, tenant, repoName, 0, "h1"); err == nil {
			t.Error("issue 0 was recorded")
		}
	})
}

func TestADriftIssueCreatesAMissingRepositoryRecord(t *testing.T) {
	ctx, s := context.Background(), store.NewMemory()
	if got, err := records.DriftOpened(ctx, s, tenant, repoName, 7, "h1"); err != nil || got != 7 {
		t.Fatalf("DriftOpened = %d, %v", got, err)
	}
	if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.DriftIssue != 7 || r.ApprovedDigest != "" {
		t.Errorf("repo = %+v", r)
	}
}

func TestExpiryIssues(t *testing.T) {
	forEachBackend(t, 1, func(t *testing.T, clients []store.Store) {
		ctx, s := context.Background(), clients[0]
		const a, b = "exception graph x y 2026-11-19", "exception unowned web/** 2026-12-01"
		for _, step := range []struct {
			key         string
			issue, want int
		}{{a, 21, 21}, {b, 22, 22}, {a, 23, 21}} {
			if got, err := records.ExpiryOpened(ctx, s, tenant, repoName, step.key, step.issue); err != nil || got != step.want {
				t.Fatalf("ExpiryOpened(%q, %d) = %d, %v; want %d", step.key, step.issue, got, err, step.want)
			}
		}
		if err := records.ExpiryClosed(ctx, s, tenant, repoName, a, 23); err != nil {
			t.Fatal(err)
		}
		r := load[records.Repo](t, s, records.RepoKey(repoName))
		if !maps.Equal(r.ExpiryIssues, map[string]int{a: 21, b: 22}) {
			t.Errorf("closing another issue changed the map: %v", r.ExpiryIssues)
		}
		for key, issue := range map[string]int{a: 21, b: 22} {
			if err := records.ExpiryClosed(ctx, s, tenant, repoName, key, issue); err != nil {
				t.Fatal(err)
			}
		}
		if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.ExpiryIssues != nil {
			t.Errorf("issues left: %v", r.ExpiryIssues)
		}
		if _, err := records.ExpiryOpened(ctx, s, tenant, repoName, "", 5); err == nil {
			t.Error("an empty key was recorded")
		}
	})
}

// TestAnUnchangedRepositoryRecordIsKept: a delivery of the same drift head again leaves the bytes as they are.
func TestAnUnchangedRepositoryRecordIsKept(t *testing.T) {
	ctx, s := context.Background(), store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{DriftIssue: 11, DriftHead: "h1"})
	before, err := s.Get(ctx, tenant, records.RepoKey(repoName))
	if err != nil {
		t.Fatal(err)
	}
	if err := records.DriftUpdated(ctx, s, tenant, repoName, 11, "h1"); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.Get(ctx, tenant, records.RepoKey(repoName)); string(after) != string(before) {
		t.Errorf("record changed: %s", after)
	}
}
