package gcptoken

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const marker = "MARKER-token"

type metadataServer struct {
	requests atomic.Int64
	body     atomic.Value // string
	status   int
	release  chan struct{} // nil: answer at once
}

func newMetadataServer(t *testing.T, body string) (*metadataServer, *Source, *time.Time) {
	t.Helper()
	m := &metadataServer{status: http.StatusOK}
	m.body.Store(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.requests.Add(1)
		if r.Header.Get("Metadata-Flavor") != "Google" || r.Method != http.MethodGet {
			http.Error(w, "missing Metadata-Flavor", http.StatusForbidden)
			return
		}
		if m.release != nil {
			<-m.release
		}
		w.WriteHeader(m.status)
		_, _ = w.Write([]byte(m.body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	s := New(srv.Client())
	s.url = srv.URL
	s.now = func() time.Time { return now }
	return m, s, &now
}

func tokenBody(token string, expiresIn int) string {
	return fmt.Sprintf(`{"access_token":%q,"expires_in":%d,"token_type":"Bearer"}`, token, expiresIn)
}

func mustToken(t *testing.T, s *Source, want string) {
	t.Helper()
	got, err := s.Token(context.Background())
	if err != nil || got != want {
		t.Fatalf("Token = %q, %v, want %q, nil", got, err, want)
	}
}

func TestTokenIsCachedUntilShortlyBeforeExpiry(t *testing.T) {
	m, s, now := newMetadataServer(t, tokenBody("first", 3600))

	mustToken(t, s, "first")
	m.body.Store(tokenBody("second", 3600))
	*now = now.Add(3600*time.Second - refreshBefore - time.Second)
	mustToken(t, s, "first")
	if got := m.requests.Load(); got != 1 {
		t.Fatalf("requests before the margin = %d, want 1", got)
	}

	*now = now.Add(time.Second)
	mustToken(t, s, "second")
	if got := m.requests.Load(); got != 2 {
		t.Errorf("requests after the margin = %d, want 2", got)
	}
}

func TestShortLivedTokenIsNotCached(t *testing.T) {
	m, s, _ := newMetadataServer(t, tokenBody("brief", 30))
	mustToken(t, s, "brief")
	mustToken(t, s, "brief")
	if got := m.requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2: a token inside the margin is used once", got)
	}
}

func TestInvalidResponsesAreErrorsWithoutTheToken(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, tokenBody(marker, 3600)},
		{"not found", http.StatusNotFound, marker},
		{"not JSON", http.StatusOK, marker},
		{"empty token", http.StatusOK, tokenBody("", 3600)},
		{"no expiry", http.StatusOK, tokenBody(marker, 0)},
		{"negative expiry", http.StatusOK, tokenBody(marker, -5)},
		{"wrong token type", http.StatusOK, `{"access_token":"` + marker + `","expires_in":3600,"token_type":"MAC"}`},
		{"too large", http.StatusOK, `{"access_token":"` + marker + strings.Repeat("x", maxResponseBytes) + `","expires_in":3600,"token_type":"Bearer"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, s, _ := newMetadataServer(t, tt.body)
			m.status = tt.status
			got, err := s.Token(context.Background())
			if err == nil || got != "" {
				t.Fatalf("Token = %q, %v, want an error", got, err)
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("error %q leaks the token", err)
			}
		})
	}
}

func TestFailedRefreshKeepsNoStaleToken(t *testing.T) {
	m, s, now := newMetadataServer(t, tokenBody("first", 3600))
	mustToken(t, s, "first")
	*now = now.Add(2 * time.Hour)
	m.status = http.StatusServiceUnavailable
	if got, err := s.Token(context.Background()); err == nil {
		t.Errorf("Token after expiry with the server down = %q, nil, want an error", got)
	}
}

func TestConcurrentCallersShareOneRefresh(t *testing.T) {
	m, s, _ := newMetadataServer(t, tokenBody("shared", 3600))
	m.release = make(chan struct{})
	const callers = 10
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			var got string
			got, errs[i] = s.Token(context.Background())
			if errs[i] == nil && got != "shared" {
				errs[i] = fmt.Errorf("got %q", got)
			}
		})
	}
	close(m.release)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if got := m.requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestWaitingCallerLeavesWhenItsContextEnds(t *testing.T) {
	m, s, _ := newMetadataServer(t, tokenBody("slow", 3600))
	m.release = make(chan struct{})
	defer close(m.release)

	first := make(chan error, 1)
	go func() { _, err := s.Token(context.Background()); first <- err }()
	for m.requests.Load() == 0 {
		time.Sleep(time.Millisecond) // wait until the first caller holds the refresh
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Token(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Token with a cancelled context while a refresh runs = %v, want context.Canceled", err)
	}
	m.release <- struct{}{}
	if err := <-first; err != nil {
		t.Errorf("first caller: %v", err)
	}
}
