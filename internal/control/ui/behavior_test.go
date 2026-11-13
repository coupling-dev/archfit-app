package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestRevokedUserLosesEveryPage(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	for _, path := range []string{"", "/graph"} {
		wantStatus(t, c.get("/r/"+repoName+path), http.StatusOK)
	}
	wantContains(t, c.get("/api/repos").Body.String(), repoName)

	e.grant("alice", repoName, "none")
	e.now = e.now.Add(61 * time.Second)

	allNotFound(t, e, c)
	if strings.Contains(c.get("/api/repos").Body.String(), repoName) {
		t.Error("the overview still lists the repository")
	}
}

func TestRemovedRepositoryDisappears(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	wantStatus(t, c.get("/r/"+repoName), http.StatusOK)

	e.gh.mu.Lock()
	delete(e.gh.repos, repoName) // removed from the installation; alice is still its administrator
	e.gh.mu.Unlock()
	e.now = e.now.Add(61 * time.Second)

	allNotFound(t, e, c)
}

// Incident 2026-10-18: a session cookie outlived its 8 h GitHub user token, GitHub answered 401, and every page
// was a 502 until the cookie expired. A rejected token now ends the session and asks for a new sign-in.
func TestExpiredGitHubTokenSignsOutInsteadOfBadGateway(t *testing.T) {
	rejected := fmt.Errorf("github: GET /repos/{r}: %w", api.ErrUserTokenRejected)
	sessionDeleted := func(res *httptest.ResponseRecorder) bool {
		for _, ck := range res.Result().Cookies() {
			if ck.Name == "archfit_session" {
				return ck.MaxAge < 0 && ck.Value == ""
			}
		}
		return false
	}
	tests := []struct {
		name     string
		inject   func(*env)
		send     func(*client) *httptest.ResponseRecorder
		want     int
		location string
	}{
		{"pull request", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/api/repos/" + repoName + "/pulls/4") },
			http.StatusUnauthorized, ""},
		{"overview", func(e *env) { e.gh.fail["UserRepos"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/api/repos") },
			http.StatusUnauthorized, ""},
		{"repository overview", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/api/repos/" + repoName + "/overview") },
			http.StatusUnauthorized, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			e := newEnv(t)
			c := e.as("alice", permRead)
			tt.inject(e)

			res := tt.send(c)

			wantStatus(t, res, tt.want)
			if got := res.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
			if !sessionDeleted(res) {
				t.Errorf("Set-Cookie = %q, want the session cookie deleted", res.Header().Values("Set-Cookie"))
			}
			if !strings.Contains(logs.String(), `"outcome":"signed_out"`) {
				t.Errorf("ui request log has no signed_out outcome:\n%s", logs.String())
			}

			// The browser dropped the cookie: the next request is a plain signed-out one.
			delete(e.gh.fail, "Permission")
			delete(e.gh.fail, "UserRepos")
			if next := c.get("/r/" + repoName); next.Code != http.StatusSeeOther {
				t.Errorf("request after the sign-out = %d, want 303 to sign in", next.Code)
			}
		})
	}

	t.Run("other GitHub failures stay a bad gateway and keep the session", func(t *testing.T) {
		for name, key := range map[string]string{"repository overview": "Permission", "overview": "UserRepos"} {
			e := newEnv(t)
			c := e.as("alice", permRead)
			e.gh.fail[key] = errors.New("boom")
			path := "/api/repos"
			if key == "Permission" {
				path = "/api/repos/" + repoName + "/overview"
			}

			res := c.get(path)

			wantStatus(t, res, http.StatusBadGateway)
			if len(res.Result().Cookies()) != 0 {
				t.Errorf("%s: a 502 changed cookies: %q", name, res.Header().Values("Set-Cookie"))
			}
		}
	})
}

// The request line carries the GitHub call count and time (api.WithCallStats counts them; api's tests check the
// counting): one line per request, numbers only, never a URL.
func TestRequestLineCountsGitHubCalls(t *testing.T) {
	logs := captureLogs(t)
	e := newEnv(t)
	c := e.as("alice", permRead)
	wantStatus(t, c.get("/api/repos"), http.StatusOK)

	var lines []map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil && m["msg"] == "ui request" && m["route"] == "GET /api/repos" {
			lines = append(lines, m)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("ui request lines = %v, want one", lines)
	}
	for _, key := range []string{"gh_calls", "gh_ms"} {
		if _, ok := lines[0][key].(float64); !ok {
			t.Errorf("%s = %v, want a number", key, lines[0][key])
		}
	}
	if strings.Contains(logs.String(), "https://") {
		t.Errorf("the request line names a URL: %s", logs.String())
	}
}

func allNotFound(t *testing.T, e *env, c *client) {
	t.Helper()
	for _, rt := range repoRoutes(repoName) {
		if !rt.authorized() {
			continue
		}
		if got := c.send(rt); got != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, got)
		}
	}
	e.noWrites(t)
}
