package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestAuthorizeFailsClosedOnGitHubErrors(t *testing.T) {
	quietLogs(t)
	errTransport := errors.New("dial tcp: connection refused")
	tests := []struct {
		name          string
		permissionErr error
		resolveErr    error
		wantForbidden bool
		wantWrapped   error
	}{
		{name: "Permission not found", permissionErr: api.ErrNotFound, wantForbidden: true},
		{name: "Permission rate limited", permissionErr: api.ErrRateLimited, wantWrapped: api.ErrRateLimited},
		{name: "Permission transport error", permissionErr: errTransport, wantWrapped: errTransport},
		{name: "ResolveRepo not found", resolveErr: fmt.Errorf("resolve: %w", api.ErrNotFound), wantForbidden: true},
		{name: "ResolveRepo rate limited", resolveErr: api.ErrRateLimited, wantWrapped: api.ErrRateLimited},
		{name: "ResolveRepo transport error", resolveErr: errTransport, wantWrapped: errTransport},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.users.answer("admin", tt.permissionErr)
			f.repos.answer(api.Repo{}, tt.resolveErr)

			repo, err := f.s.Authorize(context.Background(), testUser, testRepo, "read")

			if repo != (api.Repo{}) {
				t.Errorf("repo = %+v, want none", repo)
			}
			if errors.Is(err, ErrForbidden) != tt.wantForbidden {
				t.Errorf("err = %v, want ErrForbidden: %v", err, tt.wantForbidden)
			}
			if tt.wantWrapped != nil && (!errors.Is(err, tt.wantWrapped) || err == tt.wantWrapped) {
				t.Errorf("err = %v, want %v wrapped", err, tt.wantWrapped)
			}
			if n := f.s.cacheLen(); n != 0 {
				t.Errorf("cache holds %d entries after an error, want 0", n)
			}

			// Nothing was cached: once GitHub recovers the next call asks again and succeeds.
			f.users.answer("admin", nil)
			f.repos.answer(resolvedRepo(42), nil)
			repo, err = f.s.Authorize(context.Background(), testUser, testRepo, "read")
			if err != nil || repo != resolvedRepo(42) {
				t.Errorf("after recovery: Authorize = %+v, %v; want the repo", repo, err)
			}
			if got := f.users.callCount(); got != 2 {
				t.Errorf("%d Permission calls, want 2", got)
			}
		})
	}
}

func TestAuthorizeRejectsMalformedFullName(t *testing.T) {
	quietLogs(t)
	names := []string{
		"", "owner", "a/b/c", "../x", "o/..", "o/.", "/n", "o/", "o /n", "o/n n", "o/n?x=1", "o/n#f", "o/n\n", `o\n`,
		"o/" + strings.Repeat("n", 101),
	}
	for _, name := range names {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			f := newFixture(t)

			repo, err := f.s.Authorize(context.Background(), testUser, name, "read")

			if !errors.Is(err, ErrForbidden) || repo != (api.Repo{}) {
				t.Errorf("Authorize = %+v, %v; want no repo and ErrForbidden", repo, err)
			}
			if f.users.callCount() != 0 || f.repos.callCount() != 0 {
				t.Errorf("GitHub was called for a malformed name")
			}
		})
	}

	t.Run("user without a token", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.s.Authorize(context.Background(), User{Login: "octocat", UserID: 7}, testRepo, "read")
		if !errors.Is(err, ErrForbidden) || f.users.callCount() != 0 {
			t.Errorf("err = %v with %d GitHub calls; want ErrForbidden and none", err, f.users.callCount())
		}
	})
}

func TestSecretsNeverLeave(t *testing.T) {
	logs := captureLogs(t)
	f := newFixture(t)
	ctx := context.Background()

	var outputs []string
	record := func(resp *http.Response, withLocation bool) {
		t.Helper()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		outputs = append(outputs, string(body), strings.Join(resp.Header["Set-Cookie"], "\n"))
		if withLocation {
			outputs = append(outputs, resp.Header.Get("Location"))
		}
	}
	recordErr := func(err error) {
		if err != nil {
			outputs = append(outputs, err.Error())
		}
	}

	// The login redirect is the one place the state may appear.
	loginResp, stateC, state := f.login(t, "/r/o/n")
	record(loginResp, false)
	if strings.Contains(loginResp.Header.Get("Location"), testBaseURL+"/auth/callback?") {
		t.Errorf("login redirect carries a query on the callback URL")
	}

	record(f.callback(state, testCode, stateC), true)
	record(f.callback(state, "", stateC), true)
	record(f.callback(state, testCode, nil), true)
	record(f.callback("another-"+state, testCode, stateC), true)
	f.oauth.err = fmt.Errorf("exchange of code %s for state %s failed", testCode, state)
	record(f.callback(state, testCode, stateC), true)
	f.clock.advance(stateTTL)
	record(f.callback(state, testCode, stateC), true)

	_, err := f.s.Authorize(ctx, testUser, testRepo, "write")
	recordErr(err)
	_, err = f.s.Authorize(ctx, testUser, testRepo, "admin")
	recordErr(err)
	_, err = f.s.Authorize(ctx, testUser, testToken, "read")
	recordErr(err)
	f.users.answer("", api.ErrRateLimited)
	_, err = f.s.Authorize(ctx, testUser, "o/limited", "read")
	recordErr(err)
	f.users.answer("", api.ErrNotFound)
	_, err = f.s.Authorize(ctx, testUser, "o/unknown", "read")
	recordErr(err)
	f.users.answer("admin", nil)
	f.repos.answer(api.Repo{}, errors.New("dial tcp: connection refused"))
	_, err = f.s.Authorize(ctx, testUser, "o/unreachable", "read")
	recordErr(err)

	sealed, err := f.s.sealSession(testUser)
	if err != nil {
		t.Fatalf("sealSession: %v", err)
	}
	r := requestWith(http.MethodPost, "/auth/logout?code="+testCode, &http.Cookie{Name: sessionCookie, Value: sealed})
	record(serve(f.s.LogoutHandler(), r), true)

	if logs.String() == "" {
		t.Fatalf("nothing was logged; the capture does not work")
	}
	outputs = append(outputs, logs.String())
	for _, out := range outputs {
		for name, secret := range map[string]string{"token": testToken, "code": testCode, "state": state} {
			if strings.Contains(out, secret) {
				t.Errorf("the %s leaked into: %s", name, strings.ReplaceAll(out, secret, "<"+name+">"))
			}
		}
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name      string
		key       []byte
		baseURL   string
		wantField string
	}{
		{name: "31-byte key", key: bytes.Repeat([]byte("k"), 31), baseURL: testBaseURL, wantField: "CookieKey"},
		{name: "33-byte key", key: bytes.Repeat([]byte("k"), 33), baseURL: testBaseURL, wantField: "CookieKey"},
		{name: "nil key", key: nil, baseURL: testBaseURL, wantField: "CookieKey"},
		{name: "relative BaseURL", key: testKey, baseURL: "/relative-app", wantField: "BaseURL"},
		{name: "http on a public host", key: testKey, baseURL: "http://app.example", wantField: "BaseURL"},
		{name: "ftp", key: testKey, baseURL: "ftp://x-files.example", wantField: "BaseURL"},
		{name: "empty BaseURL", key: testKey, baseURL: "", wantField: "BaseURL"},
		{name: "unparsable BaseURL", key: testKey, baseURL: "https://app.example/%zz", wantField: "BaseURL"},
		{name: "https without a host", key: testKey, baseURL: "https:///only-path", wantField: "BaseURL"},
		{name: "https", key: testKey, baseURL: "https://app.example"},
		{name: "localhost", key: testKey, baseURL: "http://localhost:8080"},
		{name: "IPv4 loopback", key: testKey, baseURL: "http://127.0.0.1:8080"},
		{name: "IPv6 loopback", key: testKey, baseURL: "http://[::1]:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(Config{CookieKey: tt.key, BaseURL: tt.baseURL}, f.oauth, f.users, f.repos, f.clock.now)

			if tt.wantField == "" {
				if err != nil || s == nil {
					t.Errorf("New = %v, %v; want Sessions and nil", s, err)
				}
				return
			}
			if err == nil || s != nil {
				t.Fatalf("New = %v, %v; want nil and an error", s, err)
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Errorf("error %q does not name %s", err, tt.wantField)
			}
			for _, value := range []string{string(tt.key), tt.baseURL} {
				if value != "" && strings.Contains(err.Error(), value) {
					t.Errorf("error %q carries the value of the field", err)
				}
			}
		})
	}

	t.Run("missing dependency", func(t *testing.T) {
		if s, err := New(Config{CookieKey: testKey, BaseURL: testBaseURL}, f.oauth, f.users, f.repos, nil); err == nil || s != nil {
			t.Errorf("New with a nil clock = %v, %v; want nil and an error", s, err)
		}
	})
}
