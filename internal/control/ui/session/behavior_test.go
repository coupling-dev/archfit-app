package session

import (
	"context"
	"errors"
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
		t.Errorf("User = signed in after 12 h + 1 s")
	}
	resp := serve(page, requestWith(http.MethodGet, "/r/o/n", c))
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/login") {
		t.Errorf("status %d to %q, want 303 to the login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := f.users.callCount(); got != calls {
		t.Errorf("GitHub was called with the token of an ended session")
	}
}
