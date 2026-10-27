package inputs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

// A local coding agent pushes with its owner's credentials, so an owner's push must never be an approval.
func TestOwnerPushNeverApproves(t *testing.T) {
	tests := []struct {
		name   string
		pusher string
	}{
		{"owner", "alice"},
		{"member of an owner team", "carol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = []string{policy.BaselinePath}
			if err := New(f.repos, f.pulls, f.store).RecordPusher(context.Background(), pullTarget(), tt.pusher); err != nil {
				t.Fatalf("RecordPusher: %v", err)
			}

			got := f.mustResolve(t, pullTarget())
			wantApproval(t, got, ApprovalMissing, "")
			wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline))
			wantStrings(t, "head reads", f.repos.headReads(), nil)
		})
	}
}

func TestPusherRecordIsBounded(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	r := New(f.repos, f.pulls, f.store)
	for i := range maxPushers + 5 {
		if err := r.RecordPusher(context.Background(), pullTarget(), fmt.Sprintf("user%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := f.store.Get(context.Background(), store.Tenant(installation), pusherKey(pullTarget()))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(string(raw), pusherSep)); n != maxPushers {
		t.Errorf("record holds %d logins, want the cap %d", n, maxPushers)
	}
}

func TestPusherRecordFailuresAreErrors(t *testing.T) {
	f := newFixture()
	f.store = &stubStore{get: func(context.Context) ([]byte, error) { return nil, errBoom }}

	err := New(f.repos, f.pulls, f.store).RecordPusher(context.Background(), pullTarget(), "alice")
	if !errors.Is(err, errBoom) {
		t.Fatalf("RecordPusher error = %v, want the store error", err)
	}
}
