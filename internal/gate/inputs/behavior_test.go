package inputs

import (
	"reflect"
	"testing"
	"time"

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
	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, allProtected)
	if !got.EarliestWaiverExpiry.IsZero() {
		t.Errorf("EarliestWaiverExpiry = %v, want zero: head waivers are not trusted", got.EarliestWaiverExpiry)
	}
}

func TestHeadOwnersConferNothing(t *testing.T) {
	f := newFixture() // the head settings and CODEOWNERS already name mallory
	f.pulls.changed = []string{settings.Path, "CODEOWNERS", policy.PolicyPath, policy.BaselinePath}
	got := f.mustResolve(t, pullTarget())

	wantBaseDigests(t, got)
	wantStrings(t, "head reads", f.repos.headReads(), nil)
}

func TestForkNeverGetsHeadInputs(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	target := pullTarget()
	target.Fork = true
	got := f.mustResolve(t, target)

	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, []string{policy.PolicyPath})
	wantBaseDigests(t, got)
	wantStrings(t, "head reads", f.repos.headReads(), nil)
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
