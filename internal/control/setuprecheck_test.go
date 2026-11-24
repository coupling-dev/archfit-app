package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/wording"
)

type fakeSetupGitHub struct {
	calls, comments              int
	runCalls                     int
	body                         string
	suite, marker                bool
	status                       int
	suiteErr, requestErr, runErr error
	commentErr                   error
	pulls                        fakeSetupPulls
	onRequest                    func()
	pullErrors                   map[int]error
	pullCalls                    int
}

func (f *fakeSetupGitHub) PullSummary(_ context.Context, r api.RepoRef, pr int) (api.Pull, error) {
	f.pullCalls++
	if err := f.pullErrors[pr]; err != nil {
		return api.Pull{}, err
	}
	for _, p := range f.pulls[r.FullName] {
		if p.Number == pr {
			p.State = "open"
			return p, nil
		}
	}
	return api.Pull{}, api.ErrNotFound
}
func (f *fakeSetupGitHub) ActionsSuite(context.Context, api.Repo, string) (int64, bool, error) {
	f.calls++
	return 1, f.suite, f.suiteErr
}
func (f *fakeSetupGitHub) RerequestSuite(context.Context, api.Repo, int64) (int, error) {
	f.calls++
	if f.onRequest != nil {
		f.onRequest()
	}
	return f.status, f.requestErr
}
func (f *fakeSetupGitHub) RunURL(context.Context, api.Repo, int64) (string, error) {
	f.calls++
	f.runCalls++
	return "https://github.com/o/r/actions/runs/1/job/2", f.runErr
}
func (f *fakeSetupGitHub) HasMarkerComment(context.Context, api.Repo, int, string) (bool, error) {
	f.calls++
	return f.marker, nil
}
func (f *fakeSetupGitHub) Comment(_ context.Context, _ api.Repo, _ int, body string) error {
	f.body = body
	f.calls++
	f.comments++
	return f.commentErr
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

func (f fakeSetupPulls) SearchSetupPulls(_ context.Context, _ int64, _ string) ([]api.SetupCandidate, error) {
	var out []api.SetupCandidate
	for repo, pulls := range f {
		for _, p := range pulls {
			out = append(out, api.SetupCandidate{Repo: repo, Number: p.Number})
		}
	}
	return out, nil
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
			gh := &fakeSetupGitHub{suite: true, status: 201, pulls: tt.pulls}
			st := store.NewMemory()
			c := Control{setup: &setupRecheck{store: st, gh: gh, now: time.Now}, setupPulls: tt.pulls}
			if err := c.recheckAdded(context.Background(), webhook.Event{InstallationID: 1, Account: "o", AddedRepositories: tt.added}, []api.Repo{repo, other}); err != nil {
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
			gh.pulls = h.c.setupPulls.(fakeSetupPulls)
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
	gh.pulls = h.c.setupPulls.(fakeSetupPulls)
	err := h.c.HandleEvent(context.Background(), webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7, Account: "org7", InstallationCreated: true, RepositoriesOmitted: true})
	if err != nil || gh.calls != 2 {
		t.Fatalf("calls %d, err %v", gh.calls, err)
	}
}

func TestSetupSearchSkipsUnselected(t *testing.T) {
	pulls := fakeSetupPulls{"o/other": {{Number: 1, HeadRef: "archfit/setup", HeadRepoID: 2, HeadSHA: "head"}}}
	gh := &fakeSetupGitHub{pulls: pulls, suite: true, status: 201}
	c := Control{setupPulls: pulls, setup: &setupRecheck{store: store.NewMemory(), gh: gh, now: time.Now}}
	err := c.recheckAdded(context.Background(), webhook.Event{InstallationID: 1, Account: "o", AddedRepositories: []string{"o/r"}}, []api.Repo{{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 1}})
	if err != nil || gh.calls != 0 {
		t.Fatalf("err %v calls %d", err, gh.calls)
	}
}

func TestSetupRecheckFinalRecordSurvivesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := store.NewMemory()
	gh := &fakeSetupGitHub{suite: true, status: 201, onRequest: cancel}
	s := setupRecheck{store: st, gh: gh, now: time.Now}
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 1}
	outcome, err := s.Run(ctx, repo, 1, "head")
	if err != nil || outcome != "rerequested" {
		t.Fatalf("outcome %q err %v", outcome, err)
	}
	outcome, err = s.Run(context.Background(), repo, 1, "head")
	if err != nil || outcome != "done" {
		t.Fatalf("repeat %q err %v", outcome, err)
	}
}

func TestSetupRecheckPreservesGitHubError(t *testing.T) {
	for _, gh := range []*fakeSetupGitHub{{suiteErr: api.ErrNotFound}, {suite: true, requestErr: api.ErrNotFound}} {
		s := setupRecheck{store: store.NewMemory(), gh: gh, now: time.Now}
		repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 1}
		_, err := s.Run(context.Background(), repo, 1, "head")
		if !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("lost cause: %v", err)
		}
	}
}

type orderedSetupSearch struct {
	calls   int
	results []api.SetupCandidate
}

func (f *orderedSetupSearch) SearchSetupPulls(context.Context, int64, string) ([]api.SetupCandidate, error) {
	f.calls++
	return f.results, nil
}

func TestSetupSearchContinuesAfterPermanentPull(t *testing.T) {
	search := &orderedSetupSearch{results: []api.SetupCandidate{{Repo: "o/r", Number: 1}, {Repo: "o/r", Number: 2}}}
	gh := &fakeSetupGitHub{suite: true, status: 201, pullErrors: map[int]error{1: api.ErrNotFound}, pulls: fakeSetupPulls{"o/r": {{Number: 2, HeadSHA: "head", HeadRef: "archfit/setup", HeadRepoID: 42}}}}
	c := Control{setupPulls: search, setup: &setupRecheck{store: store.NewMemory(), gh: gh, now: time.Now}}
	err := c.recheckAdded(context.Background(), webhook.Event{InstallationID: 1, Account: "o", AddedRepositories: []string{"o/r"}}, []api.Repo{{RepoRef: api.RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 42}})
	if err != nil || search.calls != 1 || gh.pullCalls != 2 || gh.calls != 2 {
		t.Fatalf("err %v searches %d pulls %d calls %d", err, search.calls, gh.pullCalls, gh.calls)
	}
}

func TestWelcomePullOpened(t *testing.T) {
	for _, tt := range []struct {
		name, action, branch string
		headID               int64
		marked               bool
		want                 int
	}{
		{"opened", "opened", "archfit/setup", 70, false, 1},
		{"reopened", "reopened", "archfit/setup", 70, false, 1},
		{"fork", "opened", "archfit/setup", 99, false, 0},
		{"other branch", "opened", "feature", 70, false, 0},
		{"existing marker", "opened", "archfit/setup", 70, true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.gh.install(7, ghr(70, "org7/app", true))
			gh := &fakeSetupGitHub{marker: tt.marked, pulls: fakeSetupPulls{"org7/app": {{Number: 7, HeadRef: tt.branch, HeadRepoID: tt.headID}}}}
			h.c.setup = &setupRecheck{store: store.NewMemory(), gh: gh, now: time.Now}
			h.c.admit = admitAll{}
			body := `{"action":"` + tt.action + `","installation":{"id":7},"repository":{"id":70,"full_name":"org7/app"},"pull_request":{"number":7,"head":{"ref":"` + tt.branch + `","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repo":{"id":` + fmt.Sprint(tt.headID) + `,"full_name":"org7/app"}}}}`
			e, err := webhook.Parse("pull_request", "delivery", []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if err := h.c.HandleEvent(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if gh.comments != tt.want {
				t.Fatalf("comments = %d, want %d", gh.comments, tt.want)
			}
			if tt.want == 1 {
				if !strings.Contains(gh.body, "<!-- archfit:welcome -->") {
					t.Fatalf("missing welcome marker: %q", gh.body)
				}
				// Answers need the first policy approved: the welcome says so before an owner meets the refusal.
				if !strings.Contains(gh.body, "after a repository administrator named as an owner approves the first policy") {
					t.Fatalf("welcome does not say the first policy is approved before answers: %q", gh.body)
				}
				if words := wording.StoryNotUsedIn(gh.body); len(words) > 0 {
					t.Fatalf("welcome words: %v", words)
				}
				gh.marker = true
				if err := h.c.HandleEvent(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				if gh.comments != 1 {
					t.Fatalf("redelivery comments = %d", gh.comments)
				}
			}
		})
	}
}
