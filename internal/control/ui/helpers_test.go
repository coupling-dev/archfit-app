package ui

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	baseURL    = "https://app.example"
	repoName   = "acme/app"
	otherRepo  = "rival/secret" // installation B
	mainBranch = "main"
	workflow   = ".github/workflows/archfit.yaml"

	installA = int64(1)
	installB = int64(2)

	policyText = "version: 2\nmodules:\n  a:\n    layer: core\n    paths: [\"pkg/a/**\"]\n  b:\n    layer: edge\n    paths: [\"pkg/b/**\"]\n"

	settingsText = "version: 1\nowners: [alice]\ngate: advisory\nworkflow: " + workflow + "\ndata_mode: managed\n"

	// config_hash of testdata/healthy.json, replaced by the digest of the policy a test uses.
	fixtureConfigHash = "4a18013bab44d922fa67d9e8e699463b2b5284f1c2908b62d2b7c5f607c55574"
)

var cookieKey = bytes.Repeat([]byte{7}, 32)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// env is ui behind the real session and graph children, with every sibling and GitHub faked by hand.
type env struct {
	t        *testing.T
	gh       *fakeGH
	props    *fakeProposals
	acct     *fakeAccount
	consents *fakeConsents
	onb      *fakeOnboarding
	assist   *fakeAssist
	oauth    *fakeOAuth
	now      time.Time
	handler  http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gh := newFakeGH()
	e := &env{
		t: t, gh: gh, props: newFakeProposals(gh), acct: &fakeAccount{status: account.Status{Plan: account.PlanPublic}},
		consents: newFakeConsents(), onb: &fakeOnboarding{progress: onboarding.Progress{Stage: onboarding.StagePolicyMerged}},
		assist: &fakeAssist{}, oauth: &fakeOAuth{tokens: map[string]auth.UserToken{}},
		now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
	}
	clock := func() time.Time { return e.now }
	sessions, err := session.New(session.Config{CookieKey: cookieKey, BaseURL: baseURL}, e.oauth, gh, gh, clock)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	e.handler = newHandler(Deps{
		Account: e.acct, Consents: e.consents, Onboarding: e.onb, Assist: e.assist, Proposals: e.props, Now: clock,
	}, github{files: gh, checks: gh, actions: gh, pulls: gh, users: gh}, sessions)

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
		HeadSHA: sha(4), HeadRef: "feature", BaseRef: mainBranch,
	}
	e.gh.setFile(fullName, mainBranch, policy.PolicyPath, policyText)
	e.gh.setFile(fullName, mainBranch, settings.Path, settingsText)
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
		e.oauth.tokens[code] = auth.UserToken{AccessToken: "tok-" + login, Login: login, UserID: int64(100 + len(e.oauth.tokens))}
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

func formOf(branch, headSHA, content string) url.Values {
	return url.Values{"branch": {branch}, "head_sha": {headSHA}, "content": {content}}
}

// artifact is an engine state fixture ("healthy" or "real-blocked-no-baseline") whose config_hash matches policyText.
func artifact(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(fixtureConfigHash)) {
		t.Fatal("fixture config_hash changed")
	}
	return bytes.ReplaceAll(raw, []byte(fixtureConfigHash), []byte(policy.Digest([]byte(policyText))))
}

// publishReport makes payload the archfit-report artifact of the latest default-branch push run.
func (e *env) publishReport(fullName string, payload []byte) {
	e.gh.addRun(fullName, workflow, mainBranch, api.WorkflowRun{ID: 77, Event: "push", Status: "completed", Conclusion: "success"})
	e.gh.mu.Lock()
	defer e.gh.mu.Unlock()
	e.gh.artifacts[77] = payload
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
