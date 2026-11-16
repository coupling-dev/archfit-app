package gate

import (
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestSetupSummaryReusesProtectedInputsButOwnerChecksStayFresh(t *testing.T) {
	for _, kind := range []string{OwnerFirst, OwnerWorkflow} {
		t.Run(kind, func(t *testing.T) {
			pull := 0
			if kind == OwnerWorkflow {
				pull = 7
			}
			w := ownerViewWorld(t, kind, pull)
			action := OwnerAction{Kind: kind, Pull: pull}
			want := ApprovalNeeded
			if pull > 0 {
				if _, err := w.act(oliviaAdmin, action); err != nil {
					t.Fatal(err)
				}
				want = ApprovalPending
			}
			ctx, snapshot := WithHomeSnapshot(t.Context(), w.shop)
			if _, err := ReadHomeSnapshot(ctx, w.gh, w.shop); err != nil {
				t.Fatal(err)
			}
			if policy := snapshot.Policy(ctx, w.gh); policy.ReadErr != nil || policy.DecodeErr != nil {
				t.Fatalf("policy: %+v", policy)
			}
			if _, err := snapshot.Workflow(ctx, w.gh); err != nil {
				t.Fatal(err)
			}
			w.gh.fail["BranchHead"] = api.ErrRateLimited
			if pull == 0 {
				w.gh.fail["File"] = api.ErrRateLimited
			}
			got := w.gate.SetupApproval(ctx, w.shop, action)
			if got.Status != want {
				t.Fatalf("summary = %+v; want %s", got, want)
			}
			if snapshot.CacheHits() < 2 {
				t.Fatalf("protected inputs not reused: %d hits", snapshot.CacheHits())
			}
			if _, err := w.gate.OwnerDigest(ctx, Target{Repo: w.shop}, action); !errors.Is(err, api.ErrRateLimited) {
				t.Fatalf("approval-authorizing input was cached: %v", err)
			}
			fresh := w.gate.SetupApproval(t.Context(), w.shop, action)
			if fresh.Status != ApprovalUnknown {
				t.Fatalf("snapshot escaped request: %+v", fresh)
			}
		})
	}
}

func TestSetupSummaryWithSnapshotStillChecksCurrentHeadAndDigests(t *testing.T) {
	for _, change := range []string{"head", "policy", "workflow", "unreadable", "approval record"} {
		t.Run(change, func(t *testing.T) {
			w := ownerViewWorld(t, OwnerWorkflow, 7)
			action := OwnerAction{Kind: OwnerWorkflow, Pull: 7}
			if _, err := w.act(oliviaAdmin, action); err != nil {
				t.Fatal(err)
			}
			ctx, snapshot := WithHomeSnapshot(t.Context(), w.shop)
			if _, err := ReadHomeSnapshot(ctx, w.gh, w.shop); err != nil {
				t.Fatal(err)
			}
			snapshot.Policy(ctx, w.gh)
			if _, err := snapshot.Workflow(ctx, w.gh); err != nil {
				t.Fatal(err)
			}
			if got := w.gate.SetupApproval(ctx, w.shop, action); got.Status != ApprovalPending {
				t.Fatalf("summary = %+v", got)
			}
			pull := w.gh.pulls[pullKey(w.shop.FullName, 7)]
			want := ApprovalStale
			switch change {
			case "head":
				pull.HeadSHA = "moved"
				w.gh.pulls[pullKey(w.shop.FullName, 7)] = pull
			case "policy":
				w.setFile(w.shop, pull.HeadSHA, approvals.PolicyPath, []byte(policy4+"# changed"))
			case "workflow":
				w.setFile(w.shop, pull.HeadSHA, approvals.WorkflowPath, []byte("changed"))
			case "unreadable":
				w.gh.fail["Pull"] = api.ErrRateLimited
				want = ApprovalUnknown
			case "approval record":
				if err := w.mem.Put(ctx, store.Tenant(w.shop.InstallationID), records.ProposalIndexKey(w.shop.FullName), []byte("invalid")); err != nil {
					t.Fatal(err)
				}
				want = ApprovalUnknown
			}
			if got := w.gate.SetupApproval(ctx, w.shop, action); got.Status != want {
				t.Fatalf("summary = %+v; want %s", got, want)
			}
		})
	}
}
