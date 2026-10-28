package api

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestIssuesContract(t *testing.T) {
	ctx := context.Background()
	app := map[string]any{"login": "archfit[bot]"}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/issues", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, []map[string]any{
				{"number": 1, "body": "human", "user": map[string]any{"login": "alice"}},
				{"number": 2, "body": "other bot", "user": map[string]any{"login": "dependabot[bot]"}},
				{"number": 3, "body": "first", "user": app},
				{"number": 4, "body": "the App's own pull request", "user": app, "pull_request": map[string]any{"url": "x"}},
				{"number": 5, "body": "impostor", "user": map[string]any{"login": "archfit"}},
				{"number": 6, "body": nil, "user": app},
				{"number": 7, "body": "ghost", "user": nil},
			})
		})
		mux.HandleFunc("POST /repos/o/r/issues", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]any{"number": 41, "body": "ignored"})
		})
		mux.HandleFunc("PATCH /repos/o/r/issues/{n}", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"number": 41})
		})
		mux.HandleFunc("GET /repos/o/r/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
			issue := map[string]any{"number": 41, "state": "open", "user": app}
			if r.PathValue("n") == "4" {
				issue["pull_request"] = map[string]any{"url": "x"}
			}
			writeJSON(w, http.StatusOK, issue)
		})
	})

	issues, err := env.c.AppIssues(ctx, repoA)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Issue{{Number: 3, Body: "first"}, {Number: 6}}; !reflect.DeepEqual(issues, want) {
		t.Errorf("AppIssues = %+v, want %+v: the App's own issues, no pull request", issues, want)
	}
	list := findRequest(t, env.gh.requests(), "GET", "/repos/o/r/issues")
	if got := list.Query.Encode(); got != "per_page=100&state=open" || list.Auth != inst1 {
		t.Errorf("list request = %s %q, want per_page=100&state=open and no creator filter", got, list.Auth)
	}

	tests := []struct {
		name     string
		issue    NewIssue
		wantBody map[string]any
	}{
		{"assigned", NewIssue{Title: "t", Body: "b", Assignee: "alice"}, map[string]any{"title": "t", "body": "b", "assignees": []any{"alice"}}},
		{"unassigned", NewIssue{Title: "t", Body: "b"}, map[string]any{"title": "t", "body": "b"}},
	}
	for _, tt := range tests {
		t.Run("create "+tt.name, func(t *testing.T) {
			env.gh.reset()
			created, err := env.c.CreateIssue(ctx, repoA, tt.issue)
			if err != nil || created != (Issue{Number: 41, Body: "b"}) {
				t.Errorf("CreateIssue = %+v, %v", created, err)
			}
			post := findRequest(t, env.gh.requests(), "POST", "/repos/o/r/issues")
			if got := decodeBody(t, post); !reflect.DeepEqual(got, tt.wantBody) || post.Auth != inst1 {
				t.Errorf("create request = %v %q, want %v", got, post.Auth, tt.wantBody)
			}
		})
	}

	env.gh.reset()
	if err := env.c.UpdateIssueBody(ctx, repoA, 41, "edited"); err != nil {
		t.Fatal(err)
	}
	patch := findRequest(t, env.gh.requests(), "PATCH", "/repos/o/r/issues/41")
	if got := decodeBody(t, patch); !reflect.DeepEqual(got, map[string]any{"body": "edited"}) {
		t.Errorf("update body = %s", patch.Body)
	}

	t.Run("close an issue", func(t *testing.T) {
		env.gh.reset()
		if err := env.c.CloseIssue(ctx, repoA, 41); err != nil {
			t.Fatal(err)
		}
		patch := findRequest(t, env.gh.requests(), "PATCH", "/repos/o/r/issues/41")
		if got := decodeBody(t, patch); !reflect.DeepEqual(got, map[string]any{"state": "closed"}) {
			t.Errorf("close body = %s", patch.Body)
		}
	})

	t.Run("a pull request is never closed", func(t *testing.T) {
		env.gh.reset()
		err := env.c.CloseIssue(ctx, repoA, 4)
		wantPlainErr(t, "CloseIssue of a pull request", err)
		if n := env.gh.count("PATCH", "/repos/o/r/issues/4"); n != 0 {
			t.Errorf("PATCH sent %d times, want none", n)
		}
	})

	t.Run("body limit is 65536 characters", func(t *testing.T) {
		env.gh.reset()
		if _, err := env.c.CreateIssue(ctx, repoA, NewIssue{Title: "t", Body: strings.Repeat("é", 65536)}); err != nil {
			t.Errorf("65536 characters: %v", err)
		}
		if _, err := env.c.CreateIssue(ctx, repoA, NewIssue{Title: "t", Body: strings.Repeat("é", 65537)}); err == nil {
			t.Error("65537 characters on create: want error")
		}
		if err := env.c.UpdateIssueBody(ctx, repoA, 41, strings.Repeat("x", 65537)); err == nil {
			t.Error("65537 characters on update: want error")
		}
		if n := len(env.gh.requests()); n != 1 {
			t.Errorf("requests = %d, want only the valid one", n)
		}
	})
}

// The repair issue is a notification: whatever GitHub answers, the caller gets an error it can log, never data.
func TestIssuesFailuresAreErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header map[string]string
		want   error // nil: a plain error
	}{
		{"no Issues permission", http.StatusForbidden, nil, nil},
		{"rate limited", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "4000000000"}, ErrRateLimited},
		{"gone", http.StatusNotFound, nil, ErrNotFound},
		{"assignee rejected", http.StatusUnprocessableEntity, nil, ErrUnprocessable},
		{"server error", http.StatusBadGateway, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := muxEnv(t, func(mux *http.ServeMux) {
				mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, _ *http.Request) {
					reply{status: tt.status, header: tt.header, body: `{"message":"no"}`}.write(w)
				})
			})
			_, err := env.c.CreateIssue(context.Background(), repoA, NewIssue{Title: "t", Body: "b", Assignee: "alice"})
			if tt.want != nil {
				wantErrIs(t, "CreateIssue", err, tt.want)
			} else {
				wantPlainErr(t, "CreateIssue", err)
			}
			if tt.status != http.StatusNotFound && tt.want == nil && Status(err) != tt.status {
				t.Errorf("Status = %d, want %d", Status(err), tt.status)
			}
		})
	}
}
