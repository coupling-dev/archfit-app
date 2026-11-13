package ui

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	baseURL    = "https://app.example"
	repoName   = "acme/app"
	otherRepo  = "rival/secret" // installation B
	mainBranch = "main"
	workflow   = ".github/workflows/archfit.yaml"

	installA = int64(1)
	installB = int64(2)

	// operatorLogin signs in with operatorID, the one operator of the env.
	operatorLogin = "ops"
	operatorID    = int64(9001)

	policyText = "version: 3\nowners: [alice]\nmodules:\n  a:\n    layer: core\n    paths: [\"pkg/a/**\"]\n  b:\n    layer: edge\n    paths: [\"pkg/b/**\"]\n"
)

var cookieKey = bytes.Repeat([]byte{7}, 32)

// testWeb stands in for the web app build: a shell, one hashed asset and the licence file.
var testWeb = fstest.MapFS{
	"index.html":               {Data: []byte(`<!doctype html><meta name="csp-nonce" content="__CSP_NONCE__"><meta name="app-error" content="__APP_ERROR__"><title>Archfit</title><div id="app"></div><script type="module" src="/assets/app-1a2b.js"></script>`)},
	"assets/app-1a2b.js":       {Data: []byte(`console.log("archfit")`)},
	"assets/licenses-9f8e.txt": {Data: []byte("Third-party software in the Archfit web app.\n")},
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// env is ui behind the real session and graph children, with every sibling and GitHub faked by hand.
type env struct {
	t        *testing.T
	gh       *fakeGH
	acct     *fakeAccount
	consents *fakeConsents
	passkeys *fakePasskeys
	policy   *fakePolicyViews
	requests *fakeRequestViews
	answers  *fakeAnswers
	owners   *fakeOwners
	setups   *fakeSetups
	oauth    *fakeOAuth
	store    store.Store
	admit    *gate.Admissions // acme is approved by configuration; any other account by a Store record
	// readsDown makes every approval read fail, as a Firestore outage does.
	readsDown *atomic.Bool
	now       time.Time
	handler   http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gh := newFakeGH()
	mem := store.NewMemory()
	realAccount, _ := account.New(mem)
	reads := &downable{Store: mem}
	e := &env{
		t: t, gh: gh, store: mem, readsDown: &reads.down,
		admit:    gate.NewAdmissions(reads, []string{"acme", "rival"}),
		acct:     &fakeAccount{Account: realAccount, status: account.Status{Plan: account.PlanPublic}},
		consents: newFakeConsents(),
		passkeys: &fakePasskeys{}, policy: &fakePolicyViews{}, requests: &fakeRequestViews{}, answers: &fakeAnswers{},
		owners: &fakeOwners{}, setups: &fakeSetups{}, oauth: &fakeOAuth{tokens: map[string]auth.UserToken{}},
		now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
	}
	clock := func() time.Time { return e.now }
	sessions, err := session.New(session.Config{CookieKey: cookieKey, BaseURL: baseURL}, e.oauth, gh, gh, clock)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	e.handler = newHandler(Deps{
		Account: e.acct, Consents: e.consents,
		Admissions: e.admit, PullViews: gate.NewPullViews(gh, mem), PolicyViews: e.policy, RequestViews: e.requests, Answers: e.answers, Owners: e.owners, Setups: e.setups, Passkeys: e.passkeys, Operators: []int64{operatorID}, BaseURL: baseURL, Now: clock,
	}, github{files: gh, checks: gh, pulls: gh, users: gh, installs: gh}, sessions, testWeb)

	e.addRepo(repoName, installA, 10)
	e.addRepo(otherRepo, installB, 20)
	return e
}

// addRepo installs a repository with a policy and settings on its default branch.
func (e *env) addRepo(fullName string, installationID, id int64) {
	e.gh.repos[fullName] = api.Repo{
		RepoRef: api.RepoRef{InstallationID: installationID, FullName: fullName}, ID: id, Private: true, DefaultBranch: mainBranch,
	}
	e.gh.heads[fullName+"@"+mainBranch] = sha(int(id))
	e.gh.pulls[fullName+"#4"] = api.Pull{
		Number: 4, Title: "feat: read options", Author: "alexei", State: "open", URL: "https://github.com/" + fullName + "/pull/4",
		HeadSHA: sha(4), HeadRef: "feature", BaseRef: mainBranch, BaseSHA: sha(int(id)),
	}
	e.gh.setFile(fullName, mainBranch, ".archfit.yaml", policyText)
}

// grant gives the login a permission on a repository, as GitHub reports it.
func (e *env) grant(login, fullName, permission string) {
	token := e.user(login).AccessToken
	e.gh.mu.Lock()
	defer e.gh.mu.Unlock()
	e.gh.perms[token+" "+fullName] = permission
	for _, name := range e.gh.listed[token] {
		if name == fullName {
			return
		}
	}
	e.gh.listed[token] = append(e.gh.listed[token], fullName)
}

func (e *env) user(login string) auth.UserToken {
	code := "code-" + login
	if _, ok := e.oauth.tokens[code]; !ok {
		id := int64(100 + len(e.oauth.tokens))
		if login == operatorLogin {
			id = operatorID
		}
		e.oauth.tokens[code] = auth.UserToken{AccessToken: "tok-" + login, Login: login, UserID: id}
	}
	return e.oauth.tokens[code]
}

// client is a browser: it keeps cookies and knows the CSRF token of its session.
type client struct {
	e       *env
	cookies map[string]string
	token   string // OAuth token of the signed-in user, "" when signed out
}

func (e *env) anonymous() *client { return &client{e: e, cookies: map[string]string{}} }

// signIn runs the real OAuth flow of the session child.
func (e *env) signIn(login string) *client {
	e.t.Helper()
	c := e.anonymous()
	res := c.get("/auth/login")
	loc, err := url.Parse(res.Header().Get("Location"))
	if err != nil || res.Code != http.StatusSeeOther {
		e.t.Fatalf("login: status %d, location %q", res.Code, res.Header().Get("Location"))
	}
	e.user(login)
	res = c.get("/auth/callback?code=code-" + login + "&state=" + loc.Query().Get("state"))
	if res.Code != http.StatusSeeOther {
		e.t.Fatalf("callback: status %d: %s", res.Code, res.Body.String())
	}
	c.token = e.user(login).AccessToken
	return c
}

// as signs the login in with a permission on the default test repository.
func (e *env) as(login, permission string) *client {
	e.grant(login, repoName, permission)
	return e.signIn(login)
}

func (c *client) csrf() string { return csrfToken(session.User{Token: c.token}) }

func (c *client) do(req *http.Request) *httptest.ResponseRecorder {
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	c.e.handler.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
			continue
		}
		c.cookies[ck.Name] = ck.Value
	}
	return rec
}

func (c *client) get(path string) *httptest.ResponseRecorder {
	return c.do(httptest.NewRequest(http.MethodGet, baseURL+path, nil))
}

// post sends the form with the session's CSRF token unless the form names its own.
func (c *client) post(path string, form url.Values) *httptest.ResponseRecorder {
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("csrf") {
		form.Set("csrf", c.csrf())
	}
	req := httptest.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// postJSON sends body as JSON with the session's CSRF token in the header, as the web app does.
func (c *client) postJSON(path string, body any) *httptest.ResponseRecorder {
	return c.postJSONWith(path, body, c.csrf())
}

func (c *client) postJSONWith(path string, body any, csrf string) *httptest.ResponseRecorder {
	raw, err := json.Marshal(body)
	if err != nil {
		c.e.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set(csrfHeader, csrf)
	}
	return c.do(req)
}

// getJSON reads an /api endpoint into out and fails the test unless it answers 200.
func (c *client) getJSON(path string, out any) {
	c.e.t.Helper()
	res := c.get(path)
	wantStatus(c.e.t, res, http.StatusOK)
	if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
		c.e.t.Fatalf("decode %s: %v", path, err)
	}
}

// decodeAs reads a JSON answer, whatever its status.
func decodeAs[T any](t *testing.T, res *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %.200s: %v", res.Body.String(), err)
	}
	return out
}

// captureLogs swaps the default logger; tests that use it do not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func wantStatus(t *testing.T, res *httptest.ResponseRecorder, want int) {
	t.Helper()
	if res.Code != want {
		t.Fatalf("status = %d, want %d; body: %.300s", res.Code, want, res.Body.String())
	}
}

func wantContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}
