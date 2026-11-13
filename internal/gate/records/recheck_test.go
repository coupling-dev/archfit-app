package records_test

import (
	"context"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestRechecked(t *testing.T) {
	was := records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 1, Status: "waiting", Errors: []string{"e1"}, RequestKeys: []string{"k1"}}
	rc := records.Recheck{Proposal: "prop1", Path: "updated", Unmet: "base_changed", HeadSHA: "head1"}
	tests := []struct {
		name    string
		current *records.Pull // nil: no record
		want    bool
	}{
		{"the record still names the run", &was, true},
		{"a newer attempt uploaded", &records.Pull{HeadSHA: "head1", RunID: 9, RunAttempt: 2, Status: "pass"}, false},
		{"a newer head uploaded", &records.Pull{HeadSHA: "head2", RunID: 10, RunAttempt: 1, Status: "fail"}, false},
		{"no record", nil, false},
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

				ok, err := records.Rechecked(ctx, s, tenant, repoName, 12, was, rc)
				if err != nil || ok != tc.want {
					t.Fatalf("Rechecked = %v, %v; want %v", ok, err, tc.want)
				}
				var got records.Pull
				err = records.Load(ctx, s, tenant, key, &got)
				switch {
				case tc.current == nil:
					if err == nil {
						t.Errorf("a record was created: %+v", got)
					}
				case err != nil:
					t.Fatal(err)
				case tc.want && (got.Recheck == nil || *got.Recheck != rc || got.Status != was.Status || len(got.Errors) != 1):
					t.Errorf("record = %+v, want the same record with the recheck", got)
				case !tc.want && (got.Recheck != nil || got.RunAttempt != tc.current.RunAttempt || got.HeadSHA != tc.current.HeadSHA):
					t.Errorf("record = %+v, want the newer upload kept", got)
				}
			})
		}
	}
	t.Run("an empty recheck is refused", func(t *testing.T) {
		if _, err := records.Rechecked(context.Background(), store.NewMemory(), tenant, repoName, 12, was, records.Recheck{}); err == nil {
			t.Error("written")
		}
	})
}
