package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is an in-memory GitHub REST API. Unknown routes answer 404 and are logged,
// so a failing composition test names the endpoint it lacks.
type fakeGitHub struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	down          bool
	installs      []int64
	repos         map[int64][]ghRepo // by installation
	reposStatus   map[int64]int      // forced status of the installation's repository list
	perms         map[string]string  // "login owner/name" -> permission
	states        map[string]*ghState
	wrote         chan struct{} // one signal per answered write
	requests      []string      // "METHOD /path"
	writes        []string      // non-GET requests
	installTokens []int64       // installations whose token was used
}

type ghRepo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{t: t, repos: map[int64][]ghRepo{}, reposStatus: map[int64]int{}, perms: map[string]string{}, states: map[string]*ghState{}, wrote: make(chan struct{}, 4096)}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) set(f func(*fakeGitHub)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f(g)
}

// install registers an installation with its repositories ("owner/name", private?).
func (g *fakeGitHub) install(id int64, repos ...ghRepo) {
	g.set(func(g *fakeGitHub) {
		g.installs = append(g.installs, id)
		g.repos[id] = repos
	})
}

func (g *fakeGitHub) uninstall(id int64) {
	g.set(func(g *fakeGitHub) {
		kept := g.installs[:0]
		for _, in := range g.installs {
			if in != id {
				kept = append(kept, in)
			}
		}
		g.installs = kept
		delete(g.repos, id)
	})
}

func (g *fakeGitHub) writeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.writes)
}

func (g *fakeGitHub) requestCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.requests)
}

// writesMatching returns the write requests whose path contains any of the fragments.
func (g *fakeGitHub) writesMatching(fragments ...string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, w := range g.writes {
		for _, f := range fragments {
			if strings.Contains(w, f) {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

func (g *fakeGitHub) usedTokenOf(id int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Contains(g.installTokens, id)
}

// awaitManagedJob blocks until the managed discovery goroutine has committed path to branch and
// closed its "running" marker in the pull request body: its last write.
func (g *fakeGitHub) awaitManagedJob(fullName, branch, path string) string {
	g.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		content, ok := g.file(fullName, branch, path)
		if pulls := g.pulls(fullName); ok && len(pulls) > 0 && !strings.Contains(pulls[0].Body, "state=running") {
			return content
		}
		select {
		case <-g.wrote:
		case <-timeout:
			g.t.Fatalf("fake GitHub: the managed job never finished %s on %s", path, branch)
		}
	}
}

func ghr(id int64, fullName string, private bool) ghRepo {
	return ghRepo{ID: id, FullName: fullName, Private: private, DefaultBranch: "main"}
}

// installationOf maps the bearer "tok-<id>" back to the installation.
func installationOf(r *http.Request) int64 {
	var id int64
	_, _ = fmt.Sscanf(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "tok-%d", &id)
	if id == 0 {
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.Header.Get("Authorization"), "token "), "tok-%d", &id)
	}
	return id
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	line := r.Method + " " + r.URL.Path
	g.requests = append(g.requests, line)
	if r.Method != http.MethodGet {
		g.writes = append(g.writes, line)
		defer func() {
			select {
			case g.wrote <- struct{}{}:
			default:
			}
		}()
	}
	if id := installationOf(r); id != 0 {
		g.installTokens = append(g.installTokens, id)
	}
	if g.down {
		http.Error(w, `{"message":"down"}`, http.StatusInternalServerError)
		return
	}
	id := installationOf(r)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case line == "GET /app/installations":
		out := []map[string]any{}
		for _, in := range g.installs {
			// The account is the owner of the installation's repositories, as on GitHub; "org<id>" without one.
			account := fmt.Sprintf("org%d", in)
			if repos := g.repos[in]; len(repos) > 0 {
				account, _, _ = strings.Cut(repos[0].FullName, "/")
			}
			out = append(out, map[string]any{"id": in, "account": map[string]any{"login": account}})
		}
		writeJSON(w, http.StatusOK, out)
	case line == "GET /app":
		writeJSON(w, http.StatusOK, map[string]any{"id": 1, "slug": "archfit", "name": "Archfit", "html_url": "https://github.test/apps/archfit"})
	case line == "GET /installation/repositories":
		if st := g.reposStatus[id]; st != 0 {
			http.Error(w, `{"message":"forced"}`, st)
			return
		}
		repos, ok := g.repos[id]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(repos), "repositories": repos})
	case line == "GET /user/installations":
		login := userOf(r)
		out := []map[string]any{}
		for _, in := range g.installs {
			for _, rp := range g.repos[in] {
				if g.perms[login+" "+rp.FullName] != "" {
					out = append(out, map[string]any{"id": in, "account": map[string]any{"login": fmt.Sprintf("org%d", in)}})
					break
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(out), "installations": out})
	case r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "user" && parts[1] == "installations" && parts[3] == "repositories":
		login := userOf(r)
		var in int64
		_, _ = fmt.Sscanf(parts[2], "%d", &in)
		out := []map[string]any{}
		for _, rp := range g.repos[in] {
			if perm := g.perms[login+" "+rp.FullName]; perm != "" {
				out = append(out, userRepo(rp, perm))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(out), "repositories": out})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "repos" && userOf(r) != "":
		rp, _, ok := g.findRepo(parts[1] + "/" + parts[2])
		perm := g.perms[userOf(r)+" "+parts[1]+"/"+parts[2]]
		if !ok || perm == "" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, userRepo(rp, perm))
	case id != 0 && g.serveRepo(w, r, parts), userOf(r) == "" && parts[len(parts)-1] == "installation" && g.serveRepo(w, r, parts):
	default:
		g.t.Logf("fake GitHub: unhandled %s?%s", line, r.URL.RawQuery)
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}
}

func userOf(r *http.Request) string {
	for _, prefix := range []string{"Bearer user-", "token user-"} {
		if login, ok := strings.CutPrefix(r.Header.Get("Authorization"), prefix); ok {
			return login
		}
	}
	return ""
}

func userRepo(rp ghRepo, perm string) map[string]any {
	return map[string]any{
		"id": rp.ID, "full_name": rp.FullName, "private": rp.Private, "default_branch": rp.DefaultBranch,
		"permissions": map[string]bool{
			"admin": perm == "admin", "push": perm == "admin" || perm == "write", "pull": true,
		},
	}
}

func (g *fakeGitHub) findRepo(fullName string) (ghRepo, int64, bool) {
	for in, repos := range g.repos {
		for _, rp := range repos {
			if rp.FullName == fullName {
				return rp, in, true
			}
		}
	}
	return ghRepo{}, 0, false
}

// allow gives login a permission ("admin" | "write" | "read") on a repository.
func (g *fakeGitHub) allow(login, fullName, perm string) {
	g.set(func(g *fakeGitHub) { g.perms[login+" "+fullName] = perm })
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
