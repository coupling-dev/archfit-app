package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
	"github.com/coupling-dev/archfit-app/internal/store"
)

type fakeSetupGitHub struct {
	calls, comments              int
	runCalls                     int
	body                         string
	suite, marker                bool
	status                       int
	suiteErr, requestErr, runErr error
}

func (f *fakeSetupGitHub) ActionsSuite(context.Context, api.Repo, string) (int64, bool, error) {
	f.calls++
	return 1, f.suite, f.suiteErr
}
func (f *fakeSetupGitHub) RerequestSuite(context.Context, api.Repo, int64) (int, error) {
	f.calls++
	return f.status, f.requestErr
}
func (f *fakeSetupGitHub) RunURL(context.Context, api.Repo, int64) (string, error) {
	f.calls++
	f.runCalls++
	return "https://github.com/o/r/actions/runs/1/job/2", f.runErr
}
func (f *fakeSetupGitHub) HasMarkerComment(context.Context, api.Repo, int) (bool, error) {
	f.calls++
	return f.marker, nil
}
func (f *fakeSetupGitHub) Comment(_ context.Context, _ api.Repo, _ int, body string) error {
	f.body = body
	f.calls++
	f.comments++
	return nil
}

func TestSetupRecheck(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 42}
	for _, tt := range []struct {
		name          string
		suite, marker bool
		status        int
		lease         time.Time
		want          string
		comments      int
	}{
		{name: "accepted", suite: true, status: 201, want: "rerequested"},
		{name: "refused", suite: true, status: 403, want: "commented", comments: 1},
		{name: "marker", suite: true, status: 403, marker: true, want: "commented"},
		{name: "no suite", want: "commented", comments: 1},
		{name: "busy", lease: now.Add(time.Minute), want: "busy"},
		{name: "expired", suite: true, status: 201, lease: now.Add(-time.Second), want: "rerequested"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := store.NewMemory()
			gh := &fakeSetupGitHub{suite: tt.suite, marker: tt.marker, status: tt.status}
			s := setupRecheck{store: st, gh: gh, now: func() time.Time { return now }}
			if !tt.lease.IsZero() {
				raw, _ := json.Marshal(setupLease{Token: "old", Expires: tt.lease})
				if err := st.Put(ctx, 1, "setup-recheck/42/7", raw); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Run(ctx, repo, 7, "head")
			if (err != nil && tt.want != "busy") || (err == nil && tt.want == "busy") || got != tt.want {
				t.Fatalf("Run = %q, %v; want %q", got, err, tt.want)
			}
			if !tt.suite && gh.runCalls != 0 {
				t.Fatal("no suite must not request a run URL")
			}
			if gh.comments != tt.comments {
				t.Fatalf("comments = %d, want %d", gh.comments, tt.comments)
			}
			if got == "busy" {
				if gh.calls != 0 {
					t.Fatal("busy made GitHub calls")
				}
				return
			}
			before := gh.calls
			got, err = s.Run(ctx, repo, 7, "head")
			if err != nil || got != "done" || gh.calls != before {
				t.Fatalf("repeat = %q, %v; calls %d -> %d", got, err, before, gh.calls)
			}
		})
	}
}

type fakeSetupPulls map[string][]api.Pull

func (f fakeSetupPulls) SetupPulls(_ context.Context, r api.RepoRef) ([]api.Pull, error) {
	return f[r.FullName], nil
}

func TestSetupRecheckAddedRepositories(t *testing.T) {
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 42}
	other := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/other"}, ID: 43}
	pull := api.Pull{Number: 7, HeadSHA: "head", HeadRef: "archfit/setup", HeadRepoID: 42}
	fork := pull
	fork.HeadRepoID = 99
	for _, tt := range []struct {
		name  string
		added []string
		pulls fakeSetupPulls
		want  int
	}{
		{"created", []string{"o/r"}, fakeSetupPulls{"o/r": {pull}}, 1},
		{"added two", []string{"o/r", "o/other"}, fakeSetupPulls{"o/r": {pull}}, 1},
		{"fork", []string{"o/r"}, fakeSetupPulls{"o/r": {fork}}, 0},
		{"none", []string{"o/r"}, fakeSetupPulls{}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gh := &fakeSetupGitHub{suite: true, status: 201}
			st := store.NewMemory()
			c := Control{setup: &setupRecheck{store: st, gh: gh, now: time.Now}, setupPulls: tt.pulls}
			if err := c.recheckAdded(context.Background(), webhook.Event{AddedRepositories: tt.added}, []api.Repo{repo, other}); err != nil {
				t.Fatal(err)
			}
			if gh.calls != tt.want*2 {
				t.Fatalf("GitHub calls = %d", gh.calls)
			}
		})
	}
}

func TestSetupRecheckInstallationEvent(t *testing.T) {
	for _, tt := range []struct {
		name  string
		added []string
		fork  bool
		want  int
	}{
		{"created", []string{"org7/app"}, false, 1},
		{"added", []string{"org7/app", "org7/other"}, false, 1},
		{"fork", []string{"org7/app"}, true, 0},
		{"no setup", []string{"org7/other"}, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.gh.install(7, ghr(70, "org7/app", true), ghr(71, "org7/other", true))
			headID := int64(70)
			if tt.fork {
				headID = 90
			}
			gh := &fakeSetupGitHub{suite: true, status: 201}
			h.c.setup = &setupRecheck{store: store.NewMemory(), gh: gh, now: time.Now}
			h.c.setupPulls = fakeSetupPulls{"org7/app": {{Number: 7, HeadSHA: "head", HeadRef: "archfit/setup", HeadRepoID: headID}}}
			err := h.c.HandleEvent(context.Background(), webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7, Account: "org7", AddedRepositories: tt.added})
			if err != nil {
				t.Fatal(err)
			}
			if gh.calls != tt.want*2 {
				t.Fatalf("calls = %d, want %d", gh.calls, tt.want*2)
			}
		})
	}
}

func TestSetupRecheckFailures(t *testing.T) {
	for _, tt := range []struct {
		name     string
		gh       fakeSetupGitHub
		wantErr  bool
		fallback bool
	}{
		{"suite network", fakeSetupGitHub{suiteErr: errors.New("network")}, true, false},
		{"request network", fakeSetupGitHub{suite: true, requestErr: errors.New("network")}, true, false},
		{"request 500", fakeSetupGitHub{suite: true, status: 500, requestErr: errors.New("server")}, true, false},
		{"empty runs", fakeSetupGitHub{suite: true, status: 403, requestErr: errors.New("forbidden"), runErr: api.ErrNotFound}, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gh := tt.gh
			s := setupRecheck{store: store.NewMemory(), gh: &gh, now: time.Now}
			repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 1}
			_, err := s.Run(context.Background(), repo, 1, "head")
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if tt.fallback && !strings.Contains(gh.body, "/actions/workflows/archfit.yaml") {
				t.Fatalf("body = %q", gh.body)
			}
			if tt.wantErr && gh.comments != 0 {
				t.Fatal("comment on transient error")
			}
		})
	}
}

func TestSetupRecheckCreatedWithoutRepositories(t *testing.T) {
	h := newHarness(t)
	h.gh.install(7, ghr(70, "org7/app", true))
	gh := &fakeSetupGitHub{suite: true, status: 201}
	h.c.setup = &setupRecheck{store: store.NewMemory(), gh: gh, now: time.Now}
	h.c.setupPulls = fakeSetupPulls{"org7/app": {{Number: 7, HeadSHA: "head", HeadRef: "archfit/setup", HeadRepoID: 70}}}
	err := h.c.HandleEvent(context.Background(), webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7, Account: "org7", InstallationCreated: true, RepositoriesOmitted: true})
	if err != nil || gh.calls != 2 {
		t.Fatalf("calls %d, err %v", gh.calls, err)
	}
}
