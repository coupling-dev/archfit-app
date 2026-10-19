package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestRevokedUserLosesAccessWithinCacheWindow(t *testing.T) {
	quietLogs(t)
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.Authorize(ctx, testUser, testRepo, "write"); err != nil {
		t.Fatalf("Authorize before the revocation: %v", err)
	}

	f.users.answer("none", nil)
	f.clock.advance(61 * time.Second)

	for _, advance := range []time.Duration{0, time.Second, 61 * time.Second, time.Hour} {
		f.clock.advance(advance)
		for _, min := range []string{"read", "write", "admin"} {
			repo, err := f.s.Authorize(ctx, testUser, testRepo, min)
			if !errors.Is(err, ErrForbidden) || repo != (api.Repo{}) {
				t.Errorf("+%v, min %s: Authorize = %+v, %v; want no repo and ErrForbidden", advance, min, repo, err)
			}
		}
	}
	if got := f.repos.callCount(); got != 1 {
		t.Errorf("%d ResolveRepo calls, want only the one before the revocation", got)
	}
}

func TestRemovedRepositoryIsForbidden(t *testing.T) {
	quietLogs(t)
	f := newFixture(t)
	ctx := context.Background()
	f.users.answer("admin", nil)
	if _, err := f.s.Authorize(ctx, testUser, testRepo, "admin"); err != nil {
		t.Fatalf("Authorize before the removal: %v", err)
	}

	f.repos.answer(api.Repo{}, api.ErrNotFound)
	f.clock.advance(61 * time.Second)

	repo, err := f.s.Authorize(ctx, testUser, testRepo, "read")
	if !errors.Is(err, ErrForbidden) || repo != (api.Repo{}) {
		t.Errorf("Authorize = %+v, %v; want no repo and ErrForbidden", repo, err)
	}
}

func TestCrossInstallationIdentifierHasNoEffect(t *testing.T) {
	quietLogs(t)
	f := newFixture(t)
	c := f.signIn(t)
	f.users.answer("none", nil)

	// The request names installation A (42) everywhere a client can put it.
	form := strings.NewReader("installation_id=42&repo_id=1001")
	r := httptest.NewRequest(http.MethodPost, "/r/other-org/secret?installation_id=42&repo_id=1001", form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Installation-Id", "42")
	r.AddCookie(c)

	u, ok := f.s.User(r)
	if !ok {
		t.Fatalf("not signed in")
	}
	repo, err := f.s.Authorize(r.Context(), u, "other-org/secret", "read")

	if !errors.Is(err, ErrForbidden) || repo != (api.Repo{}) {
		t.Errorf("Authorize = %+v, %v; want no repo and ErrForbidden", repo, err)
	}
	if got := f.repos.callCount(); got != 0 {
		t.Errorf("%d ResolveRepo calls, want 0", got)
	}
	if got := f.users.lastCall(); got != (permissionCall{testToken, "other-org/secret"}) {
		t.Errorf("Permission asked about %q, want the requested repository with the user's token", got.fullName)
	}
}

func TestSessionEndsAfterTwelveHours(t *testing.T) {
	quietLogs(t)
	f := newFixture(t)
	c := f.signIn(t)
	// A protected page as the parent builds it: no user, no GitHub call, back to login.
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := f.s.User(r)
		if !ok {
			http.Redirect(w, r, "/auth/login?return="+r.URL.Path, http.StatusSeeOther)
			return
		}
		if _, err := f.s.Authorize(r.Context(), u, testRepo, "read"); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	})

	f.clock.advance(sessionTTL - time.Second)
	if resp := serve(page, requestWith(http.MethodGet, "/r/o/n", c)); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d just before the session ends, want 200", resp.StatusCode)
	}
	calls := f.users.callCount()

	f.clock.advance(2 * time.Second)

	if u, ok := f.s.User(requestWith(http.MethodGet, "/r/o/n", c)); ok || u != (User{}) {
		t.Errorf("User = signed in after 8 h + 1 s")
	}
	resp := serve(page, requestWith(http.MethodGet, "/r/o/n", c))
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/login") {
		t.Errorf("status %d to %q, want 303 to the login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := f.users.callCount(); got != calls {
		t.Errorf("GitHub was called with the token of an ended session")
	}
}

// logLine returns the one "session: authorize" record in the captured JSON logs.
func logLine(t *testing.T, logs *syncBuffer) map[string]any {
	t.Helper()
	var line map[string]any
	for raw := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}
		if rec["msg"] == "session: authorize" {
			if line != nil {
				t.Fatalf("more than one authorize log line:\n%s", logs.String())
			}
			line = rec
		}
	}
	if line == nil {
		t.Fatalf("no authorize log line in:\n%s", logs.String())
	}
	return line
}

func TestRejectedUserTokenIsAnInfoOutcomeAndNeverCached(t *testing.T) {
	logs := captureLogs(t)
	f := newFixture(t)
	f.users.answer("", fmt.Errorf("github: GET /repos/{r}: %w", api.ErrUserTokenRejected))

	repo, err := f.s.Authorize(context.Background(), testUser, testRepo, "read")

	if !errors.Is(err, api.ErrUserTokenRejected) || errors.Is(err, ErrForbidden) || repo != (api.Repo{}) {
		t.Errorf("Authorize = %+v, %v; want no repo and an error wrapping ErrUserTokenRejected, not ErrForbidden", repo, err)
	}
	line := logLine(t, logs)
	if line["outcome"] != "token_rejected" || line["level"] != slog.LevelInfo.String() {
		t.Errorf("log = outcome %v level %v, want token_rejected at INFO", line["outcome"], line["level"])
	}
	if n := f.s.cacheLen(); n != 0 {
		t.Errorf("cache holds %d entries, want 0", n)
	}
}

// roundTripFunc is the GitHub transport of a real api.Client.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubErrorLogsStepAndStatusOnly(t *testing.T) {
	const body = "RESPONSE_BODY_MARK"
	logs := captureLogs(t)
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	f := newFixture(t)
	s, err := New(Config{CookieKey: testKey, BaseURL: testBaseURL}, f.oauth, api.New(nil, hc), f.repos, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Authorize(context.Background(), testUser, testRepo, "read"); err == nil {
		t.Fatal("Authorize = nil error on a 403")
	}

	line := logLine(t, logs)
	if line["outcome"] != "github_error" || line["step"] != "permission" || line["status"] != float64(http.StatusForbidden) {
		t.Errorf("log = %v, want github_error at step permission with status 403", line)
	}
	for _, secret := range []string{testToken, body, "api.github.com"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("log leaks %q:\n%s", secret, logs.String())
		}
	}
}

func TestEndDeletesTheSessionCookie(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()

	f.s.End(rec)

	if c := findCookie(rec.Result(), sessionCookie); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("session cookie after End = %+v, want deleted", c)
	}
}
