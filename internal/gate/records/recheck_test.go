package records_test

import (
	"context"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestRechecked(t *testing.T) {
	rc := records.Recheck{Proposal: "prop1", Path: "updated", Unmet: "base_changed", HeadSHA: "head1"}
	tests := []struct {
		name    string
		current *records.Pull // nil: no record
	}{
		{"the record of the run the re-check read", &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 1, Status: "waiting", Errors: []string{"e1"}, RequestKeys: []string{"k1"}}},
		{"a newer attempt uploaded meanwhile", &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 2, Status: "pass"}},
		{"a newer head uploaded meanwhile", &records.Pull{HeadSHA: "head2", RunID: 10, RunAttempt: 1, Status: "fail"}},
		{"no record", nil},
	}
	for _, b := range backends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				s := b.clients(t, 1)[0]
				key := records.PullKey(repoName, 12)
				if tc.current != nil {
					raw, err := records.Encode(tc.current)
					if err != nil {
						t.Fatal(err)
					}
					if err := s.Put(ctx, tenant, key, raw); err != nil {
						t.Fatal(err)
					}
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
				want := *tc.current
				want.Schema, want.Recheck = records.SchemaPull, &rc
				if got.Recheck == nil || *got.Recheck != rc || got.HeadSHA != want.HeadSHA || got.RunAttempt != want.RunAttempt ||
					got.Status != want.Status || len(got.Errors) != len(want.Errors) {
					t.Errorf("record = %+v, want %+v with the recheck", got, want)
				}
			})
		}
	}
	t.Run("an empty recheck is refused", func(t *testing.T) {
		if _, err := records.Rechecked(context.Background(), store.NewMemory(), tenant, repoName, 12, records.Recheck{}); err == nil {
			t.Error("written")
		}
	})
}
