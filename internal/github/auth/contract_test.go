package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstallationTokenRequestShape(t *testing.T) {
	clock := newFakeClock()
	gh := newFakeGitHub(t, newIssuer(clock).handler)
	s := newTestSource(t, clock, gh.client())

	mustToken(t, s, 77)

	reqs := gh.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Host != apiHost || r.Path != "/app/installations/77/access_tokens" {
		t.Errorf("request = %s %s%s", r.Method, r.Host, r.Path)
	}
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		t.Fatalf("Authorization is not a bearer header")
	}
	_, claims := verifyJWT(t, bearer, &testKey().PublicKey)
	if claims["iss"] != "12345" {
		t.Errorf("iss = %v", claims["iss"])
	}
	if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Errorf("Accept = %q", got)
	}
	if got := r.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q", got)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %q, want empty", r.Body)
	}
}

func TestInvalidate(t *testing.T) {
	t.Run("cached installation", func(t *testing.T) {
		clock := newFakeClock()
		gh := newFakeGitHub(t, newIssuer(clock).handler)
		s := newTestSource(t, clock, gh.client())
		first := mustToken(t, s, 77)
		s.Invalidate(77)
		if second := mustToken(t, s, 77); second == first {
			t.Error("invalidated token returned again")
		}
		if n := gh.count(); n != 2 {
			t.Errorf("requests = %d, want 2", n)
		}
	})

	for _, id := range []int64{4242, 0, -1} {
		t.Run("no-op", func(t *testing.T) {
			clock := newFakeClock()
			gh := newFakeGitHub(t, newIssuer(clock).handler)
			s := newTestSource(t, clock, gh.client())
			cached := mustToken(t, s, 77)
			s.Invalidate(id)
			if got := mustToken(t, s, 77); got != cached {
				t.Errorf("Invalidate(%d) dropped installation 77", id)
			}
			if n := gh.count(); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	}

	t.Run("other installation stays cached", func(t *testing.T) {
		clock := newFakeClock()
		gh := newFakeGitHub(t, newIssuer(clock).handler)
		s := newTestSource(t, clock, gh.client())
		mustToken(t, s, 77)
		cached78 := mustToken(t, s, 78)
		s.Invalidate(77)
		if got := mustToken(t, s, 78); got != cached78 {
			t.Errorf("78 = %q, want cached %q", got, cached78)
		}
		if n := gh.countPath(mintPath(78)); n != 1 {
			t.Errorf("mints for 78 = %d, want 1", n)
		}
	})

	t.Run("during an in-flight mint", func(t *testing.T) {
		clock := newFakeClock()
		iss := newIssuer(clock)
		started, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() {
				close(started)
				<-release
			})
			iss.handler(w, r)
		})
		s := newTestSource(t, clock, gh.client())

		type result struct {
			token string
			err   error
		}
		done := make(chan result, 1)
		go func() {
			token, err := s.InstallationToken(t.Context(), 77)
			done <- result{token, err}
		}()
		<-started
		s.Invalidate(77)
		close(release)
		inFlight := <-done
		if inFlight.err != nil {
			t.Fatalf("in-flight mint: %v", inFlight.err)
		}
		if got := mustToken(t, s, 77); got != inFlight.token {
			t.Errorf("token = %q, want the in-flight token %q from cache", got, inFlight.token)
		}
		if n := gh.count(); n != 1 {
			t.Errorf("requests = %d, want 1", n)
		}
	})
}

func TestRepoReadTokenRequestShape(t *testing.T) {
	gh := newFakeGitHub(t, exactRepoScope("ghs_repo", "octo/repo"))
	s := newTestSource(t, newFakeClock(), gh.client())

	for range 2 {
		token, err := s.RepoReadToken(t.Context(), 77, "octo/repo")
		if err != nil || token != "ghs_repo" {
			t.Fatalf("RepoReadToken = %q, %v", token, err)
		}
	}

	reqs := gh.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (no cache)", len(reqs))
	}
	for _, r := range reqs {
		if r.Method != http.MethodPost || r.Host != apiHost || r.Path != "/app/installations/77/access_tokens" {
			t.Errorf("request = %s %s%s", r.Method, r.Host, r.Path)
		}
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		verifyJWT(t, bearer, &testKey().PublicKey)
		var body map[string]any
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatalf("body %q: %v", r.Body, err)
		}
		want := map[string]any{
			"repositories": []any{"repo"},
			"permissions":  map[string]any{"contents": "read"},
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body = %v, want %v", body, want)
		}
	}
}

func exactRepoScope(token, fullName string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":        token,
			"expires_at":   testEpoch.Add(tokenTTL).Format(time.RFC3339),
			"permissions":  map[string]string{"contents": "read", "metadata": "read"},
			"repositories": []map[string]any{{"id": 1, "full_name": fullName}},
		})
	}
}

func TestConcurrentMintsAreDeduplicated(t *testing.T) {
	clock := newFakeClock()
	iss := newIssuer(clock)
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		iss.handler(w, r)
	})
	s := newTestSource(t, clock, gh.client())

	var mu sync.Mutex
	tokens := map[int64]map[string]bool{77: {}, 78: {}}
	var wg sync.WaitGroup
	for id, callers := range map[int64]int{77: 50, 78: 10} {
		for range callers {
			wg.Go(func() {
				token, err := s.InstallationToken(t.Context(), id)
				if err != nil {
					t.Errorf("InstallationToken(%d): %v", id, err)
					return
				}
				mu.Lock()
				tokens[id][token] = true
				mu.Unlock()
			})
		}
	}
	wg.Wait()

	for id, got := range tokens {
		if len(got) != 1 {
			t.Errorf("installation %d callers saw %d distinct tokens, want 1", id, len(got))
		}
		if n := gh.countPath(mintPath(id)); n != 1 {
			t.Errorf("installation %d requests = %d, want 1", id, n)
		}
	}
}

func oauthHandler(accessToken string, user any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Host + r.URL.Path {
		case webHost + tokenPath:
			writeJSON(w, http.StatusOK, map[string]any{"access_token": accessToken, "token_type": "bearer", "scope": ""})
		case apiHost + userPath:
			writeJSON(w, http.StatusOK, user)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestExchangeReturnsIdentifiedUser(t *testing.T) {
	gh := newFakeGitHub(t, oauthHandler("ghu_user", map[string]any{"login": "octocat", "id": 583231, "name": "ignored"}))
	_, oa, err := New(validCredentials(), gh.client())
	if err != nil {
		t.Fatal(err)
	}

	got, err := oa.Exchange(t.Context(), "the-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if want := (UserToken{AccessToken: "ghu_user", Login: "octocat", UserID: 583231}); got != want {
		t.Errorf("UserToken = %+v, want %+v", got, want)
	}

	reqs := gh.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	exchange, user := reqs[0], reqs[1]
	if exchange.Method != http.MethodPost || exchange.Host != webHost || exchange.Path != tokenPath {
		t.Errorf("exchange = %s %s%s", exchange.Method, exchange.Host, exchange.Path)
	}
	if got := exchange.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("exchange Content-Type = %q", got)
	}
	if got := exchange.Header.Get("Accept"); got != "application/json" {
		t.Errorf("exchange Accept = %q", got)
	}
	form, err := url.ParseQuery(string(exchange.Body))
	if err != nil {
		t.Fatalf("exchange body: %v", err)
	}
	wantForm := url.Values{"client_id": {"client-id"}, "client_secret": {"client-secret"}, "code": {"the-code"}}
	if !reflect.DeepEqual(form, wantForm) {
		t.Errorf("exchange form = %v, want %v", form, wantForm)
	}
	if user.Method != http.MethodGet || user.Host != apiHost || user.Path != userPath {
		t.Errorf("user = %s %s%s", user.Method, user.Host, user.Path)
	}
	if got := user.Header.Get("Authorization"); got != "Bearer ghu_user" {
		t.Errorf("user Authorization = %q", got)
	}
}

func TestNewValidatesCredentials(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	tests := []struct {
		name   string
		mutate func(*Credentials)
		client *http.Client
		wantOK bool
	}{
		{"valid", func(*Credentials) {}, gh.client(), true},
		{"AppID 0", func(c *Credentials) { c.AppID = 0 }, gh.client(), false},
		{"bad key", func(c *Credentials) { c.PrivateKeyPEM = []byte("bad") }, gh.client(), false},
		{"empty client ID", func(c *Credentials) { c.OAuthClientID = "" }, gh.client(), false},
		{"empty client secret", func(c *Credentials) { c.OAuthClientSecret = "" }, gh.client(), false},
		{"nil client", func(*Credentials) {}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCredentials()
			tt.mutate(&c)
			ts, oa, err := New(c, tt.client)
			if (err == nil) != tt.wantOK {
				t.Fatalf("New error = %v, wantOK %v", err, tt.wantOK)
			}
			if tt.wantOK == (ts == nil) || tt.wantOK == (oa == nil) {
				t.Errorf("New = %v, %v with error %v", ts, oa, err)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "github/auth:") {
				t.Errorf("error %q lacks the github/auth: prefix", err)
			}
		})
	}
	if n := gh.count(); n != 0 {
		t.Errorf("New made %d requests", n)
	}

	s, err := newWithClock(validCredentials(), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if s.hc == nil || s.hc.Timeout != 10*time.Second {
		t.Errorf("default client = %+v, want a 10 s timeout", s.hc)
	}
}
