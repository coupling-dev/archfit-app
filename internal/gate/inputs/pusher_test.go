package inputs

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestPusherAuthorizesOnlyABaseOwnerOfTheExactHead(t *testing.T) {
	tests := []struct {
		name         string
		recordedSHA  string
		pusher       string
		wantApproval Approval
		wantBy       string
	}{
		{"owner pushed the head", headSHA, "alice", ApprovalGranted, "alice"},
		{"member of an owner team pushed the head", headSHA, "carol", ApprovalGranted, "carol"},
		{"non-owner pushed the head", headSHA, "bob", ApprovalMissing, ""},
		{"owner of the head settings only", headSHA, "mallory", ApprovalMissing, ""},
		{"owner pushed an older head", oldSHA, "alice", ApprovalMissing, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = []string{policy.PolicyPath}
			pushed := pullTarget()
			pushed.HeadSHA = tt.recordedSHA
			if err := New(f.repos, f.pulls, f.store).RecordPusher(context.Background(), pushed, tt.pusher); err != nil {
				t.Fatalf("RecordPusher: %v", err)
			}

			got := f.mustResolve(t, pullTarget())
			wantApproval(t, got, tt.wantApproval, tt.wantBy)
		})
	}
}

func TestPusherGrantsNothingForAFork(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	if err := New(f.repos, f.pulls, f.store).RecordPusher(context.Background(), pullTarget(), "alice"); err != nil {
		t.Fatalf("RecordPusher: %v", err)
	}
	fork := pullTarget()
	fork.Fork = true

	got := f.mustResolve(t, fork)
	wantApproval(t, got, ApprovalMissing, "")
}

func TestEmptyPusherRecordsNothing(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	if err := New(f.repos, f.pulls, f.store).RecordPusher(context.Background(), pullTarget(), ""); err != nil {
		t.Fatalf("RecordPusher: %v", err)
	}

	got := f.mustResolve(t, pullTarget())
	wantApproval(t, got, ApprovalMissing, "")
}

func TestPusherReadFailureFailsClosed(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	f.store = &stubStore{get: func(context.Context) ([]byte, error) { return nil, errBoom }}

	_, err := f.resolve(pullTarget())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Resolve error = %v, want the store error: a failed pusher read must not decide the approval", err)
	}
}
