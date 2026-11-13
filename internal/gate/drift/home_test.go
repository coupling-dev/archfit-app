package drift

import (
	"encoding/json"
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
