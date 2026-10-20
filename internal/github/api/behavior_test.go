package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// anyItem decodes as an element of every list the client reads.
func anyItem(i int) map[string]any {
	return map[string]any{
		"id": i + 1, "number": i + 1, "full_name": fmt.Sprintf("o/r%d", i), "filename": fmt.Sprintf("f%d.go", i),
		"name": "archfit", "state": "APPROVED", "body": "b", "account": map[string]any{"login": "acme"},
		"user": map[string]any{"login": "archfit[bot]"}, "app": map[string]any{"id": 4242},
	}
}

// pagedAny serves total items, 100 per page, in the shape the path asks for.
func pagedAny(w http.ResponseWriter, r *http.Request, total int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	items := []map[string]any{}
	for i := (page - 1) * 100; i < min(page*100, total); i++ {
		items = append(items, anyItem(i))
	}
	if page*100 < total {
		w.Header().Set("Link", linkHeader(r.URL.Path, page+1))
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/repositories"):
		writeJSON(w, http.StatusOK, map[string]any{"repositories": items})
	case strings.HasSuffix(r.URL.Path, "/check-runs"):
		writeJSON(w, http.StatusOK, map[string]any{"check_runs": items})
	case r.URL.Path == "/user/installations":
		writeJSON(w, http.StatusOK, map[string]any{"installations": items[:min(len(items), 1)]})
	default:
		writeJSON(w, http.StatusOK, items)
	}
}

func TestRateLimitExhaustionNeverLooksLikeData(t *testing.T) {
	limited := func(w http.ResponseWriter) {
		reply{status: 403, header: map[string]string{
			"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.Itoa(1_800_000_000 + 600),
		}, body: `{"message":"API rate limit exceeded"}`}.write(w)
	}
	reads := func(c *Client) map[string]func(context.Context) (bool, error) {
		calls := repoRefCalls(c, repoA)
		for name := range calls {
			if strings.HasPrefix(name, "Changes.") || slices.Contains([]string{"Checks.CreateCheckRun", "Checks.UpdateCheckRun",
				"Comments.CreateComment", "Comments.UpdateComment", "Actions.Rerun", "Actions.Dispatch"}, name) {
				delete(calls, name)
			}
		}
		calls["Repos.ListInstallations"] = func(ctx context.Context) (bool, error) {
			v, err := c.ListInstallations(ctx)
			return v != nil, err
		}
		calls["Repos.ListRepos"] = func(ctx context.Context) (bool, error) {
			v, err := c.ListRepos(ctx, 1)
			return v != nil, err
		}
		calls["Repos.ResolveRepo"] = func(ctx context.Context) (bool, error) {
			v, err := c.ResolveRepo(ctx, "o/r")
			return v != Repo{}, err
		}
		calls["Pulls.TeamMember"] = func(ctx context.Context) (bool, error) {
			return c.TeamMember(ctx, repoA, "acme/core", "alice")
		}
		calls["Users.Permission"] = func(ctx context.Context) (bool, error) {
			v, err := c.Permission(ctx, "user-token", "o/r")
			return v != "", err
		}
		calls["Users.UserRepos"] = func(ctx context.Context) (bool, error) {
			v, err := c.UserRepos(ctx, "user-token")
			return v != nil, err
		}
		return calls
	}
	verify := func(t *testing.T, env *testEnv, data bool, err error) {
		t.Helper()
		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("err = %v, want ErrRateLimited", err)
		}
		if data {
			t.Error("data was returned while rate limited")
		}
		if env.clock.total() > 30*time.Second {
			t.Errorf("waited %v, over the budget", env.clock.total())
		}
	}

	t.Run("on the first request", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/app" {
				appHandler(w, r)
				return
			}
			limited(w)
		}))
		for name, call := range reads(env.c) {
			t.Run(name, func(t *testing.T) {
				data, err := call(context.Background())
				verify(t, env, data, err)
			})
		}
	})

	t.Run("on page 3 of a list", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/app":
				appHandler(w, r)
			case r.URL.Query().Get("page") == "3":
				limited(w)
			default:
				pagedAny(w, r, 500)
			}
		}))
		lists := []string{"Repos.ListInstallations", "Repos.ListRepos", "Pulls.OpenPulls", "Pulls.ChangedFiles", "Pulls.Reviews",
			"Checks.CheckRuns", "Comments.AppComments", "Users.UserRepos"}
		calls := reads(env.c)
		for _, name := range lists {
			t.Run(name, func(t *testing.T) {
				env.gh.reset()
				data, err := calls[name](context.Background())
				verify(t, env, data, err)
				if n := len(env.gh.requests()); n < 3 {
					t.Errorf("requests = %d, want the list to reach page 3", n)
				}
			})
		}
	})
}

func TestLargePullRequestIsCompleteOrSaysTruncated(t *testing.T) {
	ctx := context.Background()
	const protected = ".archfit.yaml"
	// pull number → files GitHub lists, and the pull's own changed_files count
	listed := map[string]int{"1": 950, "2": 1001, "3": 300, "4": 3000}
	changed := map[string]int{"1": 950, "2": 1001, "3": 3500, "4": 3500}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/pulls/{n}/files", func(w http.ResponseWriter, r *http.Request) {
			total := listed[r.PathValue("n")]
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			page = max(page, 1)
			files := []map[string]any{}
			for i := (page - 1) * 100; i < min(page*100, total); i++ {
				name := fmt.Sprintf("src/f%d.go", i)
				if i == total-1 {
					name = protected
				}
				files = append(files, map[string]any{"filename": name})
			}
			if page*100 < total {
				w.Header().Set("Link", linkHeader(r.URL.Path, page+1))
			}
			writeJSON(w, http.StatusOK, files)
		})
		mux.HandleFunc("GET /repos/o/r/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"number": 1, "changed_files": changed[r.PathValue("n")]})
		})
		mux.HandleFunc("GET /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
			pagedAny(w, r, 1001)
		})
	})

	tests := []struct {
		name          string
		number        int
		wantPaths     int
		wantTruncated bool
		wantProtected bool
	}{
		{name: "950 files over 10 pages", number: 1, wantPaths: 950, wantProtected: true},
		{name: "1001 files", number: 2, wantPaths: 1000, wantTruncated: true},
		{name: "changed_files 3500, list ends early", number: 3, wantPaths: 300, wantTruncated: true, wantProtected: true},
		{name: "changed_files 3500, list reaches the cap", number: 4, wantPaths: 1000, wantTruncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, truncated, err := env.c.ChangedFiles(ctx, repoA, tt.number)
			if err != nil {
				t.Fatal(err)
			}
			if len(paths) != tt.wantPaths || truncated != tt.wantTruncated {
				t.Errorf("paths = %d, truncated = %v; want %d, %v", len(paths), truncated, tt.wantPaths, tt.wantTruncated)
			}
			if got := slices.Contains(paths, protected); got != tt.wantProtected {
				t.Errorf("protected path present = %v, want %v", got, tt.wantProtected)
			}
		})
	}

	t.Run("OpenPulls with 1001 pulls", func(t *testing.T) {
		pulls, err := env.c.OpenPulls(ctx, repoA)
		if err == nil || pulls != nil {
			t.Errorf("OpenPulls = %d pulls, %v; want an error and no list", len(pulls), err)
		}
	})
}

func TestRetriedWritesStayIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(t)
	env := muxEnv(t, repo.routes)
	before := repo.commitCount()
	files := []FileChange{{Path: ".archfit.yaml", Content: []byte("version: 1\n")}}
	np := NewPull{Title: "Adopt archfit", Body: "b", Head: "archfit/setup", Base: "main", Draft: true}

	var shas []string
	var numbers []int
	for range 2 {
		if err := env.c.EnsureBranch(ctx, repoA, "archfit/setup", "main"); err != nil {
			t.Fatal(err)
		}
		sha, err := env.c.Commit(ctx, repoA, "archfit/setup", "add archfit", files)
		if err != nil {
			t.Fatal(err)
		}
		pull, err := env.c.EnsurePull(ctx, repoA, np)
		if err != nil {
			t.Fatal(err)
		}
		shas, numbers = append(shas, sha), append(numbers, pull.Number)
	}

	if n := env.gh.count("POST", "/repos/o/r/git/refs"); n != 1 {
		t.Errorf("branches created = %d, want 1", n)
	}
	if got := repo.commitCount() - before; got != 1 || shas[0] != shas[1] {
		t.Errorf("commits made = %d (%v), want 1", got, shas)
	}
	if len(repo.pulls) != 1 || numbers[0] != numbers[1] {
		t.Errorf("pulls = %d (%v), want 1", len(repo.pulls), numbers)
	}
}

func TestSweepStaysCheapWithConditionalRequests(t *testing.T) {
	ctx := context.Background()
	var notModified atomic.Int64
	conditional := func(w http.ResponseWriter, r *http.Request, v any) {
		if r.Header.Get("If-None-Match") != "" {
			notModified.Add(1)
		}
		etagJSON(w, r, `W/"`+r.URL.Path+`"`, v)
	}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/{name}/pulls", func(w http.ResponseWriter, r *http.Request) {
			conditional(w, r, []any{pullFixture(1, "open", "feature", "o/"+r.PathValue("name"), nil)})
		})
		mux.HandleFunc("GET /repos/o/{name}/commits/head1/check-runs", func(w http.ResponseWriter, r *http.Request) {
			conditional(w, r, map[string]any{"check_runs": []map[string]any{
				{"id": 7, "name": "archfit", "head_sha": "head1", "status": "in_progress", "app": map[string]any{"id": 4242}},
			}})
		})
	})

	type snapshot struct {
		Pulls []Pull
		Runs  []CheckRun
	}
	sweep := func() []snapshot {
		out := []snapshot{}
		for i := range 50 {
			ref := RepoRef{InstallationID: 1, FullName: fmt.Sprintf("o/r%d", i)}
			pulls, err := env.c.OpenPulls(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			runs, err := env.c.CheckRuns(ctx, ref, pulls[0].HeadSHA, "archfit")
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, snapshot{pulls, runs})
		}
		return out
	}

	first := sweep()
	if notModified.Load() != 0 {
		t.Fatal("the first pass sent conditional requests")
	}
	env.gh.reset()
	second := sweep()
	if got := len(env.gh.requests()); got != 100 || notModified.Load() != 100 {
		t.Errorf("second pass: %d requests, %d answered 304; want 100 and 100", got, notModified.Load())
	}
	if !reflect.DeepEqual(first, second) || len(first[0].Runs) != 1 {
		t.Error("the second pass differs from the first")
	}
}

func TestUserSeesOnlyTheirRepositories(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	installations := map[string][]int{"Bearer alice-token": {1}, "Bearer bob-token": {2}}
	allowed := func(r *http.Request, id int) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(installations[r.Header.Get("Authorization")], id)
	}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			ids := installations[r.Header.Get("Authorization")]
			mu.Unlock()
			items := []map[string]any{}
			for _, id := range ids {
				items = append(items, map[string]any{"id": id})
			}
			// An ETag tempts a cache; Users calls must not take the bait.
			etagJSON(w, r, `"installations"`, map[string]any{"installations": items})
		})
		mux.HandleFunc("GET /user/installations/{id}/repositories", func(w http.ResponseWriter, r *http.Request) {
			id, _ := strconv.Atoi(r.PathValue("id"))
			if !allowed(r, id) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			etagJSON(w, r, `"repositories"`, map[string]any{"repositories": []map[string]any{
				{"id": id * 10, "full_name": fmt.Sprintf("org%d/app", id), "default_branch": "main"},
			}})
		})
		mux.HandleFunc("GET /repos/{org}/app", func(w http.ResponseWriter, r *http.Request) {
			id, _ := strconv.Atoi(strings.TrimPrefix(r.PathValue("org"), "org"))
			if !allowed(r, id) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			etagJSON(w, r, `"repo"`, map[string]any{"permissions": map[string]bool{"admin": true, "push": true, "pull": true}})
		})
	})
	names := func(token string) []string {
		repos, err := env.c.UserRepos(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range repos {
			out = append(out, fmt.Sprintf("%d:%s", r.InstallationID, r.FullName))
		}
		return out
	}
	permission := func(token, repo string) string {
		p, err := env.c.Permission(ctx, token, repo)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	if got := names("alice-token"); !reflect.DeepEqual(got, []string{"1:org1/app"}) {
		t.Errorf("alice sees %v", got)
	}
	if got := names("bob-token"); !reflect.DeepEqual(got, []string{"2:org2/app"}) {
		t.Errorf("bob sees %v", got)
	}
	if permission("bob-token", "org2/app") != "admin" || permission("bob-token", "org1/app") != "none" ||
		permission("alice-token", "org2/app") != "none" {
		t.Error("permissions cross users")
	}

	mu.Lock()
	installations["Bearer bob-token"] = nil
	mu.Unlock()
	if got := names("bob-token"); len(got) != 0 {
		t.Errorf("bob still sees %v after losing access", got)
	}
	if got := permission("bob-token", "org2/app"); got != "none" {
		t.Errorf("bob's permission after the loss = %q, want none", got)
	}
	if got := names("alice-token"); !reflect.DeepEqual(got, []string{"1:org1/app"}) {
		t.Errorf("alice sees %v", got)
	}

	for _, r := range env.gh.requests() {
		if r.IfNoneMatch != "" {
			t.Errorf("%s: a Users call was conditional, so something was cached", r)
		}
		if r.Auth != "Bearer alice-token" && r.Auth != "Bearer bob-token" {
			t.Errorf("%s: Authorization = %q", r, r.Auth)
		}
	}
	if env.ts.jwtCalls != 0 || len(env.ts.minted) != 0 {
		t.Error("App credentials were used for Users calls")
	}
}

func TestCallStatsCountEachRoundTripOfOneContext(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/pulls/{n}", func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Millisecond)
			writeJSON(w, http.StatusOK, pullFixture(1, "open", "feature", "o/r", nil))
		})
	})
	ctx, stats := WithCallStats(context.Background())
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := env.c.PullSummary(ctx, repoA, 1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, err := env.c.PullSummary(context.Background(), repoA, 1); err != nil {
		t.Fatal(err)
	}
	if stats.Calls() != 3 || stats.Duration() < 15*time.Millisecond {
		t.Errorf("stats = %d calls, %v; want the three calls of ctx, about 5 ms each", stats.Calls(), stats.Duration())
	}
}
