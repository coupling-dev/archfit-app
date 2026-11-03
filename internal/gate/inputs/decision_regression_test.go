package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestNativeOnlyApprovalCanBeWithdrawn(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "alice", State: reviewApproved, CommitSHA: headSHA, SubmittedAt: now}}
	canWithdraw, err := f.resolver().CanWithdraw(t.Context(), target, "alice")
	if err != nil || !canWithdraw {
		t.Fatalf("CanWithdraw = %v, %v, want true", canWithdraw, err)
	}
	if err := f.resolver().Decide(t.Context(), target, "alice", f.shown(t, target), DecisionWithdraw, "Reconsidering my architecture approval.", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalMissing, "")
	if f.pulls.reviews[0].State != reviewApproved {
		t.Fatal("withdrawal changed GitHub's review")
	}
	canWithdraw, err = f.resolver().CanWithdraw(t.Context(), target, "alice")
	if err != nil || canWithdraw {
		t.Fatalf("CanWithdraw after withdrawal = %v, %v, want false", canWithdraw, err)
	}
	f.pulls.reviews = append(f.pulls.reviews, api.Review{ID: 78, Reviewer: "bob", State: reviewApproved, CommitSHA: headSHA, SubmittedAt: now})
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "bob")
}

func TestWithdrawnGrantsDoNotReturnAfterAnotherCandidate(t *testing.T) {
	for _, source := range []string{"native", "legacy"} {
		t.Run(source, func(t *testing.T) {
			f := newFixture()
			f.setBaseSettings([]string{"alice"})
			f.pulls.changed = []string{policy.BaselinePath}
			original := pullTarget()
			seen := f.shown(t, original)
			if source == "legacy" {
				raw, err := json.Marshal(approvalRecord{Login: "alice", HeadSHA: original.HeadSHA, Digests: seen, At: now})
				if err != nil {
					t.Fatal(err)
				}
				if err := f.store.Put(t.Context(), store.Tenant(installation), approvalKey(original), raw); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := f.approve(original, "alice", seen); err != nil {
					t.Fatal(err)
				}
				f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "alice", State: reviewApproved, CommitSHA: original.HeadSHA, SubmittedAt: now}}
			}
			if err := f.resolver().Decide(t.Context(), original, "alice", seen, DecisionWithdraw, "Withdraw the original grant.", nil, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			wantApproval(t, f.mustResolve(t, original), ApprovalMissing, "")
			moved := original
			moved.HeadSHA = "sha-another-candidate"
			for _, path := range []string{policy.PolicyPath, policy.BaselinePath, policy.LabelsPath, settings.Path} {
				f.repos.files[fileCall{moved.HeadSHA, path}] = f.repos.files[fileCall{original.HeadSHA, path}]
			}
			movedSeen := f.shown(t, moved)
			if err := f.resolver().Decide(t.Context(), moved, "alice", movedSeen, DecisionApprove, "", nil, now.Add(2*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := f.resolver().Decide(t.Context(), moved, "alice", movedSeen, DecisionWithdraw, "Withdraw the other candidate too.", nil, now.Add(3*time.Minute)); err != nil {
				t.Fatal(err)
			}
			wantApproval(t, f.mustResolve(t, original), ApprovalMissing, "")
			if err := f.resolver().Decide(t.Context(), original, "alice", seen, DecisionApprove, "", nil, now.Add(4*time.Minute)); err != nil {
				t.Fatal(err)
			}
			wantApproval(t, f.mustResolve(t, original), ApprovalGranted, "alice")
		})
	}
}

func TestObjectionIDsAreOnlyAcceptedForOverrides(t *testing.T) {
	for _, action := range []DecisionAction{DecisionApprove, DecisionRequestChanges, DecisionWithdraw} {
		t.Run(string(action), func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = []string{policy.BaselinePath}
			target := pullTarget()
			seen := f.shown(t, target)
			if err := f.approve(target, "alice", seen); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.Get(t.Context(), store.Tenant(installation), decisionKey(target))
			if err != nil {
				t.Fatal(err)
			}
			err = f.resolver().Decide(t.Context(), target, "alice", seen, action, "A reason.", []string{"app:unexpected"}, now.Add(time.Minute))
			if !errors.Is(err, ErrDecisionInvalid) {
				t.Fatalf("decision error = %v, want ErrDecisionInvalid", err)
			}
			after, err := f.store.Get(t.Context(), store.Tenant(installation), decisionKey(target))
			if err != nil || string(after) != string(before) {
				t.Fatalf("invalid decision changed stored state: %v", err)
			}
			wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
		})
	}
}

func TestOverrideStopsWhenItsOwnerWithdrawsOrLosesAuthority(t *testing.T) {
	for _, transition := range []string{"withdraw", "leave owner team"} {
		t.Run(transition, func(t *testing.T) {
			f := newFixture()
			f.setBaseSettings([]string{teamOwner, "bob", "carol"})
			f.pulls.teams[teamOwner] = []string{"alice"}
			f.pulls.changed = []string{policy.BaselinePath}
			target := pullTarget()
			seen := f.shown(t, target)
			if err := f.resolver().Decide(t.Context(), target, "bob", seen, DecisionRequestChanges, "Changes are required.", nil, now); err != nil {
				t.Fatal(err)
			}
			blocked := f.mustResolve(t, target)
			if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionOverride, "I accept the stated risk.", []string{blocked.Objections[0].ID}, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if transition == "withdraw" {
				if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionWithdraw, "I no longer accept that risk.", nil, now.Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
			} else {
				f.pulls.teams[teamOwner] = nil
			}
			f.pulls.reviews = []api.Review{{ID: 80, Reviewer: "carol", State: reviewApproved, CommitSHA: headSHA, SubmittedAt: now.Add(3 * time.Minute)}}
			got := f.mustResolve(t, target)
			wantApproval(t, got, ApprovalMissing, "")
			if len(got.Objections) != 1 || got.Objections[0].Overridden {
				t.Fatalf("objection retained an inactive override: %+v", got.Objections)
			}
		})
	}
}

func TestEditingWithdrawnApprovalTextDoesNotReauthorize(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.approve(target, "alice", seen); err != nil {
		t.Fatal(err)
	}
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "alice", State: reviewApproved, CommitSHA: headSHA, Body: "Reviewed.", SubmittedAt: now}}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "Reconsidering this permission.", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.pulls.reviews[0].Body = "Reviewed. Typo corrected."
	wantApproval(t, f.mustResolve(t, target), ApprovalMissing, "")
	f.pulls.reviews = append(f.pulls.reviews, api.Review{ID: 78, Reviewer: "alice", State: reviewApproved, CommitSHA: headSHA, SubmittedAt: now.Add(2 * time.Minute)})
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
}

func TestMixedSourceOverrideRoundTripsThroughTheLedger(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob", "carol"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "bob", State: reviewChangesRequested, CommitSHA: headSHA, Body: "Native objection", SubmittedAt: now}}
	if err := f.resolver().Decide(t.Context(), target, "carol", seen, DecisionRequestChanges, "App objection", nil, now); err != nil {
		t.Fatal(err)
	}
	shown := f.mustResolve(t, target)
	ids := []string{shown.Objections[0].ID, shown.Objections[1].ID}
	if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionOverride, "I reviewed both objections.", ids, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
}

func TestReplacementOverrideCoversAllDisplayedObjections(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob", "carol"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.resolver().Decide(t.Context(), target, "bob", seen, DecisionRequestChanges, "Bob objects.", nil, now); err != nil {
		t.Fatal(err)
	}
	first := f.mustResolve(t, target)
	if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionOverride, "I accept Bob's risk.", []string{first.Objections[0].ID}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := f.resolver().Decide(t.Context(), target, "carol", seen, DecisionRequestChanges, "Carol objects.", nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	shown := f.mustResolve(t, target)
	var ids []string
	for _, objection := range shown.Objections {
		ids = append(ids, objection.ID)
	}
	if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionOverride, "I reviewed Bob and Carol's objections.", ids, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := f.mustResolve(t, target)
	wantApproval(t, got, ApprovalGranted, "alice")
	for _, objection := range got.Objections {
		if !objection.Overridden {
			t.Fatalf("replacement revived an objection: %+v", got.Objections)
		}
	}
}

func TestAppDecisionSurvivesAnUnrelatedBaseMove(t *testing.T) {
	for _, action := range []DecisionAction{DecisionApprove, DecisionOverride} {
		t.Run(string(action), func(t *testing.T) {
			f := newFixture()
			f.setBaseSettings([]string{"alice", "bob"})
			f.pulls.changed = []string{policy.BaselinePath}
			target := pullTarget()
			seen := f.shown(t, target)
			var ids []string
			if action == DecisionOverride {
				if err := f.resolver().Decide(t.Context(), target, "bob", seen, DecisionRequestChanges, "Review this risk.", nil, now); err != nil {
					t.Fatal(err)
				}
				ids = []string{f.mustResolve(t, target).Objections[0].ID}
			}
			if err := f.resolver().Decide(t.Context(), target, "alice", seen, action, "I reviewed this candidate.", ids, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			target.BaseSHA = "unrelated-main-commit"
			for _, path := range []string{settings.Path, policy.PolicyPath, policy.BaselinePath, policy.LabelsPath} {
				f.repos.files[fileCall{target.BaseSHA, path}] = f.repos.files[fileCall{baseRef, path}]
			}
			wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
			canWithdraw, err := f.resolver().CanWithdraw(t.Context(), target, "alice")
			if err != nil || !canWithdraw {
				t.Fatalf("CanWithdraw after unrelated merge = %v, %v", canWithdraw, err)
			}
		})
	}
}

func TestRepeatedApprovalDoesNotExhaustTheDecisionHistory(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	for range decisionHistoryCap + 1 {
		if err := f.approve(target, "alice", seen); err != nil {
			t.Fatal(err)
		}
	}
	got := f.mustResolve(t, target)
	if len(got.DecisionHistory) != 1 {
		t.Fatalf("repeated approval created %d decisions", len(got.DecisionHistory))
	}
	if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionWithdraw, "Reconsider this grant.", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalMissing, "")
}

func TestOrdinaryReapprovalPreservesAnExistingExplicitOverride(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.resolver().Decide(t.Context(), target, "bob", seen, DecisionRequestChanges, "Review this.", nil, now); err != nil {
		t.Fatal(err)
	}
	shown := f.mustResolve(t, target)
	if err := f.resolver().Decide(t.Context(), target, "alice", seen, DecisionOverride, "Risk accepted.", []string{shown.Objections[0].ID}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := f.approve(target, "alice", seen); err != nil {
		t.Fatal(err)
	}
	got := f.mustResolve(t, target)
	wantApproval(t, got, ApprovalGranted, "alice")
	if len(got.DecisionHistory) != 2 {
		t.Fatal("ordinary reapproval replaced a valid explicit override")
	}
}
