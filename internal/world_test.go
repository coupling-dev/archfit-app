package internal_test

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // fake git object IDs
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	appID   = 1
	appSlug = "archfit"
	botUser = appSlug + "[bot]"

	hostAPI   = "api.github.com"
	hostOAuth = "github.com"
	hostOIDC  = "token.actions.githubusercontent.com"
)

// world is GitHub as the App sees it: the REST API, the OAuth exchange and the Actions OIDC key set,
// all in memory. It is an http.RoundTripper, so the real clients reach it without a socket; any
// other host is refused, which keeps every test off the network.
type world struct {
	t   *testing.T
	mux *http.ServeMux

	mu          sync.Mutex
	installs    map[int64]*installation
	repos       map[string]*repo // by full name
	perms       map[string]string
	teams       map[string]bool // "org/slug login"
	nextID      int64
	requests    []string
	writes      []string
	crossTenant []string // requests that reached a repository with another installation's token
	failures    []*failure
	wrote       chan struct{}
}

type installation struct {
	id        int64
	suspended bool
}

// failure forces a status for matching requests; times < 0 means forever.
type failure struct {
	match  func(r *http.Request) bool
	status int
	times  int
}

type repo struct {
	ID            int64
	FullName      string
	Private       bool
	DefaultBranch string
	installation  int64

	branches   map[string]string
	commits    map[string]map[string][]byte // commit SHA → path → content
	blobs      map[string][]byte
	trees      map[string]map[string][]byte
	pulls      []*pull
	checks     []*checkRun
	comments   []*comment
	runs       map[int64]*run
	reruns     []int64
	dispatches []dispatch
}

type pull struct {
	Number    int
	Title     string
	Body      string
	HeadRef   string
	HeadRepo  string
	BaseRef   string
	BaseSHA   string
	MergeBase string
	// MergeCommit is GitHub's test merge commit of the head into the base; "" until GitHub has computed it.
	MergeCommit string
	Draft       bool
	State       string
	Merged      bool
	reviews     []review
}

type review struct{ Login, State, Commit string }

type checkRun struct {
	ID          int64
	Name        string
	HeadSHA     string
	ExternalID  string
	Status      string
	Conclusion  string
	StartedAt   string
	Title       string
	Summary     string
	Annotations []string
	Patches     int
}

type comment struct {
	ID      int64
	Pull    int
	Body    string
	Login   string
	Patches int
}

type run struct {
	ID         int64
	Attempt    int
	Event      string
	HeadSHA    string
	HeadRepo   string
	Path       string
	Branch     string
	Status     string
	Conclusion string
	Pulls      []int
}

type dispatch struct {
	Workflow string
	Ref      string
	Inputs   map[string]string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		t: t, installs: map[int64]*installation{}, repos: map[string]*repo{}, perms: map[string]string{},
		teams: map[string]bool{}, nextID: 1000, wrote: make(chan struct{}, 4096),
	}
	w.routes()
	return w
}

// RoundTrip serves the three GitHub hosts in process and refuses everything else.
func (w *world) RoundTrip(r *http.Request) (*http.Response, error) {
	var h http.Handler
	switch r.URL.Host {
	case hostAPI:
		h = http.HandlerFunc(w.serveAPI)
	case hostOAuth:
		h = http.HandlerFunc(serveOAuth)
	case hostOIDC:
		h = http.HandlerFunc(serveJWKS)
	default:
		return nil, fmt.Errorf("test world: outbound request to %s refused", r.URL.Host)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

func (w *world) client() *http.Client { return &http.Client{Transport: w} }

func (w *world) locked(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
}

func (w *world) id() int64 {
	w.nextID++
	return w.nextID
}

func sha(parts ...any) string {
	sum := sha1.Sum([]byte(fmt.Sprint(parts...))) //nolint:gosec // fake git object IDs
	return hex.EncodeToString(sum[:])
}

func treeSHA(snap map[string][]byte) string {
	paths := make([]string, 0, len(snap))
	for p := range snap {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	parts := []any{"tree"}
	for _, p := range paths {
		parts = append(parts, p, string(snap[p]))
	}
	return sha(parts...)
}

func cloneSnap(m map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---- seeding and inspection (test side) ----

func (w *world) install(id int64) {
	w.locked(func() { w.installs[id] = &installation{id: id} })
}

func (w *world) uninstall(id int64) {
	w.locked(func() {
		delete(w.installs, id)
		for name, rp := range w.repos {
			if rp.installation == id {
				delete(w.repos, name)
			}
		}
	})
}

// addRepo registers a repository whose default branch main points at an empty root commit.
func (w *world) addRepo(installationID, repoID int64, fullName string, private bool) {
	w.locked(func() {
		root := sha("root", fullName)
		w.repos[fullName] = &repo{
			ID: repoID, FullName: fullName, Private: private, DefaultBranch: "main", installation: installationID,
			branches: map[string]string{"main": root}, commits: map[string]map[string][]byte{root: {}},
			blobs: map[string][]byte{}, trees: map[string]map[string][]byte{}, runs: map[int64]*run{},
		}
	})
}

// edit runs f with the repository under the world lock.
func (w *world) edit(fullName string, f func(rp *repo)) {
	w.t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	rp, ok := w.repos[fullName]
	if !ok {
		w.t.Fatalf("test world: no repository %s", fullName)
	}
	f(rp)
}

// workflowsDiffer reports whether two snapshots differ in any file under .github/workflows/.
func workflowsDiffer(a, b map[string][]byte) bool {
	for _, pair := range [][2]map[string][]byte{{a, b}, {b, a}} {
		for path, c := range pair[0] {
			if strings.HasPrefix(path, ".github/workflows/") && !bytes.Equal(c, pair[1][path]) {
				return true
			}
		}
	}
	return false
}

// push points branch at commit id holding the branch's previous files overlaid with files.
func (rp *repo) push(branch, id string, files map[string]string) {
	snap := cloneSnap(rp.commits[rp.branches[branch]])
	for p, c := range files {
		snap[p] = []byte(c)
	}
	rp.commits[id] = snap
	rp.branches[branch] = id
}

// openPull opens a pull request from headRef (already pushed) into base.
func (rp *repo) openPull(number int, headRef, base string) *pull {
	p := &pull{
		Number: number, HeadRef: headRef, HeadRepo: rp.FullName, BaseRef: base,
		BaseSHA: rp.branches[base], MergeBase: rp.branches[base], State: "open",
	}
	rp.pulls = append(rp.pulls, p)
	return p
}

func (rp *repo) pull(number int) *pull {
	for _, p := range rp.pulls {
		if p.Number == number {
			return p
		}
	}
	return nil
}

// merge merges the open pull request of branch into its base.
func (rp *repo) merge(t *testing.T, branch string) {
	t.Helper()
	for _, p := range rp.pulls {
		if p.HeadRef == branch && p.State == "open" {
			snap := cloneSnap(rp.commits[rp.branches[p.BaseRef]])
			for path, c := range rp.commits[rp.branches[branch]] {
				snap[path] = c
			}
			id := sha("merge", branch, len(rp.commits))
			rp.commits[id], rp.branches[p.BaseRef] = snap, id
			p.State, p.Merged, p.Draft = "closed", true, false
			return
		}
	}
	t.Fatalf("test world: no open pull request for %s", branch)
}

func (rp *repo) changedFiles(p *pull) []string {
	head, base := rp.commits[rp.branches[p.HeadRef]], rp.commits[p.BaseSHA]
	var out []string
	for path, c := range head {
		if b, ok := base[path]; !ok || string(b) != string(c) {
			out = append(out, path)
		}
	}
	for path := range base {
		if _, ok := head[path]; !ok {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// check returns the newest check run of a name on a head SHA, or nil.
func (w *world) check(fullName, headSHA, name string) *checkRun {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out *checkRun
	for _, c := range w.repos[fullName].checks {
		if c.HeadSHA == headSHA && c.Name == name {
			cp := *c
			out = &cp
		}
	}
	return out
}

func (w *world) checkRuns(fullName string) []checkRun {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []checkRun
	for _, c := range w.repos[fullName].checks {
		out = append(out, *c)
	}
	return out
}

func (w *world) commentsOf(fullName string, number int) []comment {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []comment
	for _, c := range w.repos[fullName].comments {
		if c.Pull == number {
			out = append(out, *c)
		}
	}
	return out
}

func (w *world) pullsOf(fullName string) []pull {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []pull
	for _, p := range w.repos[fullName].pulls {
		out = append(out, *p)
	}
	return out
}

func (w *world) file(fullName, branch, path string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rp := w.repos[fullName]
	c, ok := rp.commits[rp.branches[branch]][path]
	return string(c), ok
}

func (w *world) rerunsOf(fullName string) []int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.repos[fullName].reruns)
}

func (w *world) dispatchesOf(fullName string) []dispatch {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.repos[fullName].dispatches)
}

func (w *world) requestCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.requests)
}

func (w *world) crossTenantRequests() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.crossTenant)
}

// everythingPublished is every string the App wrote to GitHub: checks, comments, pull request bodies.
func (w *world) everythingPublished() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	for _, rp := range w.repos {
		for _, c := range rp.checks {
			b.WriteString(c.Title + "\n" + c.Summary + "\n" + c.ExternalID + "\n" + strings.Join(c.Annotations, "\n") + "\n")
		}
		for _, c := range rp.comments {
			if c.Login == botUser {
				b.WriteString(c.Body + "\n")
			}
		}
		for _, p := range rp.pulls {
			b.WriteString(p.Title + "\n" + p.Body + "\n")
		}
	}
	return b.String()
}

// fail forces status for the next times requests that match; times < 0 means every one.
func (w *world) fail(status, times int, match func(r *http.Request) bool) {
	w.locked(func() { w.failures = append(w.failures, &failure{match: match, status: status, times: times}) })
}

func (w *world) allow(login, fullName, perm string) {
	w.locked(func() { w.perms[login+" "+fullName] = perm })
}

// awaitWrites blocks until done() holds, woken by every write the App makes. No sleeping.
func (w *world) awaitWrites(what string, done func() bool) {
	w.t.Helper()
	timeout := time.After(20 * time.Second)
	for !done() {
		select {
		case <-w.wrote:
		case <-timeout:
			w.t.Fatalf("test world: timed out waiting for %s", what)
		}
	}
}

// ---- credentials ----

type caller struct {
	app          bool
	installation int64
	user         string
}

func callerOf(r *http.Request) caller {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	var c caller
	switch {
	case strings.HasPrefix(tok, "user-"):
		c.user = strings.TrimPrefix(tok, "user-")
	case strings.HasPrefix(tok, "tok-"), strings.HasPrefix(tok, "read-"):
		_, _ = fmt.Sscanf(tok[strings.Index(tok, "-")+1:], "%d", &c.installation)
	case strings.Count(tok, ".") == 2:
		c.app = true
	}
	return c
}

var userIDs = map[string]int64{"alice": 1001, "bob": 1002, "olivia": 1003, "mallory": 1004}

// ---- HTTP side ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) { http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound) }

func serveOAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/login/oauth/access_token" {
		notFound(w)
		return
	}
	_ = r.ParseForm()
	login := r.PostForm.Get("code")
	if _, known := userIDs[login]; !known {
		writeJSON(w, http.StatusOK, map[string]any{"error": "bad_verification_code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": "user-" + login})
}

func serveJWKS(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/.well-known/jwks" {
		notFound(w)
		return
	}
	pub := oidcKey().PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": oidcKeyID, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (w *world) serveAPI(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	line := r.Method + " " + r.URL.Path
	w.requests = append(w.requests, line)
	if r.Method != http.MethodGet {
		w.writes = append(w.writes, line)
		defer func() {
			select {
			case w.wrote <- struct{}{}:
			default:
			}
		}()
	}
	for _, f := range w.failures {
		if f.times != 0 && f.match(r) {
			if f.times > 0 {
				f.times--
			}
			http.Error(rw, `{"message":"forced"}`, f.status)
			return
		}
	}
	w.mux.ServeHTTP(rw, r)
}

// repoOf resolves the repository of a request and enforces the caller's reach: an installation token
// sees its own repositories only, a user token the repositories it has a permission on.
func (w *world) repoOf(rw http.ResponseWriter, r *http.Request) *repo {
	rp, ok := w.repos[r.PathValue("owner")+"/"+r.PathValue("name")]
	c := callerOf(r)
	switch {
	case !ok:
	case c.installation != 0 && c.installation == rp.installation:
		return rp
	case c.installation != 0:
		w.crossTenant = append(w.crossTenant, fmt.Sprintf("tok-%d %s %s", c.installation, r.Method, r.URL.Path))
	case c.user != "" && w.perms[c.user+" "+rp.FullName] != "":
		return rp
	}
	notFound(rw)
	return nil
}

func body(r *http.Request) map[string]any {
	var out map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func (rp *repo) json() map[string]any {
	return map[string]any{"id": rp.ID, "full_name": rp.FullName, "private": rp.Private, "default_branch": rp.DefaultBranch}
}

func (rp *repo) pullJSON(p *pull) map[string]any {
	var mergedAt any
	if p.Merged {
		mergedAt = "2026-09-21T12:00:00Z"
	}
	return map[string]any{
		"number": p.Number, "node_id": fmt.Sprintf("PR_%d", p.Number), "state": p.State, "draft": p.Draft,
		"title": p.Title, "body": p.Body, "html_url": fmt.Sprintf("https://github.com/%s/pull/%d", rp.FullName, p.Number),
		"merged_at": mergedAt, "updated_at": "2026-09-21T12:00:00Z", "changed_files": len(rp.changedFiles(p)), "merge_commit_sha": nilIfEmpty(p.MergeCommit),
		"head": map[string]any{"sha": rp.branches[p.HeadRef], "ref": p.HeadRef, "repo": map[string]any{"full_name": p.HeadRepo}},
		"base": map[string]any{"sha": p.BaseSHA, "ref": p.BaseRef},
	}
}

func (r *run) json() map[string]any {
	pulls := []map[string]any{}
	for _, n := range r.Pulls {
		pulls = append(pulls, map[string]any{"number": n})
	}
	var conclusion any
	if r.Conclusion != "" {
		conclusion = r.Conclusion
	}
	return map[string]any{
		"id": r.ID, "run_attempt": r.Attempt, "event": r.Event, "head_sha": r.HeadSHA, "path": r.Path,
		"status": r.Status, "conclusion": conclusion, "head_repository": map[string]any{"full_name": r.HeadRepo},
		"pull_requests": pulls,
	}
}

func (c *checkRun) json() map[string]any {
	var conclusion, started any
	if c.Conclusion != "" {
		conclusion = c.Conclusion
	}
	if c.StartedAt != "" {
		started = c.StartedAt
	}
	return map[string]any{
		"id": c.ID, "name": c.Name, "head_sha": c.HeadSHA, "external_id": c.ExternalID, "status": c.Status,
		"conclusion": conclusion, "started_at": started, "app": map[string]any{"id": appID},
		"output": map[string]any{"title": c.Title, "summary": c.Summary, "annotations_count": len(c.Annotations)},
	}
}

func (c *checkRun) apply(b map[string]any) {
	for k, dst := range map[string]*string{
		"name": &c.Name, "head_sha": &c.HeadSHA, "external_id": &c.ExternalID, "status": &c.Status,
		"conclusion": &c.Conclusion, "started_at": &c.StartedAt,
	} {
		if v, ok := b[k].(string); ok {
			*dst = v
		}
	}
	if c.Status != "completed" {
		c.Conclusion = ""
	}
	if out, ok := b["output"].(map[string]any); ok {
		c.Title, c.Summary = str(out, "title"), str(out, "summary")
		// As GitHub: the annotations of every request are appended, never replaced.
		anns, _ := out["annotations"].([]any)
		for _, a := range anns {
			am, _ := a.(map[string]any)
			c.Annotations = append(c.Annotations, str(am, "path")+" "+str(am, "message"))
		}
	}
}

func (w *world) routes() {
	mux := http.NewServeMux()
	w.mux = mux
	const R = "/repos/{owner}/{name}"

	mux.HandleFunc("GET /app", func(rw http.ResponseWriter, _ *http.Request) {
		writeJSON(rw, http.StatusOK, map[string]any{"id": appID, "slug": appSlug, "name": "Archfit"})
	})
	mux.HandleFunc("GET /app/installations", func(rw http.ResponseWriter, r *http.Request) {
		if !callerOf(r).app {
			http.Error(rw, `{"message":"A JSON web token is required"}`, http.StatusUnauthorized)
			return
		}
		out := []map[string]any{}
		for _, in := range w.installs {
			var suspended any
			if in.suspended {
				suspended = "2026-09-01T00:00:00Z"
			}
			// The account is the owner of the installation's repositories, as on GitHub; "org<id>" without one.
			account := fmt.Sprintf("org%d", in.id)
			for name, rp := range w.repos {
				if rp.installation == in.id {
					account, _, _ = strings.Cut(name, "/")
				}
			}
			out = append(out, map[string]any{"id": in.id, "account": map[string]any{"login": account, "type": "User"},
				"created_at": "2026-10-20T09:00:00Z", "suspended_at": suspended})
		}
		writeJSON(rw, http.StatusOK, out)
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(rw http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if _, ok := w.installs[id]; !ok || !callerOf(r).app {
			notFound(rw)
			return
		}
		resp := map[string]any{"token": fmt.Sprintf("tok-%d", id), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
		if names, ok := body(r)["repositories"].([]any); ok && len(names) == 1 {
			for _, rp := range w.repos {
				if rp.installation == id && strings.HasSuffix(rp.FullName, "/"+fmt.Sprint(names[0])) {
					resp["token"] = fmt.Sprintf("read-%d", id)
					resp["permissions"] = map[string]string{"contents": "read"}
					resp["repositories"] = []map[string]any{{"full_name": rp.FullName}}
				}
			}
		}
		writeJSON(rw, http.StatusCreated, resp)
	})
	mux.HandleFunc("GET /installation/repositories", func(rw http.ResponseWriter, r *http.Request) {
		c := callerOf(r)
		if _, ok := w.installs[c.installation]; !ok {
			notFound(rw)
			return
		}
		out := []map[string]any{}
		for _, rp := range w.repos {
			if rp.installation == c.installation {
				out = append(out, rp.json())
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["id"].(int64) < out[j]["id"].(int64) })
		writeJSON(rw, http.StatusOK, map[string]any{"total_count": len(out), "repositories": out})
	})
	mux.HandleFunc("GET /user", func(rw http.ResponseWriter, r *http.Request) {
		login := callerOf(r).user
		if userIDs[login] == 0 {
			http.Error(rw, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"login": login, "id": userIDs[login]})
	})
	mux.HandleFunc("GET /user/installations", func(rw http.ResponseWriter, r *http.Request) {
		login := callerOf(r).user
		seen, out := map[int64]bool{}, []map[string]any{}
		for _, rp := range w.repos {
			if w.perms[login+" "+rp.FullName] != "" && !seen[rp.installation] {
				seen[rp.installation] = true
				out = append(out, map[string]any{"id": rp.installation})
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"total_count": len(out), "installations": out})
	})
	mux.HandleFunc("GET /user/installations/{id}/repositories", func(rw http.ResponseWriter, r *http.Request) {
		login := callerOf(r).user
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		out := []map[string]any{}
		for _, rp := range w.repos {
			if perm := w.perms[login+" "+rp.FullName]; perm != "" && rp.installation == id {
				out = append(out, rp.userJSON(perm))
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"total_count": len(out), "repositories": out})
	})
	mux.HandleFunc("GET /orgs/{org}/teams/{slug}/memberships/{login}", func(rw http.ResponseWriter, r *http.Request) {
		if !w.teams[r.PathValue("org")+"/"+r.PathValue("slug")+" "+strings.ToLower(r.PathValue("login"))] {
			notFound(rw)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"state": "active"})
	})

	mux.HandleFunc("GET "+R+"/installation", func(rw http.ResponseWriter, r *http.Request) {
		rp, ok := w.repos[r.PathValue("owner")+"/"+r.PathValue("name")]
		if !ok || !callerOf(r).app {
			notFound(rw)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"id": rp.installation})
	})
	mux.HandleFunc("GET "+R, func(rw http.ResponseWriter, r *http.Request) {
		if rp := w.repoOf(rw, r); rp != nil {
			if login := callerOf(r).user; login != "" {
				writeJSON(rw, http.StatusOK, rp.userJSON(w.perms[login+" "+rp.FullName]))
				return
			}
			writeJSON(rw, http.StatusOK, rp.json())
		}
	})
	mux.HandleFunc("GET "+R+"/contents/{path...}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		ref := r.URL.Query().Get("ref")
		if ref == "" {
			ref = rp.DefaultBranch
		}
		id, isBranch := rp.branches[ref]
		if !isBranch {
			id = ref
		}
		content, ok := rp.commits[id][r.PathValue("path")]
		if !ok {
			notFound(rw)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"type": "file", "encoding": "base64", "path": r.PathValue("path"), "sha": sha("blob", string(content)),
			"size": len(content), "content": base64.StdEncoding.EncodeToString(content),
		})
	})
	mux.HandleFunc("GET "+R+"/git/ref/heads/{branch...}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		id, ok := rp.branches[r.PathValue("branch")]
		if !ok {
			notFound(rw)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"ref": "refs/heads/" + r.PathValue("branch"), "object": map[string]any{"sha": id, "type": "commit"}})
	})
	mux.HandleFunc("POST "+R+"/git/refs", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		b := body(r)
		branch := strings.TrimPrefix(str(b, "ref"), "refs/heads/")
		if _, exists := rp.branches[branch]; exists {
			http.Error(rw, `{"message":"Reference already exists"}`, http.StatusUnprocessableEntity)
			return
		}
		rp.branches[branch] = str(b, "sha")
		writeJSON(rw, http.StatusCreated, map[string]any{"ref": str(b, "ref")})
	})
	mux.HandleFunc("PATCH "+R+"/git/refs/heads/{branch...}", func(rw http.ResponseWriter, r *http.Request) {
		if rp := w.repoOf(rw, r); rp != nil {
			branch, next := r.PathValue("branch"), str(body(r), "sha")
			// The App is registered without the "workflows" permission, so GitHub refuses any workflow change it pushes.
			if workflowsDiffer(rp.commits[rp.branches[branch]], rp.commits[next]) {
				http.Error(rw, `{"message":"refusing to allow a GitHub App to create or update workflow without workflows permission"}`, http.StatusForbidden)
				return
			}
			rp.branches[branch] = next
			writeJSON(rw, http.StatusOK, map[string]any{})
		}
	})
	mux.HandleFunc("GET "+R+"/git/commits/{sha}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		snap, ok := rp.commits[r.PathValue("sha")]
		if !ok {
			notFound(rw)
			return
		}
		tree := treeSHA(snap)
		rp.trees[tree] = snap
		writeJSON(rw, http.StatusOK, map[string]any{"sha": r.PathValue("sha"), "tree": map[string]any{"sha": tree}})
	})
	mux.HandleFunc("POST "+R+"/git/blobs", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		b := body(r)
		content := []byte(str(b, "content"))
		if str(b, "encoding") == "base64" {
			content, _ = base64.StdEncoding.DecodeString(str(b, "content"))
		}
		id := sha("blob", string(content))
		rp.blobs[id] = content
		writeJSON(rw, http.StatusCreated, map[string]any{"sha": id})
	})
	mux.HandleFunc("POST "+R+"/git/trees", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		b := body(r)
		snap := cloneSnap(rp.trees[str(b, "base_tree")])
		entries, _ := b["tree"].([]any)
		for _, e := range entries {
			entry, _ := e.(map[string]any)
			snap[str(entry, "path")] = rp.blobs[str(entry, "sha")]
		}
		id := treeSHA(snap)
		rp.trees[id] = snap
		writeJSON(rw, http.StatusCreated, map[string]any{"sha": id})
	})
	mux.HandleFunc("POST "+R+"/git/commits", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		b := body(r)
		id := sha("commit", len(rp.commits), str(b, "tree"))
		rp.commits[id] = rp.trees[str(b, "tree")]
		writeJSON(rw, http.StatusCreated, map[string]any{"sha": id})
	})

	mux.HandleFunc("GET "+R+"/pulls", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		q := r.URL.Query()
		_, head, _ := strings.Cut(q.Get("head"), ":")
		out := []map[string]any{}
		for i := len(rp.pulls) - 1; i >= 0; i-- {
			p := rp.pulls[i]
			stateOK := q.Get("state") == "all" || q.Get("state") == p.State || q.Get("state") == "" && p.State == "open"
			if (head == "" || p.HeadRef == head) && stateOK && (q.Get("base") == "" || q.Get("base") == p.BaseRef) {
				out = append(out, rp.pullJSON(p))
			}
		}
		writeJSON(rw, http.StatusOK, out)
	})
	mux.HandleFunc("POST "+R+"/pulls", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		b := body(r)
		for _, p := range rp.pulls {
			if p.State == "open" && p.HeadRef == str(b, "head") {
				http.Error(rw, `{"message":"A pull request already exists"}`, http.StatusUnprocessableEntity)
				return
			}
		}
		p := rp.openPull(len(rp.pulls)+1, str(b, "head"), str(b, "base"))
		p.Title, p.Body = str(b, "title"), str(b, "body")
		p.Draft, _ = b["draft"].(bool)
		writeJSON(rw, http.StatusCreated, rp.pullJSON(p))
	})
	pullOf := func(rw http.ResponseWriter, r *http.Request) (*repo, *pull) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return nil, nil
		}
		n, _ := strconv.Atoi(r.PathValue("n"))
		p := rp.pull(n)
		if p == nil {
			notFound(rw)
			return nil, nil
		}
		return rp, p
	}
	mux.HandleFunc("GET "+R+"/pulls/{n}", func(rw http.ResponseWriter, r *http.Request) {
		if rp, p := pullOf(rw, r); p != nil {
			writeJSON(rw, http.StatusOK, rp.pullJSON(p))
		}
	})
	mux.HandleFunc("PATCH "+R+"/pulls/{n}", func(rw http.ResponseWriter, r *http.Request) {
		if rp, p := pullOf(rw, r); p != nil {
			if b, ok := body(r)["body"].(string); ok {
				p.Body = b
			}
			writeJSON(rw, http.StatusOK, rp.pullJSON(p))
		}
	})
	mux.HandleFunc("GET "+R+"/pulls/{n}/files", func(rw http.ResponseWriter, r *http.Request) {
		if rp, p := pullOf(rw, r); p != nil {
			out := []map[string]any{}
			for _, path := range rp.changedFiles(p) {
				out = append(out, map[string]any{"filename": path})
			}
			writeJSON(rw, http.StatusOK, out)
		}
	})
	mux.HandleFunc("GET "+R+"/pulls/{n}/reviews", func(rw http.ResponseWriter, r *http.Request) {
		if _, p := pullOf(rw, r); p != nil {
			out := []map[string]any{}
			for _, rv := range p.reviews {
				out = append(out, map[string]any{"user": map[string]any{"login": rv.Login}, "state": rv.State, "commit_id": rv.Commit})
			}
			writeJSON(rw, http.StatusOK, out)
		}
	})
	mux.HandleFunc("GET "+R+"/compare/{spec}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		base, head, _ := strings.Cut(r.PathValue("spec"), "...")
		for _, p := range rp.pulls {
			if p.BaseSHA == base && rp.branches[p.HeadRef] == head {
				writeJSON(rw, http.StatusOK, map[string]any{"merge_base_commit": map[string]any{"sha": p.MergeBase}})
				return
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"merge_base_commit": map[string]any{"sha": base}})
	})

	mux.HandleFunc("GET "+R+"/commits/{sha}/check-runs", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		out := []map[string]any{}
		for _, c := range rp.checks {
			if c.HeadSHA == r.PathValue("sha") && (r.URL.Query().Get("check_name") == "" || r.URL.Query().Get("check_name") == c.Name) {
				out = append(out, c.json())
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"total_count": len(out), "check_runs": out})
	})
	mux.HandleFunc("POST "+R+"/check-runs", func(rw http.ResponseWriter, r *http.Request) {
		if rp := w.repoOf(rw, r); rp != nil {
			c := &checkRun{ID: w.id()}
			c.apply(body(r))
			rp.checks = append(rp.checks, c)
			writeJSON(rw, http.StatusCreated, c.json())
		}
	})
	mux.HandleFunc("PATCH "+R+"/check-runs/{id}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		for _, c := range rp.checks {
			if c.ID == id {
				c.apply(body(r))
				c.Patches++
				writeJSON(rw, http.StatusOK, c.json())
				return
			}
		}
		notFound(rw)
	})

	mux.HandleFunc("GET "+R+"/issues/{n}/comments", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		n, _ := strconv.Atoi(r.PathValue("n"))
		out := []map[string]any{}
		for _, c := range rp.comments {
			if c.Pull == n {
				out = append(out, map[string]any{"id": c.ID, "body": c.Body, "user": map[string]any{"login": c.Login}})
			}
		}
		writeJSON(rw, http.StatusOK, out)
	})
	mux.HandleFunc("POST "+R+"/issues/{n}/comments", func(rw http.ResponseWriter, r *http.Request) {
		if rp := w.repoOf(rw, r); rp != nil {
			n, _ := strconv.Atoi(r.PathValue("n"))
			c := &comment{ID: w.id(), Pull: n, Body: str(body(r), "body"), Login: botUser}
			rp.comments = append(rp.comments, c)
			writeJSON(rw, http.StatusCreated, map[string]any{"id": c.ID})
		}
	})
	mux.HandleFunc("PATCH "+R+"/issues/comments/{id}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		for _, c := range rp.comments {
			if c.ID == id {
				c.Body = str(body(r), "body")
				c.Patches++
				writeJSON(rw, http.StatusOK, map[string]any{"id": c.ID})
				return
			}
		}
		notFound(rw)
	})

	mux.HandleFunc("GET "+R+"/actions/runs/{id}", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if wr, ok := rp.runs[id]; ok {
			writeJSON(rw, http.StatusOK, wr.json())
			return
		}
		notFound(rw)
	})
	mux.HandleFunc("POST "+R+"/actions/runs/{id}/rerun", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		wr, ok := rp.runs[id]
		if !ok {
			notFound(rw)
			return
		}
		wr.Attempt++
		wr.Status, wr.Conclusion = "queued", ""
		rp.reruns = append(rp.reruns, id)
		writeJSON(rw, http.StatusCreated, map[string]any{})
	})
	mux.HandleFunc("POST "+R+"/actions/workflows/{file}/dispatches", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		b := body(r)
		d := dispatch{Workflow: r.PathValue("file"), Ref: str(b, "ref"), Inputs: map[string]string{}}
		if _, ok := rp.commits[rp.branches[d.Ref]][".github/workflows/"+d.Workflow]; !ok {
			notFound(rw)
			return
		}
		in, _ := b["inputs"].(map[string]any)
		for k, v := range in {
			d.Inputs[k] = fmt.Sprint(v)
		}
		rp.dispatches = append(rp.dispatches, d)
		rw.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET "+R+"/actions/workflows/{file}/runs", func(rw http.ResponseWriter, r *http.Request) {
		rp := w.repoOf(rw, r)
		if rp == nil {
			return
		}
		var ids []int64
		for id, wr := range rp.runs {
			if strings.HasSuffix(wr.Path, "/"+r.PathValue("file")) && (r.URL.Query().Get("branch") == "" || r.URL.Query().Get("branch") == wr.Branch) {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		out := []map[string]any{}
		if len(ids) > 0 {
			out = append(out, rp.runs[ids[len(ids)-1]].json())
		}
		writeJSON(rw, http.StatusOK, map[string]any{"total_count": len(out), "workflow_runs": out})
	})
	mux.HandleFunc("GET "+R+"/actions/runs/{id}/artifacts", func(rw http.ResponseWriter, r *http.Request) {
		if rp := w.repoOf(rw, r); rp != nil {
			writeJSON(rw, http.StatusOK, map[string]any{"total_count": 0, "artifacts": []any{}})
		}
	})
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		w.t.Logf("test world: unhandled %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		notFound(rw)
	})
}

func (rp *repo) userJSON(perm string) map[string]any {
	out := rp.json()
	out["permissions"] = map[string]bool{"admin": perm == "admin", "push": perm == "admin" || perm == "write", "pull": true}
	return out
}

// publishingWrites are the write requests other than minting an installation token.
func (w *world) publishingWrites() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, line := range w.writes {
		if !strings.HasSuffix(line, "/access_tokens") {
			out = append(out, line)
		}
	}
	return out
}

// nilIfEmpty is JSON null for "", as GitHub answers for a value it has not computed.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
