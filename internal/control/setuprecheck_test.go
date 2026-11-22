package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

type fakeSetupGitHub struct {
	calls, comments int
	suite, marker   bool
	status          int
}

func (f *fakeSetupGitHub) ActionsSuite(context.Context, api.Repo, string) (int64, bool, error) {
	f.calls++
	return 1, f.suite, nil
}
func (f *fakeSetupGitHub) RerequestSuite(context.Context, api.Repo, int64) (int, error) {
	f.calls++
	return f.status, nil
}
func (f *fakeSetupGitHub) LatestRunURL(context.Context, api.Repo, string) (string, error) {
	f.calls++
	return "https://github.com/o/r/actions/runs/1", nil
}
func (f *fakeSetupGitHub) HasMarkerComment(context.Context, api.Repo, int) (bool, error) {
	f.calls++
	return f.marker, nil
}
func (f *fakeSetupGitHub) Comment(context.Context, api.Repo, int, string) error {
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
			if err != nil || got != tt.want {
				t.Fatalf("Run = %q, %v; want %q", got, err, tt.want)
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
