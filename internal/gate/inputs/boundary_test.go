package inputs

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

var allProtected = []string{policy.PolicyPath, policy.BaselinePath, policy.LabelsPath, settings.Path, workflowPath}

func TestHeadTreeIsNeverRead(t *testing.T) {
	unapproved := []struct {
		name      string
		fork      bool
		truncated bool
		reviews   []api.Review
	}{
		{name: "no reviews"},
		{name: "non-owner approval", reviews: []api.Review{approved("mallory", headSHA)}},
		{name: "owner approval on an older SHA", reviews: []api.Review{approved("alice", oldSHA)}},
		{name: "owner approval dismissed", reviews: []api.Review{{Reviewer: "alice", State: "DISMISSED", CommitSHA: headSHA}}},
		{name: "truncated file list", truncated: true},
		{name: "fork with owner approval", fork: true, reviews: []api.Review{approved("alice", headSHA)}},
	}
	for _, tt := range unapproved {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = allProtected
			f.pulls.truncated = tt.truncated
			f.pulls.reviews = tt.reviews
			target := pullTarget()
			target.Fork = tt.fork

			got := f.mustResolve(t, target)
			wantApproval(t, got, ApprovalMissing, "")
			wantStrings(t, "head reads", f.repos.headReads(), nil)
		})
	}

	approvedCases := []struct {
		name    string
		changed []string
		want    []string
	}{
		{"policy, settings and workflow", []string{policy.PolicyPath, settings.Path, workflowPath, "CODEOWNERS"}, []string{policy.PolicyPath}},
		{"baseline and labels", []string{policy.LabelsPath, policy.BaselinePath}, []string{policy.BaselinePath, policy.LabelsPath}},
		{"settings only", []string{settings.Path}, nil},
	}
	for _, tt := range approvedCases {
		t.Run("approved: "+tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = tt.changed
			f.pulls.reviews = []api.Review{approved("alice", headSHA)}
			got := f.mustResolve(t, pullTarget())
			wantApproval(t, got, ApprovalGranted, "alice")
			wantStrings(t, "head reads", f.repos.headReads(), tt.want)
		})
	}
}

func TestStoreFailureIsNotSuspension(t *testing.T) {
	const marker = "PAYLOAD-MARKER"
	tests := []struct {
		name    string
		get     func(ctx context.Context) ([]byte, error)
		outcome string
	}{
		{"not found", func(context.Context) ([]byte, error) { return nil, store.ErrNotFound }, "missing"},
		{"generic error", func(context.Context) ([]byte, error) { return nil, errors.New(marker) }, "unreadable"},
		{"blocks past the timeout", func(ctx context.Context) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, "unreadable"},
		{"invalid JSON", func(context.Context) ([]byte, error) { return []byte(`{"notice":"` + marker), nil }, "unreadable"},
		{"half-valid JSON keeps nothing", func(context.Context) ([]byte, error) {
			return []byte(`{"suspend_after":"2020-01-01T00:00:00Z","reason":"` + marker + `","notice":5}`), nil
		}, "unreadable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := entitlementTimeout
			entitlementTimeout = 10 * time.Millisecond
			t.Cleanup(func() { entitlementTimeout = old })
			logs := captureLogs(t)
			f := newFixture()
			f.store = &stubStore{get: tt.get}

			got := f.mustResolve(t, pullTarget())
			if got.Suspended || got.Entitlement != (Entitlement{}) {
				t.Errorf("Suspended = %t, Entitlement = %+v; want false and zero", got.Suspended, got.Entitlement)
			}
			wantBaseDigests(t, got)
			if n := logLines(logs, "entitlement"); n != 1 {
				t.Errorf("entitlement log lines = %d, want 1:\n%s", n, logs)
			}
			if !strings.Contains(logs.String(), tt.outcome) || !strings.Contains(logs.String(), fmt.Sprint(installation)) {
				t.Errorf("log lacks outcome %q or the installation ID:\n%s", tt.outcome, logs)
			}
			if strings.Contains(logs.String(), marker) {
				t.Errorf("log carries the payload:\n%s", logs)
			}
		})
	}

	t.Run("found", func(t *testing.T) {
		logs := captureLogs(t)
		f := newFixture()
		f.putEntitlement(t, installation, Entitlement{Notice: "Trial ends 2026-10-20"})
		f.mustResolve(t, pullTarget())
		if n := logLines(logs, "found"); n != 1 {
			t.Errorf("found log lines = %d, want 1:\n%s", n, logs)
		}
	})
}

func TestGitHubErrorsFailClosed(t *testing.T) {
	failures := []struct {
		name string
		set  func(f *fixture, err error)
	}{
		{"File settings", func(f *fixture, err error) { f.repos.errs[fileCall{baseRef, settings.Path}] = err }},
		{"File policy", func(f *fixture, err error) { f.repos.errs[fileCall{baseRef, policy.PolicyPath}] = err }},
		{"File baseline", func(f *fixture, err error) { f.repos.errs[fileCall{baseRef, policy.BaselinePath}] = err }},
		{"File labels", func(f *fixture, err error) { f.repos.errs[fileCall{baseRef, policy.LabelsPath}] = err }},
		{"File head policy", func(f *fixture, err error) { f.repos.errs[fileCall{headSHA, policy.PolicyPath}] = err }},
		{"File head baseline", func(f *fixture, err error) { f.repos.errs[fileCall{headSHA, policy.BaselinePath}] = err }},
		{"ChangedFiles", func(f *fixture, err error) { f.pulls.changedErr = err }},
		{"Reviews", func(f *fixture, err error) { f.pulls.reviewsErr = err }},
		{"TeamMember", func(f *fixture, err error) { f.pulls.teamErr = err }},
	}
	errs := []error{errBoom, fmt.Errorf("after backoff: %w", api.ErrRateLimited), errOversize}
	for _, fail := range failures {
		for _, want := range errs {
			t.Run(fail.name+"/"+want.Error(), func(t *testing.T) {
				f := newFixture()
				f.pulls.changed = allProtected
				f.pulls.reviews = []api.Review{approved("carol", headSHA)} // an owner only through the team
				f.putEntitlement(t, installation, Entitlement{Notice: "Trial ends 2026-10-20"})
				fail.set(f, want)

				got, err := f.resolve(pullTarget())
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want errors.Is %v", err, want)
				}
				if !reflect.DeepEqual(got, Trusted{}) {
					t.Errorf("Trusted = %+v, want zero", got)
				}
			})
		}
	}

	t.Run("without the failure the scenario is granted", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = allProtected
		f.pulls.reviews = []api.Review{approved("carol", headSHA)}
		wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalGranted, "carol")
	})
}

type recordingStore struct {
	store.Store
	tenants []store.Tenant
}

func (s *recordingStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.tenants = append(s.tenants, t)
	return s.Store.Get(ctx, t, key)
}

func TestTenantIsolation(t *testing.T) {
	const installationB = int64(202)
	f := newFixture()
	f.putEntitlement(t, installation, Entitlement{SuspendAfter: now.Add(-time.Hour), Reason: "Trial ended"})
	rec := &recordingStore{Store: f.store}
	f.store = rec

	target := pullTarget()
	target.Repo.InstallationID = installationB
	got := f.mustResolve(t, target)

	if got.Suspended || got.Entitlement != (Entitlement{}) {
		t.Errorf("installation B sees %+v, suspended = %t", got.Entitlement, got.Suspended)
	}
	if !reflect.DeepEqual(rec.tenants, []store.Tenant{store.Tenant(installationB)}) {
		t.Errorf("Store read under tenants %v, want only %d", rec.tenants, installationB)
	}
}

func TestLogsCarryOnlyIdentifiers(t *testing.T) {
	markers := []string{"marker-owner", "marker-reviewer", "MARKER-REASON", "MARKER-POLICY", "MARKER-SETTINGS", "MARKER-ENTITLEMENT"}
	markedSettings := []byte("version: 1\nowners:\n  - marker-owner\n  - " + teamOwner + "\nworkflow: " + workflowPath + "\n")
	markedPolicy := func(expires string) []byte {
		return []byte("# MARKER-POLICY\nwaivers:\n  - rule: no-cycles\n    reason: MARKER-REASON\n    expires: " + expires + "\n")
	}
	scenarios := []struct {
		name  string
		setup func(f *fixture)
	}{
		{"approved policy change", func(f *fixture) {
			f.repos.files[fileCall{headSHA, policy.PolicyPath}] = markedPolicy("2026-10-01")
			f.pulls.reviews = []api.Review{approved("marker-reviewer", headSHA), approved("marker-owner", headSHA)}
		}},
		{"unapproved policy change", func(f *fixture) {
			f.pulls.reviews = []api.Review{approved("marker-reviewer", headSHA)}
		}},
		{"malformed waivers", func(f *fixture) {
			f.repos.files[fileCall{baseRef, policy.PolicyPath}] = markedPolicy("MARKER-REASON")
		}},
		{"unparsable settings", func(f *fixture) {
			f.repos.files[fileCall{baseRef, settings.Path}] = []byte("MARKER-SETTINGS: true\n")
		}},
		{"unreadable entitlement", func(f *fixture) {
			f.store = &stubStore{get: func(context.Context) ([]byte, error) { return []byte("MARKER-ENTITLEMENT"), nil }}
		}},
		{"github failure", func(f *fixture) { f.pulls.reviewsErr = errBoom }},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			logs := captureLogs(t)
			f := newFixture()
			f.repos.files[fileCall{baseRef, settings.Path}] = markedSettings
			f.repos.files[fileCall{baseRef, policy.PolicyPath}] = markedPolicy("2026-10-01")
			f.pulls.changed = []string{policy.PolicyPath}
			f.putEntitlement(t, installation, Entitlement{Notice: "MARKER-ENTITLEMENT", Reason: "MARKER-ENTITLEMENT"})
			sc.setup(f)

			_, _ = f.resolve(pullTarget())
			if logs.Len() == 0 {
				t.Fatal("no log line written: the test proves nothing")
			}
			for _, m := range markers {
				if strings.Contains(logs.String(), m) {
					t.Errorf("log carries %q:\n%s", m, logs)
				}
			}
		})
	}
}

// The constraint forbids control, app, LLM and UI packages; the allowed set is tighter and easier to check.
func TestImportsStayInsideAllowedSet(t *testing.T) {
	const module = "github.com/alexei-led/archfit-app/"
	allowed := map[string]bool{
		module + "internal/github/api":    true,
		module + "internal/store":         true,
		module + "internal/wire/policy":   true,
		module + "internal/wire/settings": true,
	}
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, imp := range strings.Fields(string(out)) {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("forbidden import %q", imp)
		}
	}
}
