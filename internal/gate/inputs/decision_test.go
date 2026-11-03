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

func TestRequestChangesAndExplicitOverride(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	f.setBaseSettings([]string{"alice", "bob"})
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.approve(target, "alice", seen); err != nil {
		t.Fatal(err)
	}
	if err := f.resolver().Decide(context.Background(), target, "bob", seen, DecisionRequestChanges, "Please explain the compatibility break.", nil, now.Add(time.Minute)); err != nil {
		t.Fatalf("RequestChanges: %v", err)
	}
	blocked := f.mustResolve(t, target)
	wantApproval(t, blocked, ApprovalMissing, "")
	if len(blocked.Objections) != 1 || blocked.Objections[0].Login != "bob" || blocked.Objections[0].Source != "app" ||
		blocked.Objections[0].Reason != "Please explain the compatibility break." {
		t.Fatalf("Objections = %+v, want Bob's recorded objection", blocked.Objections)
	}
	if err := f.approve(target, "alice", seen); !errors.Is(err, ErrActiveObjections) {
		t.Fatalf("ordinary approval with another owner's objection = %v, want ErrActiveObjections", err)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "I accept the compatibility risk.", []string{"app:999"}, now.Add(2*time.Minute)); !errors.Is(err, ErrOwnerDecisionStale) {
		t.Fatalf("override with stale ID = %v, want ErrOwnerDecisionStale", err)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "I accept the compatibility risk.", []string{blocked.Objections[0].ID}, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("explicit override: %v", err)
	}
	approved := f.mustResolve(t, target)
	wantApproval(t, approved, ApprovalGranted, "alice")
	if len(approved.Objections) != 1 || !approved.Objections[0].Overridden || len(approved.DecisionHistory) != 3 ||
		approved.DecisionHistory[1].Action != DecisionRequestChanges || approved.DecisionHistory[2].Action != DecisionOverride ||
		approved.DecisionHistory[2].Overrode[0] != blocked.Objections[0].ID || len(approved.DecisionHistory[2].OverrodeObjections) != 1 {
		t.Fatalf("decision history/state = %+v / %+v", approved.DecisionHistory, approved.Objections)
	}
}

func TestReasonAndWithdrawalAreBoundedOwnerDecisions(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	f.setBaseSettings([]string{"alice", "bob"})
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.resolver().Decide(context.Background(), target, "bob", seen, DecisionRequestChanges, "  \n", nil, now); !errors.Is(err, ErrDecisionReason) {
		t.Fatalf("empty request reason = %v, want ErrDecisionReason", err)
	}
	if err := f.approve(target, "alice", seen); err != nil {
		t.Fatal(err)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "", nil, now.Add(time.Minute)); !errors.Is(err, ErrDecisionReason) {
		t.Fatalf("empty withdrawal reason = %v, want ErrDecisionReason", err)
	}
	if err := f.approve(target, "bob", seen); err != nil {
		t.Fatal(err)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "Reconsidering this approval.", nil, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("withdraw own approval: %v", err)
	}
	got := f.mustResolve(t, target)
	wantApproval(t, got, ApprovalGranted, "bob")
	if len(got.DecisionHistory) != 3 || got.DecisionHistory[2].Action != DecisionWithdraw || got.DecisionHistory[2].Reason != "Reconsidering this approval." {
		t.Fatalf("history = %+v, want the grant and withdrawal reasons retained", got.DecisionHistory)
	}
	if hasAlice, err := f.resolver().CanWithdraw(context.Background(), target, "alice"); err != nil || hasAlice {
		t.Errorf("CanWithdraw(alice) = %v, %v, want false", hasAlice, err)
	}
}

func TestObjectionsSurvivePushAndOverrideIsCandidateBound(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	old := pullTarget()
	seen := f.shown(t, old)
	if err := f.resolver().Decide(context.Background(), old, "bob", seen, DecisionRequestChanges, "Re-review after the push.", nil, now); err != nil {
		t.Fatal(err)
	}
	moved := old
	moved.HeadSHA = "sha-head-3"
	f.repos.files[fileCall{moved.HeadSHA, policy.BaselinePath}] = []byte(`{"findings":["new"]}`)
	f.repos.files[fileCall{moved.HeadSHA, policy.PolicyPath}] = headPolicy
	f.repos.files[fileCall{moved.HeadSHA, policy.LabelsPath}] = headLabels
	f.repos.files[fileCall{moved.HeadSHA, settings.Path}] = headSettings
	newCandidate := f.shown(t, moved)
	stale := f.mustResolve(t, moved)
	if stale.Approval != ApprovalMissing || len(stale.Objections) != 1 || stale.Objections[0].HeadSHA != old.HeadSHA {
		t.Fatalf("new head state = %+v, want old objection and no reused grant", stale)
	}
	if err := f.approve(moved, "alice", newCandidate); !errors.Is(err, ErrActiveObjections) {
		t.Fatalf("ordinary approval after push = %v, want blocked", err)
	}
	if err := f.resolver().Decide(context.Background(), moved, "alice", newCandidate, DecisionOverride, "Reviewed the pushed candidate.", []string{stale.Objections[0].ID}, now.Add(time.Minute)); err != nil {
		t.Fatalf("override against new candidate: %v", err)
	}
	wantApproval(t, f.mustResolve(t, moved), ApprovalGranted, "alice")
	// The old head does not inherit the new-head override or grant.
	wantApproval(t, f.mustResolve(t, old), ApprovalMissing, "")
}

func TestNewObjectionInvalidatesDisplayedOverrideSet(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob", "carol"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	for i, login := range []string{"bob", "carol"} {
		if err := f.resolver().Decide(context.Background(), target, login, seen, DecisionRequestChanges, login+" objection", nil, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// Capture the earlier set before the second objection arrives.
			f.pulls.changed = []string{policy.BaselinePath}
		}
	}
	current := f.mustResolve(t, target)
	if len(current.Objections) != 2 {
		t.Fatalf("objections = %+v, want both owners", current.Objections)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "Reviewed.", []string{current.Objections[0].ID}, now.Add(3*time.Minute)); !errors.Is(err, ErrOwnerDecisionStale) {
		t.Fatalf("partial override = %v, want stale objection set", err)
	}
}

func TestWithdrawalSuppressesPriorNativeApprovalWithoutChangingGitHubReview(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.approve(target, "alice", seen); err != nil {
		t.Fatal(err)
	}
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "alice", State: "APPROVED", CommitSHA: headSHA, SubmittedAt: now}}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "Reconsidering my approval.", nil, now.Add(time.Minute)); err != nil {
		t.Fatalf("withdraw App grant: %v", err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalMissing, "")
	if len(f.pulls.reviews) != 1 || f.pulls.reviews[0].State != "APPROVED" {
		t.Fatalf("withdrawal mutated native GitHub review: %+v", f.pulls.reviews)
	}

	f.pulls.reviews = append(f.pulls.reviews, api.Review{ID: 78, Reviewer: "bob", State: "APPROVED", CommitSHA: headSHA, SubmittedAt: now})
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "bob")

	f.pulls.reviews = []api.Review{
		{ID: 77, Reviewer: "alice", State: "APPROVED", CommitSHA: headSHA, SubmittedAt: now},
		{ID: 79, Reviewer: "alice", State: "APPROVED", CommitSHA: headSHA, SubmittedAt: now.Add(2 * time.Minute)},
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
}

func TestEditingOverriddenNativeReviewCreatesNewObjectionVersion(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "bob", State: "CHANGES_REQUESTED", CommitSHA: headSHA, Body: "First objection", SubmittedAt: now}}
	before := f.mustResolve(t, target)
	if len(before.Objections) != 1 {
		t.Fatalf("initial objections = %+v", before.Objections)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "I reviewed the first objection.", []string{before.Objections[0].ID}, now.Add(time.Minute)); err != nil {
		t.Fatalf("override first review version: %v", err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")

	f.pulls.reviews[0].Body = "Updated objection with new information"
	updated := f.mustResolve(t, target)
	wantApproval(t, updated, ApprovalMissing, "")
	if len(updated.Objections) != 1 || updated.Objections[0].Overridden || updated.Objections[0].ID == before.Objections[0].ID {
		t.Fatalf("edited native review retained stale override: before=%+v after=%+v", before.Objections, updated.Objections)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "I reviewed the update.", []string{updated.Objections[0].ID}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("override updated review version: %v", err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
}

func TestNativeReviewObjectionAndDismissalAreIndependentOfAppGrant(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.approve(target, "alice", seen); err != nil {
		t.Fatal(err)
	}
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "bob", State: "CHANGES_REQUESTED", CommitSHA: headSHA, Body: "", SubmittedAt: now}}
	blocked := f.mustResolve(t, target)
	wantApproval(t, blocked, ApprovalMissing, "")
	if len(blocked.Objections) != 1 || blocked.Objections[0].ID != nativeReviewID(f.pulls.reviews[0]) || blocked.Objections[0].Reason != "" {
		t.Fatalf("native objection = %+v, want the imported empty reason and exact review version", blocked.Objections)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "I reviewed the objection.", []string{blocked.Objections[0].ID}, now.Add(time.Minute)); err != nil {
		t.Fatalf("explicit App override of native objection: %v", err)
	}
	archfitOverridden := f.mustResolve(t, target)
	wantApproval(t, archfitOverridden, ApprovalGranted, "alice")
	if len(archfitOverridden.Objections) != 1 || !archfitOverridden.Objections[0].Overridden || f.pulls.reviews[0].State != "CHANGES_REQUESTED" {
		t.Fatalf("native review/Archfit override state = review:%+v objections:%+v", f.pulls.reviews, archfitOverridden.Objections)
	}
	// An Archfit override authorizes Archfit only. The native request-changes review remains on GitHub until a separate action.
	if f.pulls.reviews[0].State != "CHANGES_REQUESTED" {
		t.Fatal("App override changed the native GitHub review")
	}
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "bob", State: "DISMISSED", CommitSHA: headSHA, Body: "", SubmittedAt: now}}
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
}

func TestOverrideIsBoundToNativeReviewVersions(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob", "carol"})
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	f.pulls.reviews = []api.Review{{ID: 77, Reviewer: "bob", State: "CHANGES_REQUESTED", CommitSHA: headSHA, Body: "First objection", SubmittedAt: now}}
	shown := f.mustResolve(t, target)
	f.pulls.reviews = append(f.pulls.reviews, api.Review{ID: 78, Reviewer: "carol", State: "CHANGES_REQUESTED", CommitSHA: headSHA, Body: "New objection", SubmittedAt: now.Add(time.Minute)})
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionOverride, "Reviewing both.", []string{shown.Objections[0].ID}, now.Add(2*time.Minute)); !errors.Is(err, ErrOwnerDecisionStale) {
		t.Fatalf("override with a stale native review set = %v, want ErrOwnerDecisionStale", err)
	}
	if record, err := f.store.Get(context.Background(), store.Tenant(installation), decisionKey(target)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale override record = %s, %v; no action should be stored", record, err)
	}
}

func TestOverrideDoesNotCarryToANewProtectedSettingsSnapshot(t *testing.T) {
	f := newFixture()
	f.setBaseSettings([]string{"alice", "bob"})
	f.pulls.changed = []string{policy.BaselinePath}
	old := pullTarget()
	seen := f.shown(t, old)
	if err := f.resolver().Decide(context.Background(), old, "bob", seen, DecisionRequestChanges, "Review this base version.", nil, now); err != nil {
		t.Fatal(err)
	}
	before := f.mustResolve(t, old)
	if err := f.resolver().Decide(context.Background(), old, "alice", seen, DecisionOverride, "Reviewed the old settings.", []string{before.Objections[0].ID}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	moved := old
	moved.BaseSHA = "new-protected-settings-commit"
	changedSettings, err := settings.Parse(f.repos.files[fileCall{baseRef, settings.Path}])
	if err != nil {
		t.Fatal(err)
	}
	changedSettings.DataMode = settings.DataManaged
	f.repos.files[fileCall{moved.BaseSHA, settings.Path}] = settings.Render(changedSettings)
	for _, path := range []string{policy.PolicyPath, policy.BaselinePath, policy.LabelsPath} {
		f.repos.files[fileCall{moved.BaseSHA, path}] = f.repos.files[fileCall{baseRef, path}]
	}
	stale := f.mustResolve(t, moved)
	if len(stale.Objections) != 1 || stale.Objections[0].Overridden {
		t.Fatalf("new protected snapshot reused the old override: %+v", stale.Objections)
	}
	if err := f.approve(moved, "alice", f.shown(t, moved)); !errors.Is(err, ErrActiveObjections) {
		t.Fatalf("ordinary approval after protected snapshot moved = %v, want blocked", err)
	}
}

func TestLegacyApprovalIsPreservedAndExplicitlyWithdrawn(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	legacy, err := json.Marshal(approvalRecord{Login: "alice", HeadSHA: headSHA, Digests: seen, At: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Put(context.Background(), store.Tenant(installation), approvalKey(target), legacy); err != nil {
		t.Fatal(err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalGranted, "alice")
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "Re-reviewing the decision.", nil, now.Add(time.Minute)); err != nil {
		t.Fatalf("withdraw legacy approval: %v", err)
	}
	wantApproval(t, f.mustResolve(t, target), ApprovalMissing, "")
}

func TestUnknownDecisionRecordFailsClosed(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	if err := f.store.Put(context.Background(), store.Tenant(installation), decisionKey(pullTarget()), []byte(`{"version":99,"next_id":1,"history":[]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.resolve(pullTarget()); !errors.Is(err, ErrDecisionInvalid) {
		t.Fatalf("Resolve error = %v, want ErrDecisionInvalid", err)
	}
}
