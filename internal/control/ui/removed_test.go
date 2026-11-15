package ui

import (
	"net/http"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
)

func TestRemovedPagesAndAPIsReturn404WithoutDataAccess(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		e := newEnv(t)
		c := e.anonymous()
		if signedIn {
			c = e.as("alice", permAdmin)
		}
		sessions, err := session.New(session.Config{CookieKey: cookieKey, BaseURL: baseURL}, e.oauth, e.gh, e.gh, func() time.Time { return e.now })
		if err != nil {
			t.Fatal(err)
		}
		// No data dependencies are wired: reaching a removed handler fails instead of silently reading a store.
		e.handler = newHandler(Deps{}, github{}, sessions, testWeb)
		e.gh.calls = nil
		paths := []route{
			{method: http.MethodGet, path: "/r/" + repoName + "/graph"},
			{method: http.MethodGet, path: "/r/" + repoName + "/pull/4/graph"},
			{method: http.MethodGet, path: "/r/" + repoName + "/decisions"},
			{method: http.MethodGet, path: "/r/" + repoName + "/data-sharing"},
			{method: http.MethodGet, path: "/r/" + repoName + "/consent"},
		}
		for _, suffix := range []string{"overview", "findings", "graph", "pulls/4/graph", "data-sharing", "decisions", "status"} {
			paths = append(paths, route{method: http.MethodGet, path: "/api/repos/" + repoName + "/" + suffix})
		}
		for _, suffix := range []string{"onboarding", "discover", "data-sharing"} {
			paths = append(paths, route{method: http.MethodPost, path: "/api/repos/" + repoName + "/" + suffix, body: map[string]string{"kind": "source_processing", "action": "allow"}})
		}
		for _, rt := range paths {
			res := c.do(requestOf(rt))
			if rt.method == http.MethodPost {
				res = c.postJSON(rt.path, rt.body)
			}
			wantStatus(t, res, http.StatusNotFound)
			if got := res.Header().Get("Location"); got != "" {
				t.Errorf("%s redirects to %s", rt.path, got)
			}
		}
		if len(e.gh.calls) != 0 {
			t.Errorf("removed routes called GitHub: %+v", e.gh.calls)
		}
		if len(e.acct.installations) != 0 {
			t.Errorf("removed routes read accounts: %v", e.acct.installations)
		}
	}
}
