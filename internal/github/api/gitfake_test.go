package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// muxEnv serves GitHub from a ServeMux; a request no pattern matches fails the test.
func muxEnv(t *testing.T, routes func(mux *http.ServeMux)) *testEnv {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.github.com/app", appHandler)
	routes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s%s", r.Method, r.Host, r.URL.RequestURI())
		w.WriteHeader(http.StatusInternalServerError)
	})
	return newEnv(t, mux)
}

func fakeSHA(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:20])
}

type fakeCommit struct {
	tree    string
	parents []string
	message string
}

// fakeRepo is a stateful o/r: refs, git objects and pull requests, enough for the Changes role.
type fakeRepo struct {
	t  *testing.T
	mu sync.Mutex

	refs    map[string]string
	commits map[string]fakeCommit
	trees   map[string]map[string]string // tree SHA → path → blob SHA
	blobs   map[string][]byte
	pulls   []map[string]any

	raceRef      bool // the next ref creation loses a race: the ref appears, the answer is 422
	racePull     bool // the same for the next pull creation
	moveOnCommit bool // someone else pushes to the branch while a commit is being made
	graphqlCalls int
	graphqlBody  map[string]any
}

func newFakeRepo(t *testing.T) *fakeRepo {
	f := &fakeRepo{t: t, refs: map[string]string{}, commits: map[string]fakeCommit{},
		trees: map[string]map[string]string{}, blobs: map[string][]byte{}}
	tree := f.putTree(map[string]string{"README.md": f.putBlob([]byte("readme"))})
	f.refs["main"] = f.putCommit(fakeCommit{tree: tree, message: "init"})
	return f
}

func (f *fakeRepo) putBlob(content []byte) string {
	sha := fakeSHA("blob", string(content))
	f.blobs[sha] = content
	return sha
}

func (f *fakeRepo) putTree(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	parts := []string{"tree"}
	for _, p := range paths {
		parts = append(parts, p, files[p])
	}
	sha := fakeSHA(parts...)
	f.trees[sha] = files
	return sha
}

func (f *fakeRepo) putCommit(c fakeCommit) string {
	sha := fakeSHA(append([]string{"commit", c.tree, c.message}, c.parents...)...)
	f.commits[sha] = c
	return sha
}

func (f *fakeRepo) pullObject(number int, head, base string, draft bool) map[string]any {
	return map[string]any{
		"number": number, "node_id": fmt.Sprintf("PR_node_%d", number), "state": "open", "draft": draft,
		"body": "", "html_url": fmt.Sprintf("https://github.com/o/r/pull/%d", number),
		"head": map[string]any{"sha": f.refs[head], "ref": head, "repo": map[string]any{"full_name": "o/r"}},
		"base": map[string]any{"sha": f.refs[base], "ref": base},
	}
}

func (f *fakeRepo) fileAt(branch, path string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blobs[f.trees[f.commits[f.refs[branch]].tree][path]]
}

func (f *fakeRepo) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.commits)
}

func readJSON(t *testing.T, r *http.Request) map[string]any {
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Errorf("%s %s: body is not JSON: %v", r.Method, r.URL.Path, err)
	}
	return m
}

func (f *fakeRepo) routes(mux *http.ServeMux) {
	locked := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			h(w, r)
		}
	}
	mux.HandleFunc("GET /repos/o/r/git/ref/heads/{branch...}", locked(func(w http.ResponseWriter, r *http.Request) {
		sha, ok := f.refs[r.PathValue("branch")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ref": "refs/heads/" + r.PathValue("branch"), "object": map[string]any{"sha": sha}})
	}))
	mux.HandleFunc("POST /repos/o/r/git/refs", locked(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(f.t, r)
		branch := strings.TrimPrefix(body["ref"].(string), "refs/heads/")
		if _, exists := f.refs[branch]; exists {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		f.refs[branch] = body["sha"].(string)
		if f.raceRef {
			f.raceRef = false
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ref": body["ref"]})
	}))
	mux.HandleFunc("GET /repos/o/r/git/commits/{sha}", locked(func(w http.ResponseWriter, r *http.Request) {
		c, ok := f.commits[r.PathValue("sha")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sha": r.PathValue("sha"), "tree": map[string]any{"sha": c.tree}})
	}))
	mux.HandleFunc("POST /repos/o/r/git/blobs", locked(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(f.t, r)
		if body["encoding"] != "base64" {
			f.t.Errorf("blob encoding = %v", body["encoding"])
		}
		content, err := base64.StdEncoding.DecodeString(body["content"].(string))
		if err != nil {
			f.t.Errorf("blob content: %v", err)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"sha": f.putBlob(content)})
	}))
	mux.HandleFunc("POST /repos/o/r/git/trees", locked(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(f.t, r)
		files := map[string]string{}
		for p, sha := range f.trees[body["base_tree"].(string)] {
			files[p] = sha
		}
		for _, e := range body["tree"].([]any) {
			entry := e.(map[string]any)
			if entry["mode"] != "100644" || entry["type"] != "blob" {
				f.t.Errorf("tree entry = %v", entry)
			}
			files[entry["path"].(string)] = entry["sha"].(string)
		}
		writeJSON(w, http.StatusCreated, map[string]any{"sha": f.putTree(files)})
	}))
	mux.HandleFunc("POST /repos/o/r/git/commits", locked(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(f.t, r)
		c := fakeCommit{tree: body["tree"].(string), message: body["message"].(string)}
		for _, p := range body["parents"].([]any) {
			c.parents = append(c.parents, p.(string))
		}
		if f.moveOnCommit {
			f.moveOnCommit = false
			for branch, head := range f.refs {
				f.refs[branch] = f.putCommit(fakeCommit{tree: f.commits[head].tree, parents: []string{head}, message: "someone else"})
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"sha": f.putCommit(c)})
	}))
	mux.HandleFunc("PATCH /repos/o/r/git/refs/heads/{branch...}", locked(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(f.t, r)
		if force, ok := body["force"].(bool); !ok || force {
			f.t.Errorf("ref update force = %v, want an explicit false", body["force"])
		}
		sha := body["sha"].(string)
		parents := f.commits[sha].parents
		if len(parents) != 1 || parents[0] != f.refs[r.PathValue("branch")] {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		f.refs[r.PathValue("branch")] = sha
		writeJSON(w, http.StatusOK, map[string]any{"object": map[string]any{"sha": sha}})
	}))
	mux.HandleFunc("GET /repos/o/r/pulls", locked(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		out := []map[string]any{}
		for _, p := range f.pulls {
			head := p["head"].(map[string]any)["ref"].(string)
			base := p["base"].(map[string]any)["ref"].(string)
			if q.Get("state") == "open" && q.Get("head") == "o:"+head && q.Get("base") == base {
				out = append(out, p)
			}
		}
		writeJSON(w, http.StatusOK, out)
	}))
	mux.HandleFunc("POST /repos/o/r/pulls", locked(func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(f.t, r)
		head, base := body["head"].(string), body["base"].(string)
		for _, p := range f.pulls {
			if p["head"].(map[string]any)["ref"] == head {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
		}
		p := f.pullObject(len(f.pulls)+1, head, base, body["draft"].(bool))
		p["title"], p["body"] = body["title"], body["body"]
		f.pulls = append(f.pulls, p)
		if f.racePull {
			f.racePull = false
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, http.StatusCreated, p)
	}))
	pullByNumber := func(r *http.Request) map[string]any {
		n, _ := strconv.Atoi(r.PathValue("n"))
		if n < 1 || n > len(f.pulls) {
			return nil
		}
		return f.pulls[n-1]
	}
	mux.HandleFunc("GET /repos/o/r/pulls/{n}", locked(func(w http.ResponseWriter, r *http.Request) {
		p := pullByNumber(r)
		if p == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}))
	mux.HandleFunc("PATCH /repos/o/r/pulls/{n}", locked(func(w http.ResponseWriter, r *http.Request) {
		p := pullByNumber(r)
		if p == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		p["body"] = readJSON(f.t, r)["body"]
		writeJSON(w, http.StatusOK, p)
	}))
	mux.HandleFunc("POST /graphql", locked(func(w http.ResponseWriter, r *http.Request) {
		f.graphqlCalls++
		f.graphqlBody = readJSON(f.t, r)
		id := f.graphqlBody["variables"].(map[string]any)["id"]
		for _, p := range f.pulls {
			if p["node_id"] == id {
				p["draft"] = false
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{}})
	}))
}
