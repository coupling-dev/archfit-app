package records_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestHomeDriftKeepsOldRecordsAndUnrelatedState(t *testing.T) {
	forEachBackend(t, 1, func(t *testing.T, clients []store.Store) {
		s := clients[0]
		old := []byte(`{"schema":"archfit-app.repo.v1","approved_digest":"approved","approved_commit":"c1","guarded_since":"c0","sweep_recheck":"p1","outside_flow":{"digest":"changed","issue":3},"drift_issue":7,"drift_digest":"body","expiry_issues":{"exception graph a b 2026-11-19":9}}`)
		var before records.Repo
		if err := records.Decode(old, &before); err != nil {
			t.Fatal(err)
		}
		encoded, err := records.Encode(&before)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, old) {
			t.Fatalf("an old record changed on round trip:\n%s\n%s", encoded, old)
		}
		if err := s.Put(t.Context(), tenant, records.RepoKey(repoName), old); err != nil {
			t.Fatal(err)
		}
		want := records.DriftState{Head: "h2", State: "drift", Errors: []records.DriftError{{Rule: "graph", From: "a", To: "b"}}, Unread: true}
		if err := records.StoreDrift(t.Context(), s, tenant, repoName, want); err != nil {
			t.Fatal(err)
		}
		after := load[records.Repo](t, s, records.RepoKey(repoName))
		if !reflect.DeepEqual(after.Drift, &want) {
			t.Fatalf("drift = %+v", after.Drift)
		}
		after.Drift = nil
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("unrelated state changed: %+v, want %+v", after, before)
		}
		if err := records.DriftClosed(t.Context(), s, tenant, repoName, 7); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(load[records.Repo](t, s, records.RepoKey(repoName)).Drift, &want) {
			t.Fatal("closing a notification lost repository evidence")
		}
	})
}

func TestHomeDriftStoreFailureDoesNotMakeEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := store.NewMemory()
	if err := records.StoreDrift(ctx, s, tenant, repoName, records.DriftState{Head: "h1", Errors: []records.DriftError{}}); err == nil {
		t.Fatal("canceled write succeeded")
	}
	if _, err := s.Get(t.Context(), tenant, records.RepoKey(repoName)); err == nil {
		t.Fatal("failed write created evidence")
	}
}

func TestHomeDriftStoresCleanAndPartialDifferently(t *testing.T) {
	s := store.NewMemory()
	for _, unread := range []bool{true, false} {
		state := records.DriftState{Head: "h1", State: "clean", Errors: []records.DriftError{}, Unread: unread}
		if err := records.StoreDrift(t.Context(), s, tenant, repoName, state); err != nil {
			t.Fatal(err)
		}
		got := load[records.Repo](t, s, records.RepoKey(repoName))
		if !reflect.DeepEqual(got.Drift, &state) {
			t.Fatalf("got %+v, want %+v", got.Drift, state)
		}
		raw, _ := records.Encode(&got)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields["drift"]) == 0 {
			t.Fatal("clean evidence was omitted")
		}
	}
}
