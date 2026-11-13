package records_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestRechecked(t *testing.T) {
	earlier := records.Recheck{Proposal: "prop0", Path: "rerun", HeadSHA: "head0"}
	rc := records.Recheck{Proposal: "prop1", Path: "updated", Unmet: "base_changed", HeadSHA: "head1"}
	tests := []struct {
		name    string
		current *records.Pull // nil: no record
		want    []records.Recheck
	}{
		{"the record of the run read", &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 1, Status: "waiting", Errors: []string{"e1"}, RequestKeys: []string{"k1"}}, []records.Recheck{rc}},
		{"a newer attempt uploaded meanwhile", &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 2, Status: "pass"}, []records.Recheck{rc}},
		{"a newer head uploaded meanwhile", &records.Pull{HeadSHA: "head2", RunID: 10, RunAttempt: 1, Status: "fail"}, []records.Recheck{rc}},
		{"an earlier proposal's marker is kept", &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 1, Status: "waiting", Rechecks: []records.Recheck{earlier}}, []records.Recheck{earlier, rc}},
		{"no record", nil, nil},
	}
	for _, b := range backends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				s := b.clients(t, 1)[0]
				key := records.PullKey(repoName, 12)
				if tc.current != nil {
					put(t, s, key, tc.current)
				}

				ok, err := records.Rechecked(ctx, s, tenant, repoName, 12, rc)
				if err != nil || ok != (tc.current != nil) {
					t.Fatalf("Rechecked = %v, %v; want written %v", ok, err, tc.current != nil)
				}
				var got records.Pull
				err = records.Load(ctx, s, tenant, key, &got)
				if tc.current == nil {
					if err == nil {
						t.Errorf("a record was created: %+v", got)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(got.Rechecks, tc.want) || got.HeadSHA != tc.current.HeadSHA || got.RunAttempt != tc.current.RunAttempt ||
					got.Status != tc.current.Status || len(got.Errors) != len(tc.current.Errors) {
					t.Errorf("record = %+v, want %+v with rechecks %+v", got, tc.current, tc.want)
				}
				if !got.Rechecked(rc.Proposal) || got.Rechecked("other") {
					t.Errorf("Rechecked(%q) = %v, Rechecked(other) = %v", rc.Proposal, got.Rechecked(rc.Proposal), got.Rechecked("other"))
				}
			})
		}
	}
	t.Run("the oldest marker goes first past the bound", func(t *testing.T) {
		ctx := context.Background()
		s := store.NewMemory()
		key := records.PullKey(repoName, 12)
		put(t, s, key, &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 1, Status: "waiting"})
		for i := range records.MaxRechecks + 1 {
			if _, err := records.Rechecked(ctx, s, tenant, repoName, 12, records.Recheck{Proposal: "p" + strconv.Itoa(i), Path: "rerun"}); err != nil {
				t.Fatal(err)
			}
		}
		var got records.Pull
		if err := records.Load(ctx, s, tenant, key, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Rechecks) != records.MaxRechecks || got.Rechecked("p0") || !got.Rechecked("p1") || !got.Rechecked("p"+strconv.Itoa(records.MaxRechecks)) {
			t.Errorf("rechecks = %+v, want the latest %d", got.Rechecks, records.MaxRechecks)
		}
	})
	t.Run("an empty recheck is refused", func(t *testing.T) {
		if _, err := records.Rechecked(context.Background(), store.NewMemory(), tenant, repoName, 12, records.Recheck{}); err == nil {
			t.Error("written")
		}
	})
}
