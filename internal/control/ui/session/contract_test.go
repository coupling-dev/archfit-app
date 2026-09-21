package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestLoginRedirectsWithStateCookie(t *testing.T) {
	f := newFixture(t)

	resp, c, _ := f.login(t, "/r/o/n")

	st, ok := f.s.openState(c.Value)
	if !ok {
		t.Fatalf("state cookie does not open")
	}
	if len(st.State) != 64 {
		t.Errorf("state has %d characters, want 64 hex", len(st.State))
	}
	if st.Return != "/r/o/n" {
		t.Errorf("return path = %q, want /r/o/n", st.Return)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", resp.StatusCode)
	}
	want := f.oauth.AuthorizeURL(st.State, testBaseURL+"/auth/callback")
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || !c.Secure || c.Path != "/" {
		t.Errorf("state cookie attributes = %+v, want HttpOnly, SameSite=Lax, Secure, Path=/", c)
	}
	if c.MaxAge != int(stateTTL/time.Second) {
		t.Errorf("state cookie Max-Age = %d, want %d", c.MaxAge, int(stateTTL/time.Second))
	}

	_, second, _ := f.login(t, "/r/o/n")
	if other, _ := f.s.openState(second.Value); other.State == st.State {
		t.Errorf("two logins share one state value")
	}

	t.Run("http on localhost is not Secure", func(t *testing.T) {
		s, err := New(Config{CookieKey: testKey, BaseURL: "http://localhost:8080/"}, f.oauth, f.users, f.repos, f.clock.now)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		resp := serve(s.LoginHandler(), requestWith(http.MethodGet, "/auth/login"))
		c := findCookie(resp, stateCookie)
		if c == nil || c.Secure || !c.HttpOnly {
			t.Fatalf("state cookie = %+v, want HttpOnly and not Secure", c)
		}
		st, _ := s.(*sessions).openState(c.Value)
		want := f.oauth.AuthorizeURL(st.State, "http://localhost:8080/auth/callback")
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	})
}

func TestCallbackCreatesSession(t *testing.T) {
	f := newFixture(t)
	_, stateC, state := f.login(t, "/r/o/n")

	resp := f.callback(state, testCode, stateC)

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/r/o/n" {
		t.Errorf("status %d to %q, want 303 to /r/o/n", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c := findCookie(resp, stateCookie); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("state cookie not deleted: %+v", c)
	}
	if got := f.oauth.exchanges(); !reflect.DeepEqual(got, []string{testCode}) {
		t.Errorf("Exchange codes = %v, want the callback code once", len(got))
	}
	c := findCookie(resp, sessionCookie)
	if c == nil {
		t.Fatalf("no session cookie")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || !c.Secure || c.Path != "/" {
		t.Errorf("session cookie attributes = %+v, want HttpOnly, SameSite=Lax, Secure, Path=/", c)
	}
	if c.MaxAge != int(sessionTTL/time.Second) {
		t.Errorf("session cookie Max-Age = %d, want %d", c.MaxAge, int(sessionTTL/time.Second))
	}

	u, ok := f.s.User(requestWith(http.MethodGet, "/r/o/n", c))
	if !ok || u != testUser {
		t.Errorf("User = id %d, %v; want the exchanged user", u.UserID, ok)
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	tests := []struct {
		name          string
		dropCookie    bool
		state         string
		advance       time.Duration
		code          string
		exchangeErr   error
		wantStatus    int
		wantExchanges int
	}{
		{name: "no state cookie", dropCookie: true, code: testCode, wantStatus: http.StatusBadRequest},
		{name: "different state", state: "deadbeef", code: testCode, wantStatus: http.StatusBadRequest},
		{name: "expired state cookie", advance: stateTTL + time.Second, code: testCode, wantStatus: http.StatusBadRequest},
		{name: "missing code", wantStatus: http.StatusBadRequest},
		{name: "exchange error", code: testCode, exchangeErr: errors.New("boom"), wantStatus: http.StatusBadGateway, wantExchanges: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.oauth.err = tt.exchangeErr
			_, stateC, state := f.login(t, "/r/o/n")
			if tt.dropCookie {
				stateC = nil
			}
			if tt.state != "" {
				state = tt.state
			}
			f.clock.advance(tt.advance)

			resp := f.callback(state, tt.code, stateC)

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if c := findCookie(resp, sessionCookie); c != nil {
				t.Errorf("session cookie set on a failed callback")
			}
			if got := len(f.oauth.exchanges()); got != tt.wantExchanges {
				t.Errorf("%d Exchange calls, want %d", got, tt.wantExchanges)
			}
		})
	}
}

func TestLogoutClearsSession(t *testing.T) {
	f := newFixture(t)
	c := f.signIn(t)

	resp := serve(f.s.LogoutHandler(), requestWith(http.MethodPost, "/auth/logout", c))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Errorf("POST: status %d to %q, want 303 to /", resp.StatusCode, resp.Header.Get("Location"))
	}
	if del := findCookie(resp, sessionCookie); del == nil || del.MaxAge >= 0 || del.Value != "" {
		t.Errorf("POST: session cookie not deleted: %+v", del)
	}

	resp = serve(f.s.LogoutHandler(), requestWith(http.MethodGet, "/auth/logout", c))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d, want 405", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("GET: cookies changed: %v", resp.Header["Set-Cookie"])
	}
	if body, _ := io.ReadAll(resp.Body); len(body) > 64 {
		t.Errorf("GET: unexpected body of %d bytes", len(body))
	}
}

func TestAuthorizeReturnsResolvedRepo(t *testing.T) {
	quietLogs(t)
	f := newFixture(t)

	repo, err := f.s.Authorize(context.Background(), testUser, testRepo, "write")

	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if repo != resolvedRepo(42) {
		t.Errorf("repo = %+v, want the resolved repo of installation 42", repo)
	}
	if got := f.users.lastCall(); got != (permissionCall{testToken, testRepo}) {
		t.Errorf("Permission called with repository %q and a token that is the user's: %v",
			got.fullName, got.token == testToken)
	}
}

func TestAuthorizeMinimumPermission(t *testing.T) {
	quietLogs(t)
	tests := []struct {
		have, min string
		allowed   bool
	}{
		{"read", "read", true},
		{"read", "write", false},
		{"read", "admin", false},
		{"write", "read", true},
		{"write", "write", true},
		{"write", "admin", false},
		{"admin", "read", true},
		{"admin", "write", true},
		{"admin", "admin", true},
		{"none", "read", false},
		{"triage", "read", false},
		{"admin", "none", false},
		{"admin", "owner", false},
	}
	for _, tt := range tests {
		t.Run(tt.have+" against "+tt.min, func(t *testing.T) {
			f := newFixture(t)
			f.users.answer(tt.have, nil)

			repo, err := f.s.Authorize(context.Background(), testUser, testRepo, tt.min)

			if tt.allowed {
				if err != nil || repo != resolvedRepo(42) {
					t.Errorf("Authorize = %+v, %v; want the repo", repo, err)
				}
				return
			}
			if !errors.Is(err, ErrForbidden) || repo != (api.Repo{}) {
				t.Errorf("Authorize = %+v, %v; want no repo and ErrForbidden", repo, err)
			}
		})
	}
}
