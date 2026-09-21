package github_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	hostAPI  = "api.github.com"
	hostWeb  = "github.com"
	hostOIDC = "token.actions.githubusercontent.com"

	appID   = 4242
	appSlug = "archfit"
	jwksKid = "fake-actions-key"

	instA = 101
	instB = 202

	repoWidgets = "acme/widgets"  // installation A
	repoGadgets = "acme/gadgets"  // installation A
	repoSecret  = "globex/secret" // installation B

	pullNumber   = 7
	pullHeadSHA  = "1111111111111111111111111111111111111111"
	pullBaseSHA  = "2222222222222222222222222222222222222222"
	pullMergeSHA = "3333333333333333333333333333333333333333"

	runID        = 9001
	workflowPath = ".github/workflows/archfit.yaml"

	filePath    = ".archfit.yaml"
	fileContent = "version: 1\n"

	userLogin = "octocat"
	userID    = 583231

	// Every secret of the fixture world carries this text, so one search finds any leak.
	marker = "MARKER"

	oauthClientID     = "Iv1.fixtureclient"
	oauthClientSecret = marker + "-oauth-client-secret"
	oauthCode         = marker + "-oauth-code"
	userAccessToken   = "ghu_" + marker + "-user-token"
	webhookSecret     = marker + "-webhook-secret"
)

var testKeys struct {
	once      sync.Once
	app, jwks *rsa.PrivateKey
	err       error
}

// keys returns the App key and the Actions signing key; both are generated once per test binary.
func keys(t *testing.T) (app, jwks *rsa.PrivateKey) {
	t.Helper()
	testKeys.once.Do(func() {
		if testKeys.app, testKeys.err = rsa.GenerateKey(rand.Reader, 2048); testKeys.err != nil {
			return
		}
		testKeys.jwks, testKeys.err = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testKeys.err != nil {
		t.Fatalf("generate keys: %v", testKeys.err)
	}
	return testKeys.app, testKeys.jwks
}

func signRS256(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	h, errH := json.Marshal(header)
	c, errC := json.Marshal(claims)
	if errH != nil || errC != nil {
		t.Fatalf("encode token: %v %v", errH, errC)
	}
	input := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return input + "." + enc.EncodeToString(sig)
}

// verifyRS256 reports whether token is signed by pub and returns its claims.
func verifyRS256(token string, pub *rsa.PublicKey) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) != nil {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil, false
	}
	return claims, true
}

// Credential labels the fake records per request.
const (
	credNone    = "none"
	credJWT     = "jwt"
	credBadJWT  = "jwt-refused"
	credUser    = "user:" + userLogin
	credUnknown = "unknown"
)

func credInst(id int64) string { return "inst:" + strconv.FormatInt(id, 10) }

type request struct {
	method, host, path string
	bearer, cred       string
	status             int
}

func (r request) isMint() bool { return strings.HasSuffix(r.path, "/access_tokens") }

// fakeGitHub is the one GitHub every child talks to: REST API, OAuth web flow and the Actions JWKS.
type fakeGitHub struct {
	appKey  *rsa.PublicKey
	jwksKey *rsa.PublicKey
	mux     *http.ServeMux

	mu        sync.Mutex
	installs  map[int64][]string // installation → repositories
	tokens    map[string]int64   // issued installation token → installation
	revoked   map[string]bool
	mints     map[int64]int
	userPerms map[string]map[string]bool // repository → permissions of the fixture user
	checkRuns int
	requests  []request
	override  func(http.ResponseWriter, *http.Request) bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	app, jwks := keys(t)
	f := &fakeGitHub{
		appKey:  &app.PublicKey,
		jwksKey: &jwks.PublicKey,
		mux:     http.NewServeMux(),
		installs: map[int64][]string{
			instA: {repoWidgets, repoGadgets},
			instB: {repoSecret},
		},
		tokens:  map[string]int64{},
		revoked: map[string]bool{},
		mints:   map[int64]int{},
		userPerms: map[string]map[string]bool{
			repoWidgets: {"admin": false, "push": true, "pull": true},
		},
	}
	f.mux.HandleFunc("POST "+hostWeb+"/login/oauth/access_token", f.oauthExchange)
	f.mux.HandleFunc("GET "+hostOIDC+"/.well-known/jwks", f.jwks)
	f.mux.HandleFunc("GET "+hostAPI+"/user", f.user)
	f.mux.HandleFunc("GET "+hostAPI+"/app", f.app)
	f.mux.HandleFunc("POST "+hostAPI+"/app/installations/{id}/access_tokens", f.mint)
	const repo = hostAPI + "/repos/{owner}/{name}"
	f.mux.HandleFunc("GET "+repo+"/installation", f.repoInstallation)
	f.mux.HandleFunc("GET "+repo, f.repo)
	f.mux.HandleFunc("GET "+repo+"/contents/{path...}", f.contents)
	f.mux.HandleFunc("GET "+repo+"/pulls", f.pulls)
	f.mux.HandleFunc("GET "+repo+"/pulls/{number}", f.pull)
	f.mux.HandleFunc("GET "+repo+"/compare/{basehead}", f.compare)
	f.mux.HandleFunc("GET "+repo+"/actions/runs/{id}", f.run)
	f.mux.HandleFunc("GET "+repo+"/commits/{sha}/check-runs", f.listCheckRuns)
	f.mux.HandleFunc("POST "+repo+"/check-runs", f.createCheckRun)
	return f
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	cred := f.classify(bearer)
	override := f.override
	f.mu.Unlock()
	if override == nil || !override(sw, r) {
		f.mux.ServeHTTP(sw, r)
	}
	f.mu.Lock()
	f.requests = append(f.requests, request{r.Method, r.Host, r.URL.Path, bearer, cred, sw.status})
	f.mu.Unlock()
}

// classify names the credential of a request. The caller holds f.mu.
func (f *fakeGitHub) classify(bearer string) string {
	switch {
	case bearer == "":
		return credNone
	case bearer == userAccessToken:
		return credUser
	case strings.Count(bearer, ".") == 2:
		claims, ok := verifyRS256(bearer, f.appKey)
		exp, _ := claims["exp"].(float64)
		if ok && claims["iss"] == strconv.Itoa(appID) && time.Now().Before(time.Unix(int64(exp), 0)) {
			return credJWT
		}
		return credBadJWT
	}
	if id, ok := f.tokens[bearer]; ok && !f.revoked[bearer] && f.installs[id] != nil {
		return credInst(id)
	}
	return credUnknown
}

func (f *fakeGitHub) credOf(r *http.Request) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.classify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int) {
	writeJSON(w, status, map[string]string{"message": http.StatusText(status)})
}

// requireJWT answers 401 unless the request carries a valid App JWT.
func (f *fakeGitHub) requireJWT(w http.ResponseWriter, r *http.Request) bool {
	if f.credOf(r) != credJWT {
		refuse(w, http.StatusUnauthorized)
		return false
	}
	return true
}

// installationRepo answers 401 without a live installation token and 404 for a repository outside the
// token's installation, as GitHub does.
func (f *fakeGitHub) installationRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	fullName := r.PathValue("owner") + "/" + r.PathValue("name")
	cred := f.credOf(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, repos := range f.installs {
		if cred == credInst(id) {
			if !slices.Contains(repos, fullName) {
				refuse(w, http.StatusNotFound)
				return "", false
			}
			return fullName, true
		}
	}
	refuse(w, http.StatusUnauthorized)
	return "", false
}

func (f *fakeGitHub) oauthExchange(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("client_id") != oauthClientID || r.FormValue("client_secret") != oauthClientSecret ||
		r.FormValue("code") != oauthCode {
		writeJSON(w, http.StatusOK, map[string]string{"error": "bad_verification_code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"access_token": userAccessToken, "token_type": "bearer"})
}

func (f *fakeGitHub) jwks(w http.ResponseWriter, _ *http.Request) {
	enc := base64.RawURLEncoding
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": jwksKid, "use": "sig", "alg": "RS256",
		"n": enc.EncodeToString(f.jwksKey.N.Bytes()),
		"e": enc.EncodeToString(big.NewInt(int64(f.jwksKey.E)).Bytes()),
	}}})
}

func (f *fakeGitHub) user(w http.ResponseWriter, r *http.Request) {
	if f.credOf(r) != credUser {
		refuse(w, http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"login": userLogin, "id": userID})
}

func (f *fakeGitHub) app(w http.ResponseWriter, r *http.Request) {
	if f.requireJWT(w, r) {
		writeJSON(w, http.StatusOK, map[string]any{"id": appID, "slug": appSlug})
	}
}

func (f *fakeGitHub) mint(w http.ResponseWriter, r *http.Request) {
	if !f.requireJWT(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installs[id] == nil {
		refuse(w, http.StatusNotFound)
		return
	}
	f.mints[id]++
	token := fmt.Sprintf("ghs_%s-installation-%d-token-%d", marker, id, f.mints[id])
	f.tokens[token] = id
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":       token,
		"expires_at":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"permissions": map[string]string{"checks": "write", "contents": "write", "metadata": "read"},
	})
}

func (f *fakeGitHub) repoInstallation(w http.ResponseWriter, r *http.Request) {
	if !f.requireJWT(w, r) {
		return
	}
	fullName := r.PathValue("owner") + "/" + r.PathValue("name")
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, repos := range f.installs {
		if slices.Contains(repos, fullName) {
			writeJSON(w, http.StatusOK, map[string]any{"id": id})
			return
		}
	}
	refuse(w, http.StatusNotFound)
}

func (f *fakeGitHub) repo(w http.ResponseWriter, r *http.Request) {
	fullName := r.PathValue("owner") + "/" + r.PathValue("name")
	body := map[string]any{"id": 5150, "full_name": fullName, "private": true, "default_branch": "main"}
	if f.credOf(r) == credUser {
		f.mu.Lock()
		perms := f.userPerms[fullName]
		f.mu.Unlock()
		if perms == nil {
			refuse(w, http.StatusNotFound)
			return
		}
		body["permissions"] = perms
		writeJSON(w, http.StatusOK, body)
		return
	}
	if _, ok := f.installationRepo(w, r); ok {
		writeJSON(w, http.StatusOK, body)
	}
}

func (f *fakeGitHub) contents(w http.ResponseWriter, r *http.Request) {
	fullName, ok := f.installationRepo(w, r)
	if !ok {
		return
	}
	if fullName != repoWidgets || r.PathValue("path") != filePath {
		refuse(w, http.StatusNotFound)
		return
	}
	const etag = `"file-v1"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"type": "file", "encoding": "base64", "size": len(fileContent), "sha": "blob-sha-1",
		"content": base64.StdEncoding.EncodeToString([]byte(fileContent)),
	})
}

func pullBody() map[string]any {
	return map[string]any{
		"number": pullNumber, "state": "open", "draft": false, "body": "adds a widget",
		"html_url":   "https://github.com/" + repoWidgets + "/pull/" + strconv.Itoa(pullNumber),
		"updated_at": "2026-09-01T10:00:00Z",
		"head":       map[string]any{"sha": pullHeadSHA, "ref": "feature", "repo": map[string]string{"full_name": repoWidgets}},
		"base":       map[string]any{"sha": pullBaseSHA, "ref": "main"},
	}
}

func (f *fakeGitHub) pulls(w http.ResponseWriter, r *http.Request) {
	if fullName, ok := f.installationRepo(w, r); ok {
		list := []map[string]any{}
		if fullName == repoWidgets {
			list = append(list, pullBody())
		}
		writeJSON(w, http.StatusOK, list)
	}
}

func (f *fakeGitHub) pull(w http.ResponseWriter, r *http.Request) {
	fullName, ok := f.installationRepo(w, r)
	if !ok {
		return
	}
	if fullName != repoWidgets || r.PathValue("number") != strconv.Itoa(pullNumber) {
		refuse(w, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, pullBody())
}

func (f *fakeGitHub) compare(w http.ResponseWriter, r *http.Request) {
	if _, ok := f.installationRepo(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"merge_base_commit": map[string]string{"sha": pullMergeSHA}})
	}
}

func (f *fakeGitHub) run(w http.ResponseWriter, r *http.Request) {
	fullName, ok := f.installationRepo(w, r)
	if !ok {
		return
	}
	if fullName != repoWidgets || r.PathValue("id") != strconv.Itoa(runID) {
		refuse(w, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": runID, "run_attempt": 1, "event": "pull_request", "head_sha": pullHeadSHA, "path": workflowPath,
		"status": "completed", "conclusion": "success",
		"head_repository": map[string]string{"full_name": repoWidgets},
		"pull_requests":   []map[string]int{{"number": pullNumber}},
	})
}

func (f *fakeGitHub) listCheckRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := f.installationRepo(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
	}
}

func (f *fakeGitHub) createCheckRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := f.installationRepo(w, r); !ok {
		return
	}
	f.mu.Lock()
	f.checkRuns++
	id := f.checkRuns
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "app": map[string]int{"id": appID}})
}

// State changes and observations used by the tests.

func (f *fakeGitHub) setOverride(o func(http.ResponseWriter, *http.Request) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.override = o
}

// answer overrides every request to host whose path starts with prefix.
func answer(host, prefix string, status int, body string, headers ...string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Host != host || !strings.HasPrefix(r.URL.Path, prefix) {
			return false
		}
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return true
	}
}

// rateLimitAll is GitHub refusing the whole REST API for 10 minutes.
func rateLimitAll() func(http.ResponseWriter, *http.Request) bool {
	reset := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	return answer(hostAPI, "/", http.StatusForbidden, `{"message":"API rate limit exceeded"}`,
		"Retry-After", "600", "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", reset)
}

// rejectEveryToken answers 401 to every API request except token mints.
func rejectEveryToken(w http.ResponseWriter, r *http.Request) bool {
	if r.Host != hostAPI || strings.HasSuffix(r.URL.Path, "/access_tokens") {
		return false
	}
	refuse(w, http.StatusUnauthorized)
	return true
}

func (f *fakeGitHub) revokeTokens(installationID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for token, id := range f.tokens {
		if id == installationID {
			f.revoked[token] = true
		}
	}
}

func (f *fakeGitHub) removeRepo(installationID int64, fullName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs[installationID] = slices.DeleteFunc(slices.Clone(f.installs[installationID]),
		func(r string) bool { return r == fullName })
}

func (f *fakeGitHub) deleteInstallation(installationID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.installs, installationID)
}

func (f *fakeGitHub) mintCount(installationID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mints[installationID]
}

func (f *fakeGitHub) checkRunCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkRuns
}

// mark returns the number of requests seen so far; since(mark) returns the ones after it.
func (f *fakeGitHub) mark() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeGitHub) since(mark int) []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests[mark:])
}

// find returns the one request since mark for method and path, failing the test otherwise.
func (f *fakeGitHub) find(t *testing.T, mark int, method, path string) request {
	t.Helper()
	var found []request
	for _, r := range f.since(mark) {
		if r.method == method && r.path == path {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s %s: got %d requests, want 1", method, path, len(found))
	}
	return found[0]
}

// reroute sends every host to the fake and keeps the original host for its router.
type reroute struct {
	addr string
	base http.RoundTripper
}

func (rt reroute) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Host = req.URL.Host
	clone.URL.Scheme = "http"
	clone.URL.Host = rt.addr
	return rt.base.RoundTrip(clone)
}

func (f *fakeGitHub) client(t *testing.T) *http.Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &http.Client{
		Transport: reroute{addr: srv.Listener.Addr().String(), base: srv.Client().Transport},
		Timeout:   10 * time.Second,
	}
}
