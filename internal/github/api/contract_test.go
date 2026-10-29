package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	appJWT = "Bearer app-jwt"
	inst1  = "Bearer inst-1-v0"
)

func wantErrIs(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: err = %v, want %v", what, err, target)
	}
}

func wantPlainErr(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
		t.Errorf("%s: err = %v, want a plain error", what, err)
	}
}

func TestReposContract(t *testing.T) {
	ctx := context.Background()
	repoJSON := map[string]any{"id": 10, "full_name": "acme/app", "private": true, "default_branch": "trunk", "extra": "ignored"}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, []map[string]any{
				{"id": 1, "account": map[string]any{"login": "acme"}, "suspended_at": nil},
				{"id": 2, "account": map[string]any{"login": "paused"}, "suspended_at": "2026-01-02T03:04:05Z"},
			})
		})
		mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 1, "repositories": []any{repoJSON}})
		})
		mux.HandleFunc("GET /repos/acme/app/installation", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"id": 7})
		})
		mux.HandleFunc("GET /repos/acme/app", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, repoJSON)
		})
		mux.HandleFunc("GET /repos/acme/app/contents/{path...}", func(w http.ResponseWriter, r *http.Request) {
			switch r.PathValue("path") {
			case "cfg/archfit.yaml":
				encoded := base64.StdEncoding.EncodeToString([]byte("version: 1\nmode: gate\n"))
				writeJSON(w, http.StatusOK, map[string]any{"type": "file", "encoding": "base64", "size": 22,
					"content": encoded[:10] + "\n" + encoded[10:] + "\n", "sha": "blob-sha-1"})
			case "dir":
				writeJSON(w, http.StatusOK, []map[string]any{{"type": "file", "name": "a"}})
			case "link":
				writeJSON(w, http.StatusOK, map[string]any{"type": "symlink", "target": "cfg/archfit.yaml", "sha": "x"})
			case "big.bin":
				writeJSON(w, http.StatusOK, map[string]any{"type": "file", "encoding": "none", "size": 2 << 20, "content": "", "sha": "big"})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		mux.HandleFunc("GET /repos/acme/app/git/ref/heads/{branch...}", func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("branch") != "trunk" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ref": "refs/heads/trunk", "object": map[string]any{"type": "commit", "sha": "c0ffee"}})
		})
	})
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	want := Repo{RepoRef: ref, ID: 10, Private: true, DefaultBranch: "trunk"}

	installations, err := env.c.ListInstallations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(installations, []Installation{{ID: 1, Account: "acme"}}) {
		t.Errorf("ListInstallations = %v, want the suspended one skipped", installations)
	}

	repos, err := env.c.ListRepos(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repos, []Repo{want}) {
		t.Errorf("ListRepos = %+v, want %+v", repos, want)
	}

	resolved, err := env.c.ResolveRepo(ctx, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("ResolveRepo = %+v, want %+v", resolved, want)
	}

	content, blobSHA, err := env.c.File(ctx, ref, "trunk", "cfg/archfit.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "version: 1\nmode: gate\n" || blobSHA != "blob-sha-1" {
		t.Errorf("File = %q, %q", content, blobSHA)
	}
	_, _, err = env.c.File(ctx, ref, "trunk", "missing.yaml")
	wantErrIs(t, "missing file", err, ErrNotFound)
	for _, path := range []string{"dir", "link", "big.bin"} {
		got, _, err := env.c.File(ctx, ref, "trunk", path)
		wantPlainErr(t, "File "+path, err)
		if got != nil {
			t.Errorf("File %s returned content with an error", path)
		}
	}

	head, err := env.c.BranchHead(ctx, ref, "trunk")
	if err != nil || head != "c0ffee" {
		t.Errorf("BranchHead = %q, %v", head, err)
	}
	_, err = env.c.BranchHead(ctx, ref, "gone")
	wantErrIs(t, "missing branch", err, ErrNotFound)

	for _, r := range env.gh.requests() {
		wantAuth := "Bearer inst-7-v0"
		if r.Path == "/app/installations" || r.Path == "/repos/acme/app/installation" {
			wantAuth = appJWT
		}
		if r.Auth != wantAuth {
			t.Errorf("%s: Authorization = %q, want %q", r, r.Auth, wantAuth)
		}
		if r.Host != apiHost || r.Header.Get("Accept") != "application/vnd.github+json" ||
			r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || r.Header.Get("User-Agent") != "archfit-app" {
			t.Errorf("%s: host or fixed headers wrong: %q %v", r, r.Host, r.Header)
		}
	}
	file := findRequest(t, env.gh.requests(), "GET", "/repos/acme/app/contents/cfg/archfit.yaml")
	if file.Query.Get("ref") != "trunk" {
		t.Errorf("File ref query = %q", file.Query.Get("ref"))
	}
}

func pullFixture(number int, state, headRef, headRepo string, mergedAt any) map[string]any {
	return map[string]any{
		"number": number, "node_id": fmt.Sprintf("PR_%d", number), "state": state, "draft": false, "merged_at": mergedAt,
		"body": "Body of the pull", "html_url": fmt.Sprintf("https://github.com/o/r/pull/%d", number),
		"title": fmt.Sprintf("Pull %d", number), "user": map[string]any{"login": "octo"},
		"updated_at": "2026-09-01T10:00:00Z", "changed_files": 2,
		"head": map[string]any{"sha": fmt.Sprintf("head%d", number), "ref": headRef, "repo": map[string]any{"full_name": headRepo}},
		"base": map[string]any{"sha": fmt.Sprintf("base%d", number), "ref": "main", "repo": map[string]any{"full_name": "o/r"}},
	}
}

func TestPullsContract(t *testing.T) {
	ctx := context.Background()
	merged := pullFixture(3, "closed", "done", "o/r", "2026-08-30T09:00:00Z")
	merged["body"] = nil
	draft := pullFixture(8, "open", "wip", "o/r", nil)
	draft["draft"] = true
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
			switch r.PathValue("n") {
			case "1", "4":
				writeJSON(w, http.StatusOK, pullFixture(1, "open", "feature", "o/r", nil))
			case "2":
				writeJSON(w, http.StatusOK, pullFixture(2, "open", "feature", "stranger/r", nil))
			case "3":
				writeJSON(w, http.StatusOK, merged)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		mux.HandleFunc("GET /repos/o/r/compare/{spec}", func(w http.ResponseWriter, r *http.Request) {
			base, _, _ := strings.Cut(r.PathValue("spec"), "...")
			writeJSON(w, http.StatusOK, map[string]any{"merge_base_commit": map[string]any{"sha": "mb-" + base}, "files": []any{}})
		})
		mux.HandleFunc("GET /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			switch q.Get("head") {
			case "":
				writeJSON(w, http.StatusOK, []any{pullFixture(1, "open", "feature", "o/r", nil), draft})
			case "o:feature":
				// GitHub honours per_page=1 with direction=desc: the newest only.
				writeJSON(w, http.StatusOK, []any{pullFixture(9, "open", "feature", "o/r", nil)})
			case "o:done":
				writeJSON(w, http.StatusOK, []any{merged})
			case "o:fork-only":
				// A fork of the same owner name must never count, even if GitHub listed it.
				writeJSON(w, http.StatusOK, []any{pullFixture(12, "open", "fork-only", "stranger/r", nil)})
			default:
				writeJSON(w, http.StatusOK, []any{})
			}
		})
		mux.HandleFunc("GET /repos/o/r/pulls/4/files", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, []map[string]any{
				{"filename": "main.go", "status": "modified"},
				{"filename": "docs/new.yaml", "previous_filename": ".archfit.yaml", "status": "renamed"},
			})
		})
		mux.HandleFunc("GET /repos/o/r/pulls/5/reviews", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, []map[string]any{
				{"user": map[string]any{"login": "alice"}, "state": "APPROVED", "commit_id": "c1", "submitted_at": "2026-10-27T10:00:00Z"},
				{"user": map[string]any{"login": "bob"}, "state": "PENDING", "commit_id": "c1"},
				{"user": map[string]any{"login": "alice"}, "state": "CHANGES_REQUESTED", "commit_id": "c2"},
			})
		})
		mux.HandleFunc("GET /orgs/acme/teams/core/memberships/{login}", func(w http.ResponseWriter, r *http.Request) {
			switch r.PathValue("login") {
			case "alice":
				writeJSON(w, http.StatusOK, map[string]any{"state": "active", "role": "member"})
			case "bob":
				writeJSON(w, http.StatusOK, map[string]any{"state": "pending"})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
	})

	t.Run("Pull", func(t *testing.T) {
		same, err := env.c.Pull(ctx, repoA, 1)
		if err != nil {
			t.Fatal(err)
		}
		want := Pull{Number: 1, Title: "Pull 1", Author: "octo", State: "open", Body: "Body of the pull", URL: "https://github.com/o/r/pull/1",
			HeadSHA: "head1", HeadRef: "feature", HeadRepo: "o/r", BaseRef: "main", BaseSHA: "base1", MergeBaseSHA: "mb-base1",
			UpdatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)}
		if same != want {
			t.Errorf("Pull = %+v\nwant   %+v", same, want)
		}
		findRequest(t, env.gh.requests(), "GET", "/repos/o/r/compare/base1...head1")

		fork, err := env.c.Pull(ctx, repoA, 2)
		if err != nil {
			t.Fatal(err)
		}
		if fork.HeadRepo != "stranger/r" || fork.HeadRepo == repoA.FullName {
			t.Errorf("fork HeadRepo = %q", fork.HeadRepo)
		}
		done, err := env.c.Pull(ctx, repoA, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !done.Merged || done.State != "closed" || done.Body != "" {
			t.Errorf("merged pull = %+v", done)
		}
		_, err = env.c.Pull(ctx, repoA, 404)
		wantErrIs(t, "missing pull", err, ErrNotFound)
	})

	t.Run("PullSummary", func(t *testing.T) {
		env.gh.reset()
		got, err := env.c.PullSummary(ctx, repoA, 1)
		if err != nil {
			t.Fatal(err)
		}
		full, err := env.c.Pull(ctx, repoA, 1)
		if err != nil {
			t.Fatal(err)
		}
		full.MergeBaseSHA = ""
		if got != full {
			t.Errorf("PullSummary = %+v\nwant Pull without the merge base %+v", got, full)
		}
		if reqs := env.gh.requests(); len(reqs) != 3 || reqs[0].String() != "GET /repos/o/r/pulls/1" {
			t.Errorf("requests = %v, want one GET of the pull, then Pull's two", reqs)
		}
		_, err = env.c.PullSummary(ctx, repoA, 404)
		wantErrIs(t, "missing pull", err, ErrNotFound)
	})

	t.Run("OpenPulls", func(t *testing.T) {
		env.gh.reset()
		pulls, err := env.c.OpenPulls(ctx, repoA)
		if err != nil {
			t.Fatal(err)
		}
		if len(pulls) != 2 || !pulls[1].Draft || pulls[1].MergeBaseSHA != "" {
			t.Errorf("OpenPulls = %+v, want the draft listed and no merge base", pulls)
		}
		if q := env.gh.requests()[0].Query; q.Get("state") != "open" || q.Get("per_page") != "100" {
			t.Errorf("query = %v", q)
		}
	})

	t.Run("PullByHead", func(t *testing.T) {
		env.gh.reset()
		newest, err := env.c.PullByHead(ctx, repoA, "feature")
		if err != nil || newest.Number != 9 {
			t.Errorf("open: %+v, %v", newest, err)
		}
		q := env.gh.requests()[0].Query
		for k, want := range map[string]string{"state": "all", "head": "o:feature", "sort": "created", "direction": "desc", "per_page": "1"} {
			if q.Get(k) != want {
				t.Errorf("query %s = %q, want %q", k, q.Get(k), want)
			}
		}
		done, err := env.c.PullByHead(ctx, repoA, "done")
		if err != nil || !done.Merged || done.Number != 3 {
			t.Errorf("merged-only: %+v, %v", done, err)
		}
		_, err = env.c.PullByHead(ctx, repoA, "fork-only")
		wantErrIs(t, "fork-only", err, ErrNotFound)
		_, err = env.c.PullByHead(ctx, repoA, "nothing")
		wantErrIs(t, "no pull", err, ErrNotFound)
	})

	t.Run("ChangedFiles", func(t *testing.T) {
		paths, truncated, err := env.c.ChangedFiles(ctx, repoA, 4)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(paths, []string{"main.go", "docs/new.yaml", ".archfit.yaml"}) || truncated {
			t.Errorf("ChangedFiles = %v, truncated %v", paths, truncated)
		}
	})

	t.Run("Reviews", func(t *testing.T) {
		reviews, err := env.c.Reviews(ctx, repoA, 5)
		if err != nil {
			t.Fatal(err)
		}
		want := []Review{
			{Reviewer: "alice", State: "APPROVED", CommitSHA: "c1", SubmittedAt: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)},
			{Reviewer: "alice", State: "CHANGES_REQUESTED", CommitSHA: "c2"},
		}
		if !reflect.DeepEqual(reviews, want) {
			t.Errorf("Reviews = %+v, want %+v", reviews, want)
		}
	})

	t.Run("TeamMember", func(t *testing.T) {
		for login, want := range map[string]bool{"alice": true, "bob": false, "carol": false} {
			got, err := env.c.TeamMember(ctx, repoA, "acme/core", login)
			if err != nil || got != want {
				t.Errorf("TeamMember(%s) = %v, %v; want %v", login, got, err, want)
			}
		}
		env.gh.reset()
		for _, team := range []string{"core", "acme/core/extra", "/core", "acme/", "../core"} {
			if _, err := env.c.TeamMember(ctx, repoA, team, "alice"); err == nil {
				t.Errorf("team %q: want error", team)
			}
		}
		if n := len(env.gh.requests()); n != 0 {
			t.Errorf("bad team strings sent %d requests", n)
		}
	})

	for _, r := range env.gh.requests() {
		if r.Auth != inst1 {
			t.Errorf("%s: Authorization = %q", r, r.Auth)
		}
	}
}

func TestChecksContract(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /repos/o/r/check-runs", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]any{"id": 99, "name": "archfit"})
		})
		mux.HandleFunc("PATCH /repos/o/r/check-runs/99", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"id": 99})
		})
		mux.HandleFunc("GET /repos/o/r/commits/abc123/check-runs", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 2, "check_runs": []map[string]any{
				{"id": 99, "name": "archfit", "head_sha": "abc123", "external_id": "ext-1", "details_url": "https://app.example/x", "status": "completed",
					"conclusion": "success", "started_at": "2026-09-21T08:00:00Z", "app": map[string]any{"id": 4242},
					"output": map[string]any{"title": "Gate", "summary": "All good", "annotations_count": 50}},
				{"id": 100, "name": "archfit", "head_sha": "abc123", "status": "completed", "conclusion": "success",
					"app": map[string]any{"id": 1}, "output": map[string]any{"title": "Impostor"}},
			}})
		})
	})

	run := CheckRun{Name: "archfit", HeadSHA: "abc123", ExternalID: "ext-1", DetailsURL: "https://app.example/r/o/r/pull/1", Status: "in_progress", Title: "Gate",
		Summary: "Working", StartedAt: started}
	created, err := env.c.CreateCheckRun(ctx, repoA, run)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 99 || created.ExternalID != "ext-1" {
		t.Errorf("created = %+v", created)
	}
	body := decodeBody(t, findRequest(t, env.gh.requests(), "POST", "/repos/o/r/check-runs"))
	wantCreate := map[string]any{"name": "archfit", "head_sha": "abc123", "external_id": "ext-1", "details_url": "https://app.example/r/o/r/pull/1", "status": "in_progress",
		"started_at": "2026-09-21T08:00:00Z", "output": map[string]any{"title": "Gate", "summary": "Working"}}
	if !reflect.DeepEqual(body, wantCreate) {
		t.Errorf("create body = %v\nwant          %v", body, wantCreate)
	}

	created.Status, created.Conclusion, created.Summary = "completed", "failure", "Two problems"
	for i := range 50 {
		created.Annotations = append(created.Annotations, Annotation{Path: "a.go", Line: i, Level: "warning", Message: fmt.Sprintf("m%d", i)})
	}
	if err := env.c.UpdateCheckRun(ctx, repoA, created); err != nil {
		t.Fatal(err)
	}
	body = decodeBody(t, findRequest(t, env.gh.requests(), "PATCH", "/repos/o/r/check-runs/99"))
	if body["status"] != "completed" || body["conclusion"] != "failure" || body["head_sha"] != nil {
		t.Errorf("update body = %v", body)
	}
	annotations := body["output"].(map[string]any)["annotations"].([]any)
	if len(annotations) != 50 {
		t.Fatalf("annotations = %d, want 50", len(annotations))
	}
	wantFirst := map[string]any{"path": "a.go", "start_line": 1.0, "end_line": 1.0, "annotation_level": "warning", "message": "m0"}
	if !reflect.DeepEqual(annotations[0], wantFirst) {
		t.Errorf("annotation 0 = %v, want line 0 sent as 1", annotations[0])
	}
	if second := annotations[12].(map[string]any); second["start_line"] != 12.0 || second["end_line"] != 12.0 {
		t.Errorf("annotation 12 = %v", second)
	}

	runs, err := env.c.CheckRuns(ctx, repoA, "abc123", "archfit")
	if err != nil {
		t.Fatal(err)
	}
	want := []CheckRun{{ID: 99, Name: "archfit", HeadSHA: "abc123", ExternalID: "ext-1", DetailsURL: "https://app.example/x", Status: "completed",
		Conclusion: "success", Title: "Gate", Summary: "All good", StartedAt: started, AnnotationCount: 50}}
	if !reflect.DeepEqual(runs, want) {
		t.Errorf("CheckRuns = %+v\nwant        %+v", runs, want)
	}
	list := findRequest(t, env.gh.requests(), "GET", "/repos/o/r/commits/abc123/check-runs")
	if list.Query.Get("check_name") != "archfit" || list.Query.Get("app_id") != "4242" {
		t.Errorf("list query = %v", list.Query)
	}
	for _, r := range env.gh.requests() {
		if want := map[bool]string{true: appJWT, false: inst1}[r.Path == "/app"]; r.Auth != want {
			t.Errorf("%s: Authorization = %q", r, r.Auth)
		}
	}
}

func TestCommentsContract(t *testing.T) {
	ctx := context.Background()
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/issues/7/comments", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, []map[string]any{
				{"id": 1, "body": "human", "user": map[string]any{"login": "alice"}},
				{"id": 2, "body": "other bot", "user": map[string]any{"login": "dependabot[bot]"}},
				{"id": 3, "body": "first", "user": map[string]any{"login": "archfit[bot]"}},
				{"id": 4, "body": "impostor", "user": map[string]any{"login": "archfit"}},
				{"id": 5, "body": "second", "user": map[string]any{"login": "archfit[bot]"}},
				{"id": 6, "body": "ghost", "user": nil},
			})
		})
		mux.HandleFunc("POST /repos/o/r/issues/7/comments", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]any{"id": 31, "body": "hello"})
		})
		mux.HandleFunc("PATCH /repos/o/r/issues/comments/31", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"id": 31})
		})
	})

	for range 2 {
		comments, err := env.c.AppComments(ctx, repoA, 7)
		if err != nil {
			t.Fatal(err)
		}
		if want := []Comment{{ID: 3, Body: "first"}, {ID: 5, Body: "second"}}; !reflect.DeepEqual(comments, want) {
			t.Errorf("AppComments = %+v, want %+v", comments, want)
		}
	}
	if n := env.gh.count("GET", "/app"); n != 1 {
		t.Errorf("GET /app called %d times, want 1", n)
	}

	created, err := env.c.CreateComment(ctx, repoA, 7, "hello")
	if err != nil || created != (Comment{ID: 31, Body: "hello"}) {
		t.Errorf("CreateComment = %+v, %v", created, err)
	}
	post := findRequest(t, env.gh.requests(), "POST", "/repos/o/r/issues/7/comments")
	if !reflect.DeepEqual(decodeBody(t, post), map[string]any{"body": "hello"}) || post.Auth != inst1 {
		t.Errorf("create request = %s %q", post.Body, post.Auth)
	}
	if err := env.c.UpdateComment(ctx, repoA, 31, "edited"); err != nil {
		t.Fatal(err)
	}
	patch := findRequest(t, env.gh.requests(), "PATCH", "/repos/o/r/issues/comments/31")
	if !reflect.DeepEqual(decodeBody(t, patch), map[string]any{"body": "edited"}) {
		t.Errorf("update body = %s", patch.Body)
	}

	t.Run("body limit is 65536 characters", func(t *testing.T) {
		env.gh.reset()
		if _, err := env.c.CreateComment(ctx, repoA, 7, strings.Repeat("é", 65536)); err != nil {
			t.Errorf("65536 characters: %v", err)
		}
		if _, err := env.c.CreateComment(ctx, repoA, 7, strings.Repeat("é", 65537)); err == nil {
			t.Error("65537 characters on create: want error")
		}
		if err := env.c.UpdateComment(ctx, repoA, 31, strings.Repeat("x", 65537)); err == nil {
			t.Error("65537 characters on update: want error")
		}
		if n := len(env.gh.requests()); n != 1 {
			t.Errorf("requests = %d, want only the valid one", n)
		}
	})
}

func TestActionsContract(t *testing.T) {
	ctx := context.Background()
	report := []byte(`{"schema":"archfit/report","ok":true}`)
	runJSON := map[string]any{
		"id": 55, "run_attempt": 2, "event": "pull_request", "head_sha": "abc123", "status": "completed",
		"conclusion": "success", "path": ".github/workflows/archfit.yaml",
		"head_repository": map[string]any{"full_name": "o/r"}, "pull_requests": []map[string]any{{"number": 7}, {"number": 9}},
	}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/actions/runs/55", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, runJSON)
		})
		mux.HandleFunc("POST /repos/o/r/actions/runs/55/rerun", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]any{})
		})
		mux.HandleFunc("POST /repos/o/r/actions/workflows/archfit.yaml/dispatches", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		mux.HandleFunc("GET /repos/o/r/actions/workflows/archfit.yaml/runs", func(w http.ResponseWriter, r *http.Request) {
			runs := []any{}
			if r.URL.Query().Get("branch") == "main" {
				runs = append(runs, runJSON)
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "workflow_runs": runs})
		})
		mux.HandleFunc("GET /repos/o/r/actions/runs/55/artifacts", func(w http.ResponseWriter, r *http.Request) {
			artifacts := []map[string]any{}
			if r.URL.Query().Get("name") == "archfit-report" {
				artifacts = []map[string]any{
					{"id": 1, "name": "archfit-report", "expired": true, "created_at": "2026-09-21T09:00:00Z"},
					{"id": 2, "name": "archfit-report", "expired": false, "created_at": "2026-09-21T07:00:00Z"},
					{"id": 3, "name": "archfit-report", "expired": false, "created_at": "2026-09-21T08:00:00Z"},
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
		})
		mux.HandleFunc("GET api.github.com/repos/o/r/actions/artifacts/3/zip", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "https://"+blobHost+"/signed/3?sig=abc")
			w.WriteHeader(http.StatusFound)
		})
		mux.HandleFunc("GET "+blobHost+"/signed/3", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("sig") != "abc" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write(zipOf(t, zipEntry{"report.json", report}))
		})
	})

	run, err := env.c.Run(ctx, repoA, 55)
	if err != nil {
		t.Fatal(err)
	}
	want := WorkflowRun{ID: 55, Attempt: 2, Event: "pull_request", HeadSHA: "abc123", HeadRepo: "o/r",
		WorkflowPath: ".github/workflows/archfit.yaml", Status: "completed", Conclusion: "success", PullRequests: []int{7, 9}}
	if !reflect.DeepEqual(run, want) {
		t.Errorf("Run = %+v\nwant  %+v", run, want)
	}
	if err := env.c.Rerun(ctx, repoA, 55); err != nil {
		t.Errorf("Rerun (201): %v", err)
	}
	if err := env.c.Dispatch(ctx, repoA, ".github/workflows/archfit.yaml", "main", map[string]string{"mode": "discover"}); err != nil {
		t.Errorf("Dispatch (204): %v", err)
	}
	dispatch := findRequest(t, env.gh.requests(), "POST", "/repos/o/r/actions/workflows/archfit.yaml/dispatches")
	if want := map[string]any{"ref": "main", "inputs": map[string]any{"mode": "discover"}}; !reflect.DeepEqual(decodeBody(t, dispatch), want) {
		t.Errorf("dispatch body = %s", dispatch.Body)
	}

	latest, err := env.c.LatestRun(ctx, repoA, ".github/workflows/archfit.yaml", RunFilter{Branch: "main"})
	if err != nil || !reflect.DeepEqual(latest, want) {
		t.Errorf("LatestRun = %+v, %v", latest, err)
	}
	_, err = env.c.LatestRun(ctx, repoA, ".github/workflows/archfit.yaml", RunFilter{Branch: "quiet"})
	wantErrIs(t, "no run", err, ErrNotFound)

	env.gh.reset()
	got, err := env.c.Artifact(ctx, repoA, 55, "archfit-report", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, report) {
		t.Errorf("Artifact = %q", got)
	}
	reqs := env.gh.requests()
	if len(reqs) != 3 {
		t.Fatalf("artifact requests = %v", reqs)
	}
	if reqs[1].Path != "/repos/o/r/actions/artifacts/3/zip" || reqs[1].Auth != inst1 {
		t.Errorf("zip request = %s %q, want the newest unexpired artifact", reqs[1], reqs[1].Auth)
	}
	if blob := reqs[2]; blob.Host != blobHost || blob.Auth != "" {
		t.Errorf("redirected request: host %q, Authorization %q; want no credential on another host", blob.Host, blob.Auth)
	}

	// Artifact is ArtifactID then ArtifactByID: the same three requests.
	env.gh.reset()
	id, err := env.c.ArtifactID(ctx, repoA, 55, "archfit-report")
	if err != nil || id != 3 {
		t.Fatalf("ArtifactID = %d, %v; want the newest unexpired, 3", id, err)
	}
	if got, err := env.c.ArtifactByID(ctx, repoA, id, 1<<20); err != nil || !bytes.Equal(got, report) {
		t.Errorf("ArtifactByID = %q, %v", got, err)
	}
	if split := env.gh.requests(); !slices.EqualFunc(split, reqs, func(a, b recorded) bool { return a.String() == b.String() }) {
		t.Errorf("split requests = %v, want %v", split, reqs)
	}
	if _, err := env.c.ArtifactByID(ctx, repoA, id, 0); err == nil {
		t.Error("ArtifactByID maxBytes 0: want error")
	}

	_, err = env.c.Artifact(ctx, repoA, 55, "archfit-report", int64(len(report))-1)
	wantErrIs(t, "artifact over maxBytes", err, ErrArtifactInvalid)
	_, err = env.c.Artifact(ctx, repoA, 55, "absent", 1<<20)
	wantErrIs(t, "no artifact", err, ErrNotFound)
	env.gh.reset()
	for _, maxBytes := range []int64{0, -1} {
		if _, err := env.c.Artifact(ctx, repoA, 55, "archfit-report", maxBytes); err == nil {
			t.Errorf("maxBytes %d: want error", maxBytes)
		}
	}
	if n := len(env.gh.requests()); n != 0 {
		t.Errorf("maxBytes guard sent %d requests", n)
	}
}

func TestLatestRunSendsOnlySetFilters(t *testing.T) {
	ctx := context.Background()
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/actions/workflows/archfit.yaml/runs", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 1, "workflow_runs": []any{map[string]any{"id": 55}}})
		})
	})
	tests := []struct {
		name   string
		filter RunFilter
		want   url.Values
	}{
		{"none", RunFilter{}, url.Values{"per_page": {"1"}}},
		{"branch", RunFilter{Branch: "main"}, url.Values{"per_page": {"1"}, "branch": {"main"}}},
		{"all", RunFilter{Branch: "main", Event: "push", Status: "success"},
			url.Values{"per_page": {"1"}, "branch": {"main"}, "event": {"push"}, "status": {"success"}}},
		{"event and status", RunFilter{Event: "push", Status: "completed"},
			url.Values{"per_page": {"1"}, "event": {"push"}, "status": {"completed"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env.gh.reset()
			if _, err := env.c.LatestRun(ctx, repoA, ".github/workflows/archfit.yaml", tt.filter); err != nil {
				t.Fatal(err)
			}
			got := findRequest(t, env.gh.requests(), "GET", "/repos/o/r/actions/workflows/archfit.yaml/runs").Query
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("query = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangesContract(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*testEnv, *fakeRepo) {
		repo := newFakeRepo(t)
		return muxEnv(t, repo.routes), repo
	}
	files := []FileChange{{Path: ".archfit.yaml", Content: []byte("version: 1\n")}, {Path: "docs/policy.md", Content: []byte{0xff, 0x00, 'x'}}}

	t.Run("EnsureBranch", func(t *testing.T) {
		env, repo := setup(t)
		if err := env.c.EnsureBranch(ctx, repoA, "archfit/setup", "main"); err != nil {
			t.Fatal(err)
		}
		if repo.refs["archfit/setup"] != repo.refs["main"] {
			t.Error("new branch does not point at fromRef")
		}
		if err := env.c.EnsureBranch(ctx, repoA, "archfit/setup", "main"); err != nil {
			t.Fatal(err)
		}
		if n := env.gh.count("POST", "/repos/o/r/git/refs"); n != 1 {
			t.Errorf("refs created = %d, want 1", n)
		}

		repo.raceRef = true
		if err := env.c.EnsureBranch(ctx, repoA, "raced", repo.refs["main"]); err != nil {
			t.Errorf("lost race: %v", err)
		}
		err := env.c.EnsureBranch(ctx, repoA, "orphan", "no-such-branch")
		wantErrIs(t, "missing fromRef", err, ErrNotFound)
	})

	t.Run("Commit", func(t *testing.T) {
		env, repo := setup(t)
		before := repo.commitCount()
		sha, err := env.c.Commit(ctx, repoA, "main", "add archfit", files)
		if err != nil {
			t.Fatal(err)
		}
		if repo.refs["main"] != sha || repo.commitCount() != before+1 {
			t.Errorf("head = %q, commit = %q, commits %d → %d", repo.refs["main"], sha, before, repo.commitCount())
		}
		if !bytes.Equal(repo.fileAt("main", ".archfit.yaml"), files[0].Content) ||
			!bytes.Equal(repo.fileAt("main", "docs/policy.md"), files[1].Content) ||
			string(repo.fileAt("main", "README.md")) != "readme" {
			t.Error("commit does not hold both files on top of the base tree")
		}
		if c := repo.commits[sha]; c.message != "add archfit" || len(c.parents) != 1 {
			t.Errorf("commit = %+v", c)
		}

		env.gh.reset()
		again, err := env.c.Commit(ctx, repoA, "main", "add archfit", files)
		if err != nil || again != sha {
			t.Errorf("identical content: %q, %v; want the head %q", again, err, sha)
		}
		if env.gh.count("POST", "/repos/o/r/git/commits") != 0 || repo.commitCount() != before+1 {
			t.Error("identical content made a commit")
		}

		repo.moveOnCommit = true
		_, err = env.c.Commit(ctx, repoA, "main", "late", []FileChange{{Path: "x", Content: []byte("y")}})
		wantPlainErr(t, "non-fast-forward", err)

		env.gh.reset()
		if _, err := env.c.Commit(ctx, repoA, "main", "empty", nil); err == nil {
			t.Error("no files: want error")
		}
		if len(env.gh.requests()) != 0 {
			t.Error("no files: a request was sent")
		}
	})

	t.Run("EnsurePull", func(t *testing.T) {
		env, repo := setup(t)
		repo.refs["archfit/setup"], repo.refs["raced"] = repo.refs["main"], repo.refs["main"]
		np := NewPull{Title: "Adopt archfit", Body: "details", Head: "archfit/setup", Base: "main", Draft: true}
		created, err := env.c.EnsurePull(ctx, repoA, np)
		if err != nil {
			t.Fatal(err)
		}
		if created.Number != 1 || !created.Draft || created.HeadRef != "archfit/setup" || created.MergeBaseSHA != "" {
			t.Errorf("created = %+v", created)
		}
		post := decodeBody(t, findRequest(t, env.gh.requests(), "POST", "/repos/o/r/pulls"))
		want := map[string]any{"title": "Adopt archfit", "body": "details", "head": "archfit/setup", "base": "main", "draft": true}
		if !reflect.DeepEqual(post, want) {
			t.Errorf("create body = %v", post)
		}
		existing, err := env.c.EnsurePull(ctx, repoA, np)
		if err != nil || existing.Number != 1 {
			t.Errorf("existing = %+v, %v", existing, err)
		}
		repo.racePull = true
		np.Head = "raced"
		raced, err := env.c.EnsurePull(ctx, repoA, np)
		if err != nil || raced.Number != 2 {
			t.Errorf("lost race = %+v, %v", raced, err)
		}
		if len(repo.pulls) != 2 {
			t.Errorf("pulls = %d, want one per head", len(repo.pulls))
		}
	})

	t.Run("UpdatePullBody and MarkReady", func(t *testing.T) {
		env, repo := setup(t)
		repo.refs["a"], repo.refs["b"] = repo.refs["main"], repo.refs["main"]
		repo.pulls = append(repo.pulls, repo.pullObject(1, "a", "main", true), repo.pullObject(2, "b", "main", false))

		if err := env.c.UpdatePullBody(ctx, repoA, 1, "new body"); err != nil {
			t.Fatal(err)
		}
		if repo.pulls[0]["body"] != "new body" {
			t.Errorf("body = %v", repo.pulls[0]["body"])
		}
		if err := env.c.MarkReady(ctx, repoA, 1); err != nil {
			t.Fatal(err)
		}
		if repo.graphqlCalls != 1 || repo.pulls[0]["draft"] != false {
			t.Errorf("mutations = %d, draft = %v", repo.graphqlCalls, repo.pulls[0]["draft"])
		}
		if q, _ := repo.graphqlBody["query"].(string); !strings.Contains(q, "markPullRequestReadyForReview") ||
			!reflect.DeepEqual(repo.graphqlBody["variables"], map[string]any{"id": "PR_node_1"}) {
			t.Errorf("graphql body = %v", repo.graphqlBody)
		}
		if err := env.c.MarkReady(ctx, repoA, 2); err != nil {
			t.Fatal(err)
		}
		if err := env.c.MarkReady(ctx, repoA, 1); err != nil {
			t.Fatal(err)
		}
		if repo.graphqlCalls != 1 {
			t.Errorf("mutations = %d, want none for a ready pull", repo.graphqlCalls)
		}
		if gql := findRequest(t, env.gh.requests(), "POST", "/graphql"); gql.Auth != inst1 {
			t.Errorf("graphql Authorization = %q", gql.Auth)
		}
	})
}

func TestUsersContract(t *testing.T) {
	ctx := context.Background()
	const token = "user-oauth-token"
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/{name}", func(w http.ResponseWriter, r *http.Request) {
			perms := map[string]map[string]bool{
				"admin": {"admin": true, "push": true, "pull": true},
				"push":  {"admin": false, "push": true, "pull": true},
				"pull":  {"admin": false, "push": false, "pull": true},
				"bare":  {},
			}
			switch name := r.PathValue("name"); name {
			case "gone":
				w.WriteHeader(http.StatusNotFound)
			case "unauth":
				w.WriteHeader(http.StatusUnauthorized)
			default:
				writeJSON(w, http.StatusOK, map[string]any{"full_name": "o/" + name, "permissions": perms[name]})
			}
		})
		mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"installations": []map[string]any{{"id": 1}, {"id": 2}}})
		})
		mux.HandleFunc("GET /user/installations/{id}/repositories", func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			writeJSON(w, http.StatusOK, map[string]any{"repositories": []map[string]any{
				{"id": 10, "full_name": "org" + id + "/app", "private": true, "default_branch": "main"},
				{"id": 11, "full_name": "org" + id + "/lib", "default_branch": "dev"},
			}})
		})
	})

	for name, want := range map[string]string{"admin": "admin", "push": "write", "pull": "read", "bare": "none", "gone": "none"} {
		got, err := env.c.Permission(ctx, token, "o/"+name)
		if err != nil || got != want {
			t.Errorf("Permission(o/%s) = %q, %v; want %q", name, got, err, want)
		}
	}
	got, err := env.c.Permission(ctx, token, "o/unauth")
	if !errors.Is(err, ErrUserTokenRejected) {
		t.Errorf("Permission on 401: err = %v, want ErrUserTokenRejected", err)
	}
	if got != "" {
		t.Errorf("Permission on 401 = %q", got)
	}

	repos, err := env.c.UserRepos(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{
		{RepoRef: RepoRef{1, "org1/app"}, ID: 10, Private: true, DefaultBranch: "main"},
		{RepoRef: RepoRef{1, "org1/lib"}, ID: 11, DefaultBranch: "dev"},
		{RepoRef: RepoRef{2, "org2/app"}, ID: 10, Private: true, DefaultBranch: "main"},
		{RepoRef: RepoRef{2, "org2/lib"}, ID: 11, DefaultBranch: "dev"},
	}
	if !reflect.DeepEqual(repos, want) {
		t.Errorf("UserRepos = %+v\nwant        %+v", repos, want)
	}

	for _, r := range env.gh.requests() {
		if r.Auth != bearer(token) {
			t.Errorf("%s: Authorization = %q, want only the user token", r, r.Auth)
		}
	}
	if env.ts.jwtCalls != 0 || len(env.ts.minted) != 0 {
		t.Errorf("App credentials were minted for Users calls: jwt %d, installation %v", env.ts.jwtCalls, env.ts.minted)
	}
}

func TestCheckAppProvesTheKeyOnEveryCall(t *testing.T) {
	ctx := context.Background()
	status := http.StatusOK
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			reply{status: status, body: `{"message":"Bad credentials"}`}.write(w)
			return
		}
		appHandler(w, r)
	}))
	for range 2 {
		if err := env.c.CheckApp(ctx); err != nil {
			t.Fatalf("CheckApp = %v", err)
		}
	}
	if n := env.gh.count("GET", "/app"); n != 2 {
		t.Errorf("GET /app called %d times, want 2: the identity cache must not answer a check", n)
	}
	for _, r := range env.gh.requests() {
		if r.Auth != appJWT {
			t.Errorf("%s: Authorization = %q, want the App JWT", r, r.Auth)
		}
	}

	status = http.StatusUnauthorized
	err := env.c.CheckApp(ctx)
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Errorf("CheckApp after a rejected JWT = %v, want an error with the status", err)
	}
	if err != nil && strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("error %q quotes the response body", err)
	}
}
