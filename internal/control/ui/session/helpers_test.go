package session

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
)

const (
	testBaseURL = "https://app.example"
	testToken   = "gho_RECOGNIZABLE_TOKEN_5f3a"
	testCode    = "RECOGNIZABLE_CODE_91c2"
	testRepo    = "o/n"
)

var (
	testKey  = bytes.Repeat([]byte("k"), 32)
	testUser = User{Login: "octocat", UserID: 7, Token: testToken}
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	s     *sessions
	oauth *fakeOAuth
	users *fakeUsers
	repos *fakeRepos
	clock *clock
}

func resolvedRepo(installationID int64) api.Repo {
	return api.Repo{
		RepoRef:       api.RepoRef{InstallationID: installationID, FullName: testRepo},
		ID:            1001,
		DefaultBranch: "main",
	}
}

// newFixture signs octocat in through the fake OAuth flow, gives them "write" on every
// repository, and resolves every repository to installation 42.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithKey(t, testKey)
}

func newFixtureWithKey(t *testing.T, key []byte) *fixture {
	t.Helper()
	f := &fixture{
		oauth: &fakeOAuth{tok: auth.UserToken{AccessToken: testToken, Login: testUser.Login, UserID: testUser.UserID}},
		users: &fakeUsers{t: t, perm: "write"},
		repos: &fakeRepos{t: t, repo: resolvedRepo(42)},
		clock: &clock{t: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)},
	}
	s, err := New(Config{CookieKey: key, BaseURL: testBaseURL}, f.oauth, f.users, f.repos, f.clock.now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.s = s.(*sessions)
	return f
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs swaps the default logger; tests that use it do not run in parallel.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func quietLogs(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func findCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func serve(h http.Handler, r *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Result()
}

// login runs GET /auth/login and returns the response, the state cookie and the state value
// GitHub would echo back.
func (f *fixture) login(t *testing.T, returnPath string) (*http.Response, *http.Cookie, string) {
	t.Helper()
	target := "/auth/login"
	if returnPath != "" {
		target += "?return=" + url.QueryEscape(returnPath)
	}
	resp := serve(f.s.LoginHandler(), httptest.NewRequest(http.MethodGet, target, nil))
	c := findCookie(resp, stateCookie)
	if c == nil {
		t.Fatalf("login set no %s cookie", stateCookie)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("login Location: %v", err)
	}
	return resp, c, loc.Query().Get("state")
}

func (f *fixture) callback(state, code string, stateC *http.Cookie) *http.Response {
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if code != "" {
		q.Set("code", code)
	}
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?"+q.Encode(), nil)
	if stateC != nil {
		r.AddCookie(stateC)
	}
	return serve(f.s.CallbackHandler(), r)
}

// signIn runs the whole flow and returns the session cookie.
func (f *fixture) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	_, stateC, state := f.login(t, "")
	resp := f.callback(state, testCode, stateC)
	c := findCookie(resp, sessionCookie)
	if resp.StatusCode != http.StatusSeeOther || c == nil || c.MaxAge < 0 {
		t.Fatalf("sign-in failed: status %d, session cookie %v", resp.StatusCode, c)
	}
	return c
}

func requestWith(method, target string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func (s *sessions) cacheLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cache)
}
