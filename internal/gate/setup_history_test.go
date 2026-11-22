package gate

import (
	"errors"
	"reflect"
	"testing"

	"encoding/json"
	"github.com/coupling-dev/archfit-app/internal/gate/inputs"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestSetupPolicyHistory(t *testing.T) {
	w := newMemoWorld(t)
	w.unrecorded()
	ctx := t.Context()
	got, err := w.gate.SetupPolicyHistory(ctx, w.shop)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty history: %#v, %v", got, err)
	}
	changes := []records.PolicyChange{{Commit: "one", PullRequest: 41, Merger: "ada", Requests: []int{37}}, {Commit: "two", Direct: true}}
	for _, c := range changes {
		if err := records.Unguarded(ctx, w.mem, store.Tenant(w.shop.InstallationID), w.shop.FullName, records.Push{After: c.Commit}, "digest", c); err != nil {
			t.Fatal(err)
		}
	}
	got, err = w.gate.SetupPolicyHistory(ctx, w.shop)
	want := []SetupPolicyChange{{Commit: "one", PullRequest: 41, Merger: "ada", Requests: []int{37}}, {Commit: "two", Direct: true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, %v; want %#v", got, err, want)
	}
	state, err := w.gate.SetupState(ctx, w.shop)
	if err != nil || state.ApprovedDigest != "" {
		t.Fatalf("history supplied approval: %+v, %v", state, err)
	}
	other := w.shop
	other.FullName = "acme/other"
	got, err = w.gate.SetupPolicyHistory(ctx, other)
	if err != nil || len(got) != 0 {
		t.Fatalf("other repo = %#v, %v", got, err)
	}
	other = w.shop
	other.InstallationID++
	if got, err = w.gate.SetupPolicyHistory(ctx, other); err == nil {
		t.Fatalf("unadmitted installation = %#v", got)
	}
	raw, err := json.Marshal(inputs.Admission{Account: "acme", By: "operator", ByID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.mem.Put(ctx, store.Tenant(other.InstallationID), inputs.AdmissionKey, raw); err != nil {
		t.Fatal(err)
	}
	if err := records.Unguarded(ctx, w.mem, store.Tenant(other.InstallationID), other.FullName, records.Push{After: "other"}, "digest", records.PolicyChange{Commit: "other"}); err != nil {
		t.Fatal(err)
	}
	got, err = w.gate.SetupPolicyHistory(ctx, other)
	if err != nil || len(got) != 1 || got[0].Commit != "other" {
		t.Fatalf("other installation = %#v, %v", got, err)
	}
	down := errors.New("store down")
	w.counts.fail[records.PolicyHistoryKey(w.shop.FullName)] = down
	if got, err = w.gate.SetupPolicyHistory(ctx, w.shop); !errors.Is(err, down) || got != nil {
		t.Fatalf("read error = %#v, %v", got, err)
	}
}
