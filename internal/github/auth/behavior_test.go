package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLongRunningProcessKeepsWorkingTokens(t *testing.T) {
	clock := newFakeClock()
	iss := newIssuer(clock)
	gh := newFakeGitHub(t, iss.handler)
	s := newTestSource(t, clock, gh.client())

	for elapsed := time.Duration(0); elapsed <= 3*time.Hour; elapsed += 5 * time.Minute {
		for _, id := range []int64{1, 2} {
			token := mustToken(t, s, id)
			exp, ok := iss.expiry(token)
			if !ok {
				t.Fatalf("token %q was never issued", token)
			}
			if left := exp.Sub(clock.now()); left <= 5*time.Minute {
				t.Errorf("at %v installation %d got a token with %v left", elapsed, id, left)
			}
		}
		clock.advance(5 * time.Minute)
	}

	// 3 h of 60-minute tokens renewed at 55 min: mints at 0, 55, 110 and 165 min.
	for _, id := range []int64{1, 2} {
		if n := gh.countPath(mintPath(id)); n != 4 {
			t.Errorf("installation %d mints = %d, want 4", id, n)
		}
	}
}

func TestUserLoginRoundTrip(t *testing.T) {
	const state, redirect, code = "st&te=1 ✓", "https://app.example/auth/callback", "code-from-github"
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tokenPath {
			if err := r.ParseForm(); err != nil || r.PostForm.Get("code") != code {
				writeJSON(w, http.StatusOK, map[string]string{"error": "bad_verification_code"})
				return
			}
		}
		oauthHandler("ghu_user", map[string]any{"login": "octocat", "id": 583231})(w, r)
	})

	_, login, err := New(validCredentials(), gh.client())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(login.AuthorizeURL(state, redirect))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("state"); got != state {
		t.Errorf("state = %q, want %q", got, state)
	}
	if got := u.Query().Get("redirect_uri"); got != redirect {
		t.Errorf("redirect_uri = %q, want %q", got, redirect)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("AuthorizeURL made %d requests", n)
	}

	// A second instance finishes the login: nothing from AuthorizeURL is needed by Exchange.
	_, callback, err := New(validCredentials(), gh.client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := callback.Exchange(t.Context(), code)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got.Login != "octocat" || got.UserID != 583231 || got.AccessToken != "ghu_user" {
		t.Errorf("UserToken = %+v", got)
	}
}

func TestInvalidateAfter401Recovers(t *testing.T) {
	clock := newFakeClock()
	gh := newFakeGitHub(t, newIssuer(clock).handler)
	s := newTestSource(t, clock, gh.client())

	t1 := mustToken(t, s, 9)
	clock.advance(10 * time.Minute)
	if got := mustToken(t, s, 9); got != t1 {
		t.Fatalf("T1 is not served from cache")
	}

	// github/api got a 401 with T1.
	s.Invalidate(9)
	t2 := mustToken(t, s, 9)
	if t2 == t1 {
		t.Fatal("T1 returned after Invalidate")
	}
	if n := gh.count(); n != 2 {
		t.Errorf("mints = %d, want 2", n)
	}
	for range 12 {
		clock.advance(5 * time.Minute)
		if got := mustToken(t, s, 9); got == t1 {
			t.Fatal("T1 returned again")
		}
	}
}

func TestSandboxGetsOnlyARepoReadToken(t *testing.T) {
	clock := newFakeClock()
	iss := newIssuer(clock)
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength == 0 {
			iss.handler(w, r)
			return
		}
		exactRepoScope("ghs_narrow", "o/r")(w, r)
	})
	s := newTestSource(t, clock, gh.client())

	full := mustToken(t, s, 9)
	narrow, err := s.RepoReadToken(t.Context(), 9, "o/r")
	if err != nil {
		t.Fatalf("RepoReadToken: %v", err)
	}
	if narrow != "ghs_narrow" || narrow == full {
		t.Errorf("RepoReadToken = %q, full token %q", narrow, full)
	}

	reqs := gh.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(reqs[1].Body, &body); err != nil {
		t.Fatalf("body %q: %v", reqs[1].Body, err)
	}
	if len(body.Repositories) != 1 || body.Repositories[0] != "r" {
		t.Errorf("repositories = %v, want [r]", body.Repositories)
	}
	if len(body.Permissions) != 1 || body.Permissions["contents"] != "read" {
		t.Errorf("permissions = %v, want contents:read only", body.Permissions)
	}

	if got := mustToken(t, s, 9); got != full {
		t.Errorf("installation token = %q, want the cached %q", got, full)
	}
	if n := gh.count(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestUninstallStopsNewWork(t *testing.T) {
	clock := newFakeClock()
	iss := newIssuer(clock)
	var uninstalled atomic.Bool
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if uninstalled.Load() && r.URL.Path == mintPath(9) {
			writeRaw(w, http.StatusNotFound, `{"message":"Not Found"}`)
			return
		}
		iss.handler(w, r)
	})
	s := newTestSource(t, clock, gh.client())

	old := mustToken(t, s, 9)
	mustToken(t, s, 10)
	uninstalled.Store(true)
	clock.advance(56 * time.Minute)

	for call := range 3 {
		token, err := s.InstallationToken(t.Context(), 9)
		if err == nil || token != "" {
			t.Fatalf("call %d for the deleted installation = %q, %v", call, token, err)
		}
		if !strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), old) {
			t.Errorf("error = %q", err)
		}
		clock.advance(time.Minute)
	}
	if n := gh.countPath(mintPath(9)); n != 4 {
		t.Errorf("requests for 9 = %d, want 4 (no failure cached)", n)
	}
	if token := mustToken(t, s, 10); !strings.Contains(token, "-10-") {
		t.Errorf("installation 10 token = %q", token)
	}
}
