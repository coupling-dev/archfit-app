package inputs

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

var bareHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func digests(raws ...[]byte) []string {
	out := make([]string, len(raws))
	for i, raw := range raws {
		out[i] = policy.Digest(raw)
	}
	return out
}

func wantBaseDigests(t *testing.T, got Trusted) {
	t.Helper()
	wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy))
	wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline))
	wantStrings(t, "LabelsDigests", got.LabelsDigests, digests(baseLabels))
}

func wantApproval(t *testing.T, got Trusted, approval Approval, by string) {
	t.Helper()
	if got.Approval != approval || got.ApprovedBy != by {
		t.Errorf("Approval, ApprovedBy = %q, %q; want %q, %q", got.Approval, got.ApprovedBy, approval, by)
	}
}

func TestResolveCleanPullRequest(t *testing.T) {
	f := newFixture()
	got := f.mustResolve(t, pullTarget())

	if !got.SettingsFound {
		t.Error("SettingsFound = false")
	}
	if got.Settings.Gate != settings.GateRequired || got.Settings.Workflow != workflowPath {
		t.Errorf("Settings = %+v", got.Settings)
	}
	wantBaseDigests(t, got)
	if !bareHex.MatchString(got.PolicyDigests[0]) {
		t.Errorf("policy digest %q is not bare lowercase hex", got.PolicyDigests[0])
	}
	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, nil)
	wantApproval(t, got, ApprovalNotRequired, "")
	if f.pulls.reviewsCalls != 0 {
		t.Errorf("Reviews called %d times", f.pulls.reviewsCalls)
	}

	t.Run("baseline and labels absent on base", func(t *testing.T) {
		f := newFixture()
		delete(f.repos.files, fileCall{baseRef, policy.BaselinePath})
		delete(f.repos.files, fileCall{baseRef, policy.LabelsPath})
		delete(f.repos.files, fileCall{baseRef, policy.PolicyPath})
		got := f.mustResolve(t, pullTarget())
		if !got.SettingsFound {
			t.Error("SettingsFound = false")
		}
		if len(got.PolicyDigests)+len(got.BaselineDigests)+len(got.LabelsDigests) != 0 {
			t.Errorf("digests = %q %q %q, want none", got.PolicyDigests, got.BaselineDigests, got.LabelsDigests)
		}
	})
}

func TestResolveApprovedPolicyChange(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{"main.go", policy.PolicyPath}
	f.pulls.reviews = []api.Review{approved("alice", headSHA)}
	got := f.mustResolve(t, pullTarget())

	wantApproval(t, got, ApprovalGranted, "alice")
	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, []string{policy.PolicyPath})
	wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy, headPolicy))
	wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline))
	wantStrings(t, "LabelsDigests", got.LabelsDigests, digests(baseLabels))
	if want := time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC); !got.EarliestWaiverExpiry.Equal(want) {
		t.Errorf("EarliestWaiverExpiry = %v, want the approved head waiver %v", got.EarliestWaiverExpiry, want)
	}

	t.Run("labels only", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.LabelsPath}
		f.pulls.reviews = []api.Review{approved("alice", headSHA)}
		got := f.mustResolve(t, pullTarget())
		wantApproval(t, got, ApprovalGranted, "alice")
		wantStrings(t, "LabelsDigests", got.LabelsDigests, digests(baseLabels, headLabels))
		wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy))
		wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline))
		if !got.EarliestWaiverExpiry.IsZero() {
			t.Errorf("EarliestWaiverExpiry = %v, want zero: the head policy is not approved input", got.EarliestWaiverExpiry)
		}
	})

	t.Run("policy deleted at head appends nothing", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.PolicyPath}
		f.pulls.reviews = []api.Review{approved("alice", headSHA)}
		delete(f.repos.files, fileCall{headSHA, policy.PolicyPath})
		got := f.mustResolve(t, pullTarget())
		wantApproval(t, got, ApprovalGranted, "alice")
		wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy))
	})
}

func TestResolvePushTarget(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.PolicyPath}
	got := f.mustResolve(t, pushTarget())

	wantBaseDigests(t, got)
	wantStrings(t, "ProtectedPaths", got.ProtectedPaths, nil)
	wantApproval(t, got, ApprovalNotRequired, "")
	if f.pulls.changedCalls != 0 || f.pulls.reviewsCalls != 0 {
		t.Errorf("ChangedFiles calls = %d, Reviews calls = %d; want 0, 0", f.pulls.changedCalls, f.pulls.reviewsCalls)
	}
	for _, c := range f.repos.calls {
		if c.ref != baseRef {
			t.Errorf("File read at %q, want only %q", c.ref, baseRef)
		}
	}
}

func TestResolveBootstrap(t *testing.T) {
	tests := []struct {
		name     string
		settings []byte
	}{
		{"no settings file", nil},
		{"unparsable settings file", []byte("version: 1\nowners: [alice]\nsurprise: true\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			delete(f.repos.files, fileCall{baseRef, settings.Path})
			if tt.settings != nil {
				f.repos.files[fileCall{baseRef, settings.Path}] = tt.settings
			}
			f.pulls.changed = []string{policy.PolicyPath}
			ent := Entitlement{SuspendAfter: now.Add(-time.Hour), Reason: "Trial ended"}
			f.putEntitlement(t, installation, ent)

			got := f.mustResolve(t, pullTarget())
			want := Trusted{Approval: ApprovalNotRequired, Suspended: true, Entitlement: ent}
			if got.SettingsFound || got.Approval != want.Approval || got.Suspended != want.Suspended ||
				got.Entitlement != want.Entitlement || len(got.Settings.Owners) != 0 {
				t.Errorf("Trusted = %+v, want %+v", got, want)
			}
			if len(got.PolicyDigests)+len(got.BaselineDigests)+len(got.LabelsDigests)+len(got.ProtectedPaths) != 0 {
				t.Errorf("digests and paths must be empty: %+v", got)
			}
			if f.pulls.changedCalls != 0 {
				t.Errorf("ChangedFiles called %d times", f.pulls.changedCalls)
			}
		})
	}
}

func TestEntitlementRecordShape(t *testing.T) {
	record := `{"suspend_after":"2026-10-20T00:00:00Z","warn_from":"2026-10-13T00:00:00Z",` +
		`"notice":"Trial ends 2026-10-20","reason":"Trial ended. Subscribe to restore the gate."}`
	s := &stubStore{get: func(context.Context) ([]byte, error) { return []byte(record), nil }}
	f := newFixture()
	f.store = s

	got := f.mustResolve(t, pullTarget()).Entitlement
	want := Entitlement{
		SuspendAfter: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
		WarnFrom:     time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		Notice:       "Trial ends 2026-10-20",
		Reason:       "Trial ended. Subscribe to restore the gate.",
	}
	if !got.SuspendAfter.Equal(want.SuspendAfter) || !got.WarnFrom.Equal(want.WarnFrom) ||
		got.Notice != want.Notice || got.Reason != want.Reason {
		t.Errorf("Entitlement = %+v, want %+v", got, want)
	}
	if EntitlementKey != "entitlement" {
		t.Errorf("EntitlementKey = %q", EntitlementKey)
	}
	if len(s.keys) != 1 || s.keys[0] != "entitlement" || s.tenants[0] != store.Tenant(installation) {
		t.Errorf("Store reads = %v under %v, want one read of %q under tenant %d", s.keys, s.tenants, "entitlement", installation)
	}
}
