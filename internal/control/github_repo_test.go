package control

import (
	"crypto/sha1" //nolint:gosec // fake git object IDs
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ghState is the git and pull-request state of one fake repository.
type ghState struct {
	branches map[string]string            // branch -> commit SHA
	commits  map[string]map[string][]byte // commit SHA -> snapshot path -> content
	blobs    map[string][]byte
	trees    map[string]map[string][]byte
	pulls    []*ghPull
	dispatch []string // dispatched workflow files
}

type ghPull struct {
	Number int
	Title  string
	Body   string
	Head   string
	Base   string
	Draft  bool
	State  string
	Merged bool
}

func sha(parts ...any) string {
	sum := sha1.Sum([]byte(fmt.Sprint(parts...))) //nolint:gosec // fake git object IDs
	return hex.EncodeToString(sum[:])
}

// state returns the repository's state, created on first use with an empty default branch.
func (g *fakeGitHub) state(fullName string) *ghState {
	st, ok := g.states[fullName]
	if !ok {
		root := sha("root", fullName)
		st = &ghState{
			branches: map[string]string{"main": root},
			commits:  map[string]map[string][]byte{root: {}},
			blobs:    map[string][]byte{}, trees: map[string]map[string][]byte{},
		}
		g.states[fullName] = st
	}
	return st
}

// merge merges the pull request of branch into its base.
func (g *fakeGitHub) merge(fullName, branch string) {
	g.set(func(g *fakeGitHub) {
		st := g.state(fullName)
		for _, p := range st.pulls {
			if p.Head == branch && p.State == "open" {
				snap := clone(st.commits[st.branches[p.Base]])
				for path, c := range st.commits[st.branches[branch]] {
					snap[path] = c
				}
				st.commit(p.Base, snap)
				p.State, p.Merged, p.Draft = "closed", true, false
				return
			}
		}
		g.t.Errorf("fake GitHub: no open pull request for %s", branch)
	})
}

func (g *fakeGitHub) pulls(fullName string) []ghPull {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ghPull
	for _, p := range g.state(fullName).pulls {
		out = append(out, *p)
	}
	return out
}

func (g *fakeGitHub) file(fullName, branch, path string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.state(fullName)
	c, ok := st.commits[st.branches[branch]][path]
	return string(c), ok
}

func clone(m map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (st *ghState) commit(branch string, snap map[string][]byte) string {
	id := sha("commit", branch, len(st.commits), st.branches[branch])
	st.commits[id] = snap
	st.branches[branch] = id
	return id
}

func (st *ghState) pullJSON(fullName string, p *ghPull) map[string]any {
	owner, _, _ := strings.Cut(fullName, "/")
	repoJSON := map[string]any{"full_name": fullName}
	return map[string]any{
		"number": p.Number, "state": p.State, "merged": p.Merged, "draft": p.Draft, "title": p.Title, "body": p.Body,
		"html_url": fmt.Sprintf("https://github.test/%s/pull/%d", fullName, p.Number),
		"node_id":  fmt.Sprintf("PR_%d", p.Number),
		"merged_at": func() any {
			if p.Merged {
				return "2026-09-21T12:00:00Z"
			}
			return nil
		}(),
		"updated_at": "2026-09-21T12:00:00Z",
		"head":       map[string]any{"ref": p.Head, "sha": st.branches[p.Head], "label": owner + ":" + p.Head, "repo": repoJSON},
		"base":       map[string]any{"ref": p.Base, "sha": st.branches[p.Base], "repo": repoJSON},
	}
}

// serveRepo handles /repos/{owner}/{name}/... for installation tokens. It reports whether it answered.
func (g *fakeGitHub) serveRepo(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 3 || parts[0] != "repos" {
		return false
	}
	fullName := parts[1] + "/" + parts[2]
	rp, in, ok := g.findRepo(fullName)
	if !ok {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return true
	}
	rest := strings.Join(parts[3:], "/")
	st := g.state(fullName)
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	str := func(k string) string { s, _ := body[k].(string); return s }
	switch {
	case r.Method == http.MethodGet && rest == "installation":
		writeJSON(w, http.StatusOK, map[string]any{"id": in})
	case r.Method == http.MethodGet && rest == "":
		writeJSON(w, http.StatusOK, rp)
	case r.Method == http.MethodGet && (strings.HasPrefix(rest, "git/ref/heads/") || strings.HasPrefix(rest, "git/refs/heads/")):
		branch := strings.TrimPrefix(strings.TrimPrefix(rest, "git/ref/heads/"), "git/refs/heads/")
		id, ok := st.branches[branch]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]any{"sha": id, "type": "commit"}})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "branches/"):
		id, ok := st.branches[strings.TrimPrefix(rest, "branches/")]
		if !ok {
			http.Error(w, `{"message":"Branch not found"}`, http.StatusNotFound)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": strings.TrimPrefix(rest, "branches/"), "commit": map[string]any{"sha": id}})
	case r.Method == http.MethodPost && rest == "git/refs":
		branch := strings.TrimPrefix(str("ref"), "refs/heads/")
		if _, exists := st.branches[branch]; exists {
			http.Error(w, `{"message":"Reference already exists"}`, http.StatusUnprocessableEntity)
			return true
		}
		st.branches[branch] = str("sha")
		writeJSON(w, http.StatusCreated, map[string]any{"ref": str("ref"), "object": map[string]any{"sha": str("sha")}})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "contents/"):
		ref := r.URL.Query().Get("ref")
		id, isBranch := st.branches[ref]
		if !isBranch {
			id = ref
		}
		if ref == "" {
			id = st.branches[rp.DefaultBranch]
		}
		path := strings.TrimPrefix(rest, "contents/")
		content, ok := st.commits[id][path]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"type": "file", "encoding": "base64", "path": path, "sha": sha("blob", content), "size": len(content),
			"content": base64.StdEncoding.EncodeToString(content),
		})
	case r.Method == http.MethodGet && strings.HasSuffix(rest, "/check-runs"):
		writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "git/commits/"):
		id := strings.TrimPrefix(rest, "git/commits/")
		writeJSON(w, http.StatusOK, map[string]any{"sha": id, "tree": map[string]any{"sha": "tree-of-" + id}})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "commits/"):
		id := strings.TrimPrefix(rest, "commits/")
		if b, ok := st.branches[id]; ok {
			id = b
		}
		writeJSON(w, http.StatusOK, map[string]any{"sha": id, "commit": map[string]any{"tree": map[string]any{"sha": "tree-of-" + id}}})
	case r.Method == http.MethodPost && rest == "git/blobs":
		content := []byte(str("content"))
		if str("encoding") == "base64" {
			content, _ = base64.StdEncoding.DecodeString(str("content"))
		}
		id := sha("blob", content)
		st.blobs[id] = content
		writeJSON(w, http.StatusCreated, map[string]any{"sha": id})
	case r.Method == http.MethodPost && rest == "git/trees":
		snap := clone(st.commits[strings.TrimPrefix(str("base_tree"), "tree-of-")])
		entries, _ := body["tree"].([]any)
		for _, e := range entries {
			entry, _ := e.(map[string]any)
			path, _ := entry["path"].(string)
			if blob, ok := entry["sha"].(string); ok {
				snap[path] = st.blobs[blob]
			} else if content, ok := entry["content"].(string); ok {
				snap[path] = []byte(content)
			}
		}
		id := sha("tree", len(st.trees), str("base_tree"))
		st.trees[id] = snap
		writeJSON(w, http.StatusCreated, map[string]any{"sha": id})
	case r.Method == http.MethodPost && rest == "git/commits":
		id := sha("commit", len(st.commits), str("tree"))
		st.commits[id] = st.trees[str("tree")]
		writeJSON(w, http.StatusCreated, map[string]any{"sha": id})
	case r.Method == http.MethodPatch && strings.HasPrefix(rest, "git/refs/heads/"):
		st.branches[strings.TrimPrefix(rest, "git/refs/heads/")] = str("sha")
		writeJSON(w, http.StatusOK, map[string]any{"object": map[string]any{"sha": str("sha")}})
	case r.Method == http.MethodGet && rest == "pulls":
		q := r.URL.Query()
		_, head, _ := strings.Cut(q.Get("head"), ":")
		out := []map[string]any{}
		for i := len(st.pulls) - 1; i >= 0; i-- {
			p := st.pulls[i]
			if (head == "" || p.Head == head) && (q.Get("state") == "all" || q.Get("state") == p.State || q.Get("state") == "" && p.State == "open") {
				out = append(out, st.pullJSON(fullName, p))
			}
		}
		writeJSON(w, http.StatusOK, out)
	case r.Method == http.MethodPost && rest == "pulls":
		draft, _ := body["draft"].(bool)
		p := &ghPull{Number: len(st.pulls) + 1, Title: str("title"), Body: str("body"), Head: str("head"), Base: str("base"), Draft: draft, State: "open"}
		st.pulls = append(st.pulls, p)
		writeJSON(w, http.StatusCreated, st.pullJSON(fullName, p))
	case len(parts) == 5 && parts[3] == "pulls":
		n, _ := strconv.Atoi(parts[4])
		if n < 1 || n > len(st.pulls) {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return true
		}
		p := st.pulls[n-1]
		if r.Method == http.MethodPatch {
			if b, ok := body["body"].(string); ok {
				p.Body = b
			}
		}
		writeJSON(w, http.StatusOK, st.pullJSON(fullName, p))
	case r.Method == http.MethodPost && strings.HasPrefix(rest, "actions/workflows/") && strings.HasSuffix(rest, "/dispatches"):
		st.dispatch = append(st.dispatch, strings.TrimSuffix(strings.TrimPrefix(rest, "actions/workflows/"), "/dispatches"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "actions/"):
		writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "workflow_runs": []any{}})
	default:
		return false
	}
	return true
}
