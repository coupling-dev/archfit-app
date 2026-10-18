package inputs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	baseRef      = "main"
	headSHA      = "sha-head-2"
	oldSHA       = "sha-head-1"
	workflowPath = ".github/workflows/archfit.yaml"
	teamOwner    = "acme/architects"
	installation = int64(101)
)

// Bytes are built inline: a formatter must never change what the digests are computed from.
var (
	baseSettings = []byte("version: 1\nowners:\n  - alice\n  - " + teamOwner + "\ngate: required\nworkflow: " + workflowPath + "\n")
	headSettings = []byte("version: 1\nowners:\n  - alice\n  - mallory\ngate: advisory\nworkflow: .github/workflows/other.yaml\n")
	basePolicy   = []byte("version: 1\nrules: []\n")
	headPolicy   = []byte("version: 1\nrules: []\nwaivers:\n  - rule: no-cycles\n    reason: migration\n    expires: 2026-10-05\n")
	baseBaseline = []byte(`{"findings":[]}`)
	headBaseline = []byte(`{"findings":["everything"]}`)
	baseLabels   = []byte("labels: {}\n")
	headLabels   = []byte("labels:\n  core: [a]\n")

	now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	errBoom     = errors.New("boom")
	errOversize = errors.New("github: file exceeds 1 MiB")
)

type fileCall struct{ ref, path string }

type fakeRepos struct {
	api.Repos // unused methods panic: inputs must never call them
	files     map[fileCall][]byte
	errs      map[fileCall]error
	calls     []fileCall
}

func (f *fakeRepos) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	c := fileCall{ref, path}
	f.calls = append(f.calls, c)
	if err := f.errs[c]; err != nil {
		return nil, "", err
	}
	raw, ok := f.files[c]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return raw, "blob", nil
}

func (f *fakeRepos) headReads() []string {
	var paths []string
	for _, c := range f.calls {
		if c.ref != baseRef {
			paths = append(paths, c.path)
		}
	}
	return paths
}

type fakePulls struct {
	api.Pulls // unused methods panic: inputs must never call them
	changed   []string
	truncated bool
	reviews   []api.Review
	teams     map[string][]string // "org/team-slug" -> member logins

	changedErr, reviewsErr, teamErr error

	changedCalls, reviewsCalls int
	teamCalls                  []string // "team:login"
}

func (f *fakePulls) ChangedFiles(context.Context, api.RepoRef, int) ([]string, bool, error) {
	f.changedCalls++
	return f.changed, f.truncated, f.changedErr
}

func (f *fakePulls) Reviews(context.Context, api.RepoRef, int) ([]api.Review, error) {
	f.reviewsCalls++
	return f.reviews, f.reviewsErr
}

func (f *fakePulls) TeamMember(_ context.Context, _ api.RepoRef, team, login string) (bool, error) {
	f.teamCalls = append(f.teamCalls, team+":"+login)
	if f.teamErr != nil {
		return false, f.teamErr
	}
	for _, m := range f.teams[team] {
		if strings.EqualFold(m, login) {
			return true, nil
		}
	}
	return false, nil
}

// stubStore answers Get only; any other Store method panics, which proves the paths that use it only read.
type stubStore struct {
	store.Store
	get     func(ctx context.Context) ([]byte, error)
	tenants []store.Tenant
	keys    []string
}

func (s *stubStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.tenants = append(s.tenants, t)
	s.keys = append(s.keys, key)
	return s.get(ctx)
}

type fixture struct {
	repos *fakeRepos
	pulls *fakePulls
	store store.Store
}

// newFixture has all four trusted files on base, every protected file edited at head, and no reviews.
func newFixture() *fixture {
	return &fixture{
		repos: &fakeRepos{
			files: map[fileCall][]byte{
				{baseRef, settings.Path}:       baseSettings,
				{baseRef, policy.PolicyPath}:   basePolicy,
				{baseRef, policy.BaselinePath}: baseBaseline,
				{baseRef, policy.LabelsPath}:   baseLabels,
				{headSHA, settings.Path}:       headSettings,
				{headSHA, policy.PolicyPath}:   headPolicy,
				{headSHA, policy.BaselinePath}: headBaseline,
				{headSHA, policy.LabelsPath}:   headLabels,
				{headSHA, "CODEOWNERS"}:        []byte("* @mallory\n"),
			},
			errs: map[fileCall]error{},
		},
		pulls: &fakePulls{
			changed: []string{"internal/x/x.go"},
			teams:   map[string][]string{teamOwner: {"carol"}},
		},
		store: store.NewMemory(),
	}
}

func (f *fixture) resolve(t Target) (Trusted, error) {
	return New(f.repos, f.pulls, f.store).Resolve(context.Background(), t, now)
}

func (f *fixture) mustResolve(tb testing.TB, t Target) Trusted {
	tb.Helper()
	got, err := f.resolve(t)
	if err != nil {
		tb.Fatalf("Resolve: %v", err)
	}
	return got
}

func (f *fixture) putEntitlement(tb testing.TB, installationID int64, e Entitlement) {
	tb.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		tb.Fatal(err)
	}
	if err := f.store.Put(context.Background(), store.Tenant(installationID), EntitlementKey, raw); err != nil {
		tb.Fatal(err)
	}
}

func pullTarget() Target {
	return Target{
		Repo: api.Repo{
			RepoRef:       api.RepoRef{InstallationID: installation, FullName: "acme/shop"},
			ID:            9001,
			Private:       true,
			DefaultBranch: baseRef,
		},
		PullRequest: 7,
		HeadSHA:     headSHA,
		BaseRef:     baseRef,
	}
}

func pushTarget() Target {
	t := pullTarget()
	t.PullRequest = 0
	return t
}

func approved(login, sha string) api.Review {
	return api.Review{Reviewer: login, State: "APPROVED", CommitSHA: sha}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	old := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func logLines(buf *bytes.Buffer, substr string) int {
	n := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

func wantStrings(tb testing.TB, name string, got, want []string) {
	tb.Helper()
	if !slices.Equal(got, want) {
		tb.Errorf("%s = %q, want %q", name, got, want)
	}
}
