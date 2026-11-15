package drift

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestPushStoresHomeDriftAndDistinguishesMissingPartialAndClean(t *testing.T) {
	w := newWorld(t)
	w.push("h0", nil)
	raw, err := records.Encode(ptrRepo(w.repoRecord()))
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]json.RawMessage
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if _, ok := before["drift"]; ok {
		t.Fatal("a missing repository block became evidence")
	}
	state := block(t, "repository_drift")
	w.push("h1", state)
	assertHomeDrift(t, w, "h1", len(state.Drift), len(state.Unread) > 0)
	w.push("h2", block(t, "repository_clean"))
	assertHomeDrift(t, w, "h2", 0, true)
	w.push("h3", noUnread(t, "repository_clean"))
	assertHomeDrift(t, w, "h3", 0, false)
	w.push("h4", nil)
	assertHomeDrift(t, w, "h3", 0, false)
}

func ptrRepo(r records.Repo) *records.Repo { return &r }

func assertHomeDrift(t *testing.T, w *world, head string, count int, unread bool) {
	t.Helper()
	raw, err := records.Encode(ptrRepo(w.repoRecord()))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Drift *struct {
			Head   string
			State  string
			Errors []struct{ Rule, From, To string }
			Unread bool
		}
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Drift == nil {
		t.Fatal("the push did not store its drift for Home")
	}
	if got.Drift.Head != head || len(got.Drift.Errors) != count || got.Drift.Unread != unread {
		t.Fatalf("drift = %+v, want head %s, %d errors, unread %v", got.Drift, head, count, unread)
	}
	if count > 0 {
		want := block(t, "repository_drift").Drift[0]
		if got.Drift.Errors[0].Rule != want.Rule || got.Drift.Errors[0].From != want.From || got.Drift.Errors[0].To != want.To {
			t.Fatalf("drift lost its rule or modules: %+v", got.Drift.Errors)
		}
	}
}

func TestHomeDriftIsStoredEvenWhenIssuesAreTurnedOff(t *testing.T) {
	w := newWorld(t)
	w.gh.failOn, w.gh.failErr = "CreateIssue", api.ErrNotFound
	w.push("h1", block(t, "repository_drift"))
	assertHomeDrift(t, w, "h1", 1, len(block(t, "repository_drift").Unread) > 0)
}

func TestHomeSnapshotWriteFailureIsReturnedAndRetryStoresIt(t *testing.T) {
	w := newWorld(t)
	w.push("h0", noUnread(t, "repository_clean"))
	before := w.repoRecord().Drift
	w.svc = New(w.gh, failingRecordStore{w.store})
	_, err := w.svc.Pushed(t.Context(), Run{Repo: repo, Head: "h1", State: noUnread(t, "repository_clean")})
	if !errors.Is(err, errFake) {
		t.Fatalf("Pushed = %v", err)
	}
	if !reflect.DeepEqual(w.repoRecord().Drift, before) {
		t.Fatal("failed write changed evidence")
	}
	w.svc = New(w.gh, w.store)
	w.push("h1", noUnread(t, "repository_clean"))
	assertHomeDrift(t, w, "h1", 0, false)
}

func TestHomeSnapshotDeduplicatesAndBoundsValidErrors(t *testing.T) {
	w := newWorld(t)
	state := block(t, "repository_drift")
	for range 2000 {
		state.Drift = append(state.Drift, state.Drift[0])
	}
	w.push("h1", state)
	if len(w.repoRecord().Drift.Errors) != 1 {
		t.Fatalf("repeated errors stored: %d", len(w.repoRecord().Drift.Errors))
	}
	for i := range state.Drift {
		state.Drift[i].From = fmt.Sprintf("module-%d-%s", i, strings.Repeat("a", 100))
	}
	w.push("h2", state)
	got := w.repoRecord().Drift
	if got == nil || !got.Unread || len(got.Errors) == 0 || len(got.Errors) >= len(state.Drift) {
		t.Fatalf("snapshot not bounded and marked incomplete: %+v", got)
	}
}
