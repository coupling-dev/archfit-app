package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCookieRoundTrip(t *testing.T) {
	f := newFixture(t)

	first, err := f.s.sealSession(testUser)
	if err != nil {
		t.Fatalf("sealSession: %v", err)
	}
	second, err := f.s.sealSession(testUser)
	if err != nil {
		t.Fatalf("sealSession: %v", err)
	}
	if first == second {
		t.Errorf("two seals of the same user are equal; the nonce is not random")
	}
	if strings.Contains(first, testToken) {
		t.Errorf("sealed cookie carries the token in clear")
	}

	got, ok := f.s.openSession(first)
	if !ok || got != testUser {
		t.Errorf("openSession = %+v, %v; want the sealed user", got.UserID, ok)
	}
}

func TestCookieRejectsTamperingAndExpiry(t *testing.T) {
	logs := captureLogs(t)
	f := newFixture(t)
	valid, err := f.s.sealSession(testUser)
	if err != nil {
		t.Fatalf("sealSession: %v", err)
	}
	otherKey, err := newFixtureWithKey(t, bytes.Repeat([]byte("x"), 32)).s.sealSession(testUser)
	if err != nil {
		t.Fatalf("sealSession: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(valid)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)/2] ^= 0x01
	flipped := base64.RawURLEncoding.EncodeToString(raw)

	tests := []struct {
		name    string
		value   string
		advance time.Duration
		asState bool
	}{
		{name: "one flipped bit", value: flipped},
		{name: "truncated", value: valid[:len(valid)/2]},
		{name: "invalid base64", value: "!!not*base64!!"},
		{name: "empty", value: ""},
		{name: "sealed with another key", value: otherKey},
		{name: "session cookie under the state cookie name", value: valid, asState: true},
		{name: "expired", value: valid, advance: sessionTTL + time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.clock.advance(tt.advance)
			if tt.asState {
				if _, ok := f.s.openState(tt.value); ok {
					t.Errorf("openState accepted a session cookie value")
				}
				return
			}
			r := requestWith(http.MethodGet, "/", &http.Cookie{Name: sessionCookie, Value: tt.value})
			if u, ok := f.s.User(r); ok || u != (User{}) {
				t.Errorf("User = %v, true; want not signed in", u.UserID)
			}
		})
	}

	if u, ok := f.s.User(requestWith(http.MethodGet, "/")); ok || u != (User{}) {
		t.Errorf("User without a cookie = signed in")
	}
	for _, secret := range []string{valid, flipped, otherKey, testToken} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("log carries a cookie value or the token")
		}
	}
}

func TestPermissionOrder(t *testing.T) {
	tests := []struct {
		have, min string
		want      bool
	}{
		{"none", "read", false},
		{"none", "write", false},
		{"none", "admin", false},
		{"read", "read", true},
		{"read", "write", false},
		{"read", "admin", false},
		{"write", "read", true},
		{"write", "write", true},
		{"write", "admin", false},
		{"admin", "read", true},
		{"admin", "write", true},
		{"admin", "admin", true},
		{"triage", "read", false},
		{"", "read", false},
		{"ADMIN", "read", false},
		{"admin", "none", false},
		{"none", "none", false},
		{"admin", "triage", false},
		{"admin", "", false},
		{"admin", "ADMIN", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("have=%q,min=%q", tt.have, tt.min), func(t *testing.T) {
			if got := allowed(tt.have, tt.min); got != tt.want {
				t.Errorf("allowed(%q, %q) = %v, want %v", tt.have, tt.min, got, tt.want)
			}
		})
	}
}

func TestReturnPathIsLocal(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/r/o/n", "/r/o/n"},
		{"/", "/"},
		{"", "/"},
		{"//evil.example", "/"},
		{`/\evil.example`, "/"},
		{"https://evil.example", "/"},
		{"javascript:x", "/"},
		{"/r/o/n\r\nSet-Cookie: x=y", "/"},
		{"/\t/evil.example", "/"},
		{"r/o/n", "/"},
		{"/r/o/n?tab=policy", "/r/o/n?tab=policy"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.in), func(t *testing.T) {
			if got := safeReturn(tt.in); got != tt.want {
				t.Errorf("safeReturn(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCacheExpiryAndBound(t *testing.T) {
	quietLogs(t)
	ctx := context.Background()

	t.Run("expiry", func(t *testing.T) {
		f := newFixture(t)
		for _, step := range []struct {
			advance   time.Duration
			wantCalls int
		}{{0, 1}, {59 * time.Second, 1}, {2 * time.Second, 2}} {
			f.clock.advance(step.advance)
			if _, err := f.s.Authorize(ctx, testUser, testRepo, "read"); err != nil {
				t.Fatalf("Authorize: %v", err)
			}
			if got := f.users.callCount(); got != step.wantCalls {
				t.Errorf("after +%v: %d Permission calls, want %d", step.advance, got, step.wantCalls)
			}
			if got := f.repos.callCount(); got != step.wantCalls {
				t.Errorf("after +%v: %d ResolveRepo calls, want %d", step.advance, got, step.wantCalls)
			}
		}
	})

	t.Run("key ignores case", func(t *testing.T) {
		f := newFixture(t)
		for _, name := range []string{"Owner/Name", "owner/name"} {
			if _, err := f.s.Authorize(ctx, testUser, name, "read"); err != nil {
				t.Fatalf("Authorize: %v", err)
			}
		}
		if got := f.users.callCount(); got != 1 {
			t.Errorf("%d Permission calls, want 1", got)
		}
	})

	t.Run("bound", func(t *testing.T) {
		f := newFixture(t)
		fill := func(from, to int) {
			t.Helper()
			for i := from; i < to; i++ {
				if _, err := f.s.Authorize(ctx, testUser, fmt.Sprintf("o/n%d", i), "read"); err != nil {
					t.Fatalf("Authorize: %v", err)
				}
				if n := f.s.cacheLen(); n > maxCacheEntries {
					t.Fatalf("cache holds %d entries, bound is %d", n, maxCacheEntries)
				}
			}
		}

		fill(0, maxCacheEntries)
		if n := f.s.cacheLen(); n != maxCacheEntries {
			t.Fatalf("cache holds %d entries, want %d", n, maxCacheEntries)
		}
		// Full and nothing expired: the whole cache is cleared.
		fill(maxCacheEntries, maxCacheEntries+1)
		if n := f.s.cacheLen(); n != 1 {
			t.Errorf("after overflow: %d entries, want 1", n)
		}

		// Full with one expired entry: only that entry is dropped.
		f.clock.advance(30 * time.Second)
		fill(maxCacheEntries+1, 2*maxCacheEntries)
		f.clock.advance(31 * time.Second)
		fill(2*maxCacheEntries, 2*maxCacheEntries+1)
		if n := f.s.cacheLen(); n != maxCacheEntries {
			t.Errorf("after dropping one expired entry: %d entries, want %d", n, maxCacheEntries)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		f := newFixture(t)
		var wg sync.WaitGroup
		for g := range 8 {
			wg.Go(func() {
				for i := range 50 {
					if _, err := f.s.Authorize(ctx, testUser, fmt.Sprintf("o/n%d", (g+i)%10), "write"); err != nil {
						t.Errorf("Authorize: %v", err)
					}
				}
			})
		}
		wg.Wait()
		if n := f.s.cacheLen(); n != 10 {
			t.Errorf("cache holds %d entries, want 10", n)
		}
	})
}
