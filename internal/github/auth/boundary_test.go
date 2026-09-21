package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMintErrorStatuses(t *testing.T) {
	validExpiry := testEpoch.Add(tokenTTL).Format(time.RFC3339)
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"401", http.StatusUnauthorized, `{"message":"Bad credentials"}`},
		{"403 suspended", http.StatusForbidden, `{"message":"This installation has been suspended"}`},
		{"404 uninstalled", http.StatusNotFound, `{"message":"Not Found"}`},
		{"422", http.StatusUnprocessableEntity, `{"message":"Unprocessable"}`},
		{"500", http.StatusInternalServerError, `oops`},
		{"201 empty token", http.StatusCreated, `{"token":"","expires_at":"` + validExpiry + `"}`},
		{"201 unparsable expires_at", http.StatusCreated, `{"token":"ghs_x","expires_at":"next tuesday"}`},
		{"201 missing expires_at", http.StatusCreated, `{"token":"ghs_x"}`},
		{"201 malformed JSON", http.StatusCreated, `{"token":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := newFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { writeRaw(w, tt.status, tt.body) })
			s := newTestSource(t, newFakeClock(), gh.client())
			for call := 1; call <= 2; call++ {
				token, err := s.InstallationToken(t.Context(), 77)
				if err == nil || token != "" {
					t.Fatalf("call %d = %q, %v; want an error and no token", call, token, err)
				}
				if !strings.HasPrefix(err.Error(), "github/auth:") || !strings.Contains(err.Error(), strconv.Itoa(tt.status)) {
					t.Errorf("error %q does not name status %d under the github/auth: prefix", err, tt.status)
				}
				if n := gh.count(); n != call {
					t.Errorf("requests after call %d = %d", call, n)
				}
			}
		})
	}
}

func TestRevokedInstallationAfterCacheExpiry(t *testing.T) {
	clock := newFakeClock()
	iss := newIssuer(clock)
	var revoked atomic.Bool
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() {
			writeRaw(w, http.StatusNotFound, `{"message":"Not Found"}`)
			return
		}
		iss.handler(w, r)
	})
	s := newTestSource(t, clock, gh.client())

	mustToken(t, s, 77)
	clock.advance(56 * time.Minute)
	revoked.Store(true)

	if token, err := s.InstallationToken(t.Context(), 77); err == nil || token != "" {
		t.Errorf("after revocation = %q, %v; want an error and no token", token, err)
	}
}

func TestNoCrossInstallationToken(t *testing.T) {
	clock := newFakeClock()
	gh := newFakeGitHub(t, newIssuer(clock).handler)
	s := newTestSource(t, clock, gh.client())

	for round := range 6 {
		for _, id := range []int64{1, 2, 2, 1} {
			token := mustToken(t, s, id)
			if want := fmt.Sprintf("ghs_app-installations-%d-access_tokens_", id); !strings.HasPrefix(token, want) {
				t.Fatalf("installation %d got token %q", id, token)
			}
		}
		if round%2 == 1 {
			clock.advance(56 * time.Minute)
		}
	}

	before := gh.count()
	for _, id := range []int64{0, -1} {
		if token, err := s.InstallationToken(t.Context(), id); err == nil || token != "" {
			t.Errorf("InstallationToken(%d) = %q, %v; want an error", id, token, err)
		}
	}
	if n := gh.count(); n != before {
		t.Errorf("invalid IDs made %d requests", n-before)
	}
}

func TestRepoReadTokenRefusesWiderOrWrongScope(t *testing.T) {
	const fullName = "octo/repo"
	repo := func(names ...string) []map[string]any {
		out := make([]map[string]any, len(names))
		for i, n := range names {
			out[i] = map[string]any{"full_name": n}
		}
		return out
	}
	created := func(permissions map[string]string, repositories any) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			body := map[string]any{"token": "ghs_narrow", "expires_at": testEpoch.Add(tokenTTL).Format(time.RFC3339), "permissions": permissions}
			if repositories != nil {
				body["repositories"] = repositories
			}
			writeJSON(w, http.StatusCreated, body)
		}
	}
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { writeRaw(w, code, `{"message":"no"}`) }
	}
	readOnly := map[string]string{"contents": "read", "metadata": "read"}

	responses := []struct {
		name    string
		handler http.HandlerFunc
		wantOK  bool
	}{
		{"exact scope", created(readOnly, repo(fullName)), true},
		{"exact scope, other case", created(readOnly, repo("Octo/Repo")), true},
		{"contents write", created(map[string]string{"contents": "write", "metadata": "read"}, repo(fullName)), false},
		{"extra actions read", created(map[string]string{"contents": "read", "metadata": "read", "actions": "read"}, repo(fullName)), false},
		{"two repositories", created(readOnly, repo(fullName, "octo/other")), false},
		{"another repository", created(readOnly, repo("octo/other")), false},
		{"no repositories field", created(readOnly, nil), false},
		{"no permissions field", created(nil, repo(fullName)), false},
		{"metadata only", created(map[string]string{"metadata": "read"}, repo(fullName)), false},
		{"422", status(http.StatusUnprocessableEntity), false},
		{"404", status(http.StatusNotFound), false},
	}
	for _, tt := range responses {
		t.Run(tt.name, func(t *testing.T) {
			gh := newFakeGitHub(t, tt.handler)
			s := newTestSource(t, newFakeClock(), gh.client())
			token, err := s.RepoReadToken(t.Context(), 77, fullName)
			if tt.wantOK {
				if err != nil || token != "ghs_narrow" {
					t.Fatalf("RepoReadToken = %q, %v", token, err)
				}
				return
			}
			if err == nil || token != "" {
				t.Fatalf("RepoReadToken = %q, %v; want an error and no token", token, err)
			}
			if n := gh.count(); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	}

	t.Run("non-ASCII case fold", func(t *testing.T) {
		gh := newFakeGitHub(t, created(readOnly, repo("\u212a/\u017f")))
		s := newTestSource(t, newFakeClock(), gh.client())
		if token, err := s.RepoReadToken(t.Context(), 77, "k/s"); err == nil || token != "" {
			t.Errorf("RepoReadToken = %q, %v; want an error", token, err)
		}
	})

	inputs := []struct {
		name     string
		id       int64
		fullName string
	}{
		{"no slash", 77, "a"},
		{"two slashes", 77, "a/b/c"},
		{"dot-dot owner", 77, "../x"},
		{"dot repo", 77, "a/."},
		{"empty", 77, ""},
		{"space", 77, "a/b c"},
		{"ID 0", 0, fullName},
		{"negative ID", -1, fullName},
	}
	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			gh := newFakeGitHub(t, created(readOnly, repo(fullName)))
			s := newTestSource(t, newFakeClock(), gh.client())
			if token, err := s.RepoReadToken(t.Context(), tt.id, tt.fullName); err == nil || token != "" {
				t.Errorf("RepoReadToken = %q, %v; want an error", token, err)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("bad input made %d requests", n)
			}
		})
	}
}

func TestExchangeFailures(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	ok := oauthHandler("ghu_user", map[string]any{"login": "octocat", "id": 583231})

	tests := []struct {
		name    string
		ctx     context.Context
		code    string
		handler http.HandlerFunc
		wantReq int
	}{
		{"error in a 200 body", t.Context(), "code", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"error": "bad_verification_code", "error_description": "expired"})
		}, 1},
		{"no access_token", t.Context(), "code", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"token_type": "bearer"})
		}, 1},
		{"exchange 500", t.Context(), "code", func(w http.ResponseWriter, _ *http.Request) {
			writeRaw(w, http.StatusInternalServerError, "oops")
		}, 1},
		{"GET /user 401", t.Context(), "code", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == userPath {
				writeRaw(w, http.StatusUnauthorized, `{"message":"Bad credentials"}`)
				return
			}
			ok(w, r)
		}, 2},
		{"GET /user without identity", t.Context(), "code", oauthHandler("ghu_user", map[string]any{}), 2},
		{"empty code", t.Context(), "", ok, 0},
		{"cancelled context", cancelled, "code", ok, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := newFakeGitHub(t, tt.handler)
			_, oa, err := New(validCredentials(), gh.client())
			if err != nil {
				t.Fatal(err)
			}
			got, err := oa.Exchange(tt.ctx, tt.code)
			if err == nil {
				t.Fatal("Exchange succeeded")
			}
			if got != (UserToken{}) {
				t.Errorf("UserToken = %+v, want zero", got)
			}
			if !strings.HasPrefix(err.Error(), "github/auth:") {
				t.Errorf("error %q lacks the github/auth: prefix", err)
			}
			if n := gh.count(); n != tt.wantReq {
				t.Errorf("requests = %d, want %d", n, tt.wantReq)
			}
		})
	}
}

func TestErrorsAndStringsCarryNoSecrets(t *testing.T) {
	const (
		keyMarker    = "MARKER-KEY-BYTES"
		secretMarker = "MARKER-CLIENT-SECRET"
		codeMarker   = "MARKER-OAUTH-CODE"
		tokenMarker  = "MARKER-TOKEN"
		bodyMarker   = "MARKER-RESPONSE-BODY"
	)
	markers := []string{keyMarker, secretMarker, codeMarker, tokenMarker, bodyMarker}
	var texts []string
	collect := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: no error", what)
			return
		}
		texts = append(texts, err.Error(), fmt.Sprintf("%+v", err))
	}

	badKey := validCredentials()
	badKey.PrivateKeyPEM = []byte("-----BEGIN RSA PRIVATE KEY-----\n" + keyMarker + "\n-----END RSA PRIVATE KEY-----\n")
	_, _, err := New(badKey, nil)
	collect("bad key", err)
	badKey.PrivateKeyPEM = []byte(keyMarker)
	_, _, err = New(badKey, nil)
	collect("garbage key", err)
	noID := validCredentials()
	noID.OAuthClientSecret = secretMarker
	noID.OAuthClientID = ""
	_, _, err = New(noID, nil)
	collect("empty client ID", err)

	creds := validCredentials()
	creds.OAuthClientSecret = secretMarker
	handlers := map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) {
			writeRaw(w, http.StatusForbidden, `{"token":"`+tokenMarker+`","message":"`+bodyMarker+`"}`)
		},
		"bad expiry": func(w http.ResponseWriter, _ *http.Request) {
			writeRaw(w, http.StatusCreated, `{"token":"`+tokenMarker+`","expires_at":"`+bodyMarker+`"}`)
		},
		"bad JSON": func(w http.ResponseWriter, _ *http.Request) {
			writeRaw(w, http.StatusCreated, `{"token":"`+tokenMarker+`",`+bodyMarker)
		},
		"wide scope": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]any{
				"token": tokenMarker, "permissions": map[string]string{bodyMarker: "write"},
				"repositories": []map[string]string{{"full_name": bodyMarker + "/x"}},
			})
		},
		"oauth error": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"error": bodyMarker, "error_description": bodyMarker, "access_token": tokenMarker})
		},
		"oauth status": func(w http.ResponseWriter, _ *http.Request) { writeRaw(w, http.StatusBadGateway, bodyMarker) },
		"user 401": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == userPath {
				writeRaw(w, http.StatusUnauthorized, bodyMarker)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"access_token": tokenMarker})
		},
		"user bad JSON": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == userPath {
				writeRaw(w, http.StatusOK, `{"login":"`+bodyMarker)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"access_token": tokenMarker})
		},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, h := range handlers {
		gh := newFakeGitHub(t, h)
		ts, oa, err := New(creds, gh.client())
		if err != nil {
			t.Fatal(err)
		}
		_, err = ts.InstallationToken(t.Context(), 77)
		collect(name+": installation token", err)
		_, err = ts.RepoReadToken(t.Context(), 77, "octo/repo")
		collect(name+": repo token", err)
		_, err = oa.Exchange(t.Context(), codeMarker)
		collect(name+": exchange", err)
		_, err = ts.InstallationToken(cancelled, 77)
		collect(name+": cancelled mint", err)
		_, err = oa.Exchange(cancelled, codeMarker)
		collect(name+": cancelled exchange", err)
		_, err = ts.RepoReadToken(t.Context(), 77, tokenMarker+"/../"+bodyMarker)
		collect(name+": bad full name", err)

		for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
			texts = append(texts, fmt.Sprintf(verb, ts), fmt.Sprintf(verb, oa))
		}
	}

	privateExponent := testKey().D.String()
	for _, text := range texts {
		for _, m := range markers {
			if strings.Contains(text, m) {
				t.Errorf("secret marker %s leaked in %q", m, text)
			}
		}
		if strings.Contains(text, privateExponent) || strings.Contains(text, "client-secret") {
			t.Errorf("credential leaked in %q", text)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTransportErrorsCarryNoURL(t *testing.T) {
	errBoom := errors.New("connection refused by the test")

	failAll := func(cause error) http.RoundTripper {
		return roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })
	}
	// Answers the code exchange, then fails at GET /user.
	failUserLookup := func(cause error) http.RoundTripper {
		return roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPost {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"access_token":"ghu_user"}`)),
				}, nil
			}
			return nil, cause
		})
	}

	calls := []struct {
		name      string
		transport func(error) http.RoundTripper
		call      func(context.Context, TokenSource, OAuth) error
	}{
		{"InstallationToken", failAll, func(ctx context.Context, ts TokenSource, _ OAuth) error {
			_, err := ts.InstallationToken(ctx, 77)
			return err
		}},
		{"RepoReadToken", failAll, func(ctx context.Context, ts TokenSource, _ OAuth) error {
			_, err := ts.RepoReadToken(ctx, 77, "octo/repo")
			return err
		}},
		{"Exchange", failAll, func(ctx context.Context, _ TokenSource, oa OAuth) error {
			_, err := oa.Exchange(ctx, "code")
			return err
		}},
		{"Exchange at the user lookup", failUserLookup, func(ctx context.Context, _ TokenSource, oa OAuth) error {
			_, err := oa.Exchange(ctx, "code")
			return err
		}},
	}
	// What a transport returns when the request deadline passes, and a plain failure.
	causes := map[string]error{"timeout": context.DeadlineExceeded, "plain failure": errBoom}
	for _, c := range calls {
		for name, cause := range causes {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				ts, oa, err := New(validCredentials(), &http.Client{Transport: c.transport(cause)})
				if err != nil {
					t.Fatal(err)
				}
				err = c.call(t.Context(), ts, oa)
				if err == nil {
					t.Fatal("call succeeded")
				}
				if !errors.Is(err, cause) {
					t.Errorf("errors.Is(%q, %v) = false", err, cause)
				}
				text := err.Error()
				if !strings.HasPrefix(text, "github/auth:") {
					t.Errorf("error %q lacks the github/auth: prefix", text)
				}
				for _, leak := range []string{"https://", "github.com", "/app/installations", "/login/oauth", "/user"} {
					if strings.Contains(text, leak) {
						t.Errorf("error %q contains %q", text, leak)
					}
				}
			})
		}
	}
}

func TestAuthImportsOnlyStdlib(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const self = "github.com/alexei-led/archfit-app/internal/github/auth"
	for dep := range strings.FieldsSeq(string(out)) {
		if dep != self {
			t.Errorf("non-standard dependency %s", dep)
		}
	}

	out, err = exec.CommandContext(t.Context(), "go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for imp := range strings.FieldsSeq(string(out)) {
		if imp == "log" || imp == "log/slog" || imp == "os" {
			t.Errorf("forbidden direct import %s", imp)
		}
	}
}
