package inputs

import (
	"reflect"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestHeadEditsCannotWeakenInputs(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{"main.go", workflowPath, settings.Path, policy.BaselinePath, policy.PolicyPath, policy.LabelsPath}
	got := f.mustResolve(t, pullTarget())

	wantBaseDigests(t, got)
	if !reflect.DeepEqual(got.Settings.Owners, []string{"alice", teamOwner}) {
		t.Errorf("Owners = %q, want the base owners", got.Settings.Owners)
	}
	if got.Settings.Gate != settings.GateRequired {
		t.Errorf("Gate = %q, want the base mode %q", got.Settings.Gate, settings.GateRequired)
	}
	if got.Settings.Workflow != workflowPath {
		t.Errorf("Workflow = %q, want the base path %q", got.Settings.Workflow, workflowPath)
	}
	wantApproval(t, got, ApprovalMissing, "")
	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, allProtected)
	if !got.EarliestWaiverExpiry.IsZero() {
		t.Errorf("EarliestWaiverExpiry = %v, want zero: head waivers are not trusted", got.EarliestWaiverExpiry)
	}
}

func TestOrdinaryReviewConfersNothing(t *testing.T) {
	f := newFixture() // the head settings and CODEOWNERS already name mallory
	f.pulls.changed = []string{settings.Path, "CODEOWNERS", policy.PolicyPath, policy.BaselinePath}
	f.pulls.reviews = []api.Review{approved("mallory", headSHA)}
	got := f.mustResolve(t, pullTarget())

	wantApproval(t, got, ApprovalMissing, "")
	wantBaseDigests(t, got)
	wantStrings(t, "head reads", f.repos.headReads(), nil)
}

func TestNewCommitInvalidatesApproval(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	f.pulls.reviews = []api.Review{approved("alice", oldSHA)}

	got := f.mustResolve(t, pullTarget())
	wantApproval(t, got, ApprovalMissing, "")
	wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy))

	f.pulls.reviews = append(f.pulls.reviews, approved("alice", headSHA))
	got = f.mustResolve(t, pullTarget())
	wantApproval(t, got, ApprovalGranted, "alice")
	wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy, headPolicy))
}

func TestOwnerApprovedBaselinePullRequest(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}

	got := f.mustResolve(t, pullTarget())
	wantApproval(t, got, ApprovalMissing, "")
	wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline))

	f.pulls.reviews = []api.Review{approved("alice", headSHA)}
	got = f.mustResolve(t, pullTarget())
	wantApproval(t, got, ApprovalGranted, "alice")
	wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline, headBaseline))
	wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy))
	wantStrings(t, "LabelsDigests", got.LabelsDigests, digests(baseLabels))
}

func TestForkNeverGetsHeadInputs(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	f.pulls.reviews = []api.Review{approved("alice", headSHA)}
	target := pullTarget()
	target.Fork = true
	got := f.mustResolve(t, target)

	wantApproval(t, got, ApprovalMissing, "")
	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, []string{policy.PolicyPath})
	wantBaseDigests(t, got)
	wantStrings(t, "head reads", f.repos.headReads(), nil)
	if f.pulls.reviewsCalls != 0 {
		t.Errorf("Reviews called %d times for a fork", f.pulls.reviewsCalls)
	}
}

func TestSuspensionFacts(t *testing.T) {
	ent := Entitlement{
		SuspendAfter: now.Add(-24 * time.Hour),
		WarnFrom:     now.Add(-8 * 24 * time.Hour),
		Notice:       "Trial ended 2026-09-20",
		Reason:       "Trial ended. Subscribe to restore the gate.",
	}
	f := newFixture()
	f.putEntitlement(t, installation, ent)

	private := f.mustResolve(t, pullTarget())
	if !private.Suspended || private.Entitlement != ent {
		t.Errorf("private: Suspended = %t, Entitlement = %+v; want true, %+v", private.Suspended, private.Entitlement, ent)
	}
	wantBaseDigests(t, private)

	public := pullTarget()
	public.Repo.Private = false
	if got := f.mustResolve(t, public); got.Suspended || got.Entitlement != ent {
		t.Errorf("public: Suspended = %t, Entitlement = %+v; want false, %+v", got.Suspended, got.Entitlement, ent)
	}

	restored := ent
	restored.SuspendAfter = now.Add(30 * 24 * time.Hour)
	f.putEntitlement(t, installation, restored)
	if got := f.mustResolve(t, pullTarget()); got.Suspended || got.Entitlement != restored {
		t.Errorf("restored: Suspended = %t, Entitlement = %+v; want false, %+v", got.Suspended, got.Entitlement, restored)
	}
}
