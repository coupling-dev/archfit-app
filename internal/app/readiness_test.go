package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/store/firestore"
)

// countedProbe fails with err (nil passes) and counts its calls.
func countedProbe(name string, err error, calls *atomic.Int32) probe {
	return probe{name: name, check: func(context.Context) error {
		calls.Add(1)
		return err
	}}
}

func readyRequest() *http.Request { return httptest.NewRequest(http.MethodGet, "/ready", nil) }

func TestReadyAnswersFromTheProbes(t *testing.T) {
	leak := errors.New("MARKER-secret-or-tenant")
	tests := []struct {
		name                string
		githubErr, storeErr error
		want                int
		body                string
		outcome             string
	}{
		{"both pass", nil, nil, http.StatusOK, `{"status":"ok"}`, "ready"},
		{"GitHub refuses the App key", leak, nil, http.StatusServiceUnavailable, `{"status":"unavailable","failed":["github"]}`, "not_ready"},
		{"the store does not answer", nil, leak, http.StatusServiceUnavailable, `{"status":"unavailable","failed":["store"]}`, "not_ready"},
		{"both fail", leak, leak, http.StatusServiceUnavailable, `{"status":"unavailable","failed":["github","store"]}`, "not_ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			f := newFixture()
			f.probes = []probe{countedProbe("github", tt.githubErr, &calls), countedProbe("store", tt.storeErr, &calls)}
			f.rebuild()
			rec := f.do(readyRequest())
			if rec.Code != tt.want || strings.TrimSpace(rec.Body.String()) != tt.body {
				t.Errorf("GET /ready = %d %s, want %d %s", rec.Code, rec.Body, tt.want, tt.body)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("the answer may be cached by a proxy")
			}
			if strings.Contains(rec.Body.String(), "MARKER") {
				t.Error("the body quotes a probe error")
			}
			if logs := f.logs.String(); !strings.Contains(logs, `"outcome":"`+tt.outcome+`"`) {
				t.Errorf("logs lack outcome %s: %s", tt.outcome, logs)
			}
			if calls.Load() != 2 {
				t.Errorf("probes ran %d times, want 2", calls.Load())
			}
		})
	}
}

func TestReadyRemembersAResultBriefly(t *testing.T) {
	now := testNow
	clock := func() time.Time { return now }
	tests := []struct {
		name string
		err  error
		keep time.Duration
	}{
		{"a pass", nil, readyFor},
		{"a failure", errors.New("down"), notReadyFor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now = testNow
			var calls atomic.Int32
			r := &readiness{probes: []probe{countedProbe("github", tt.err, &calls)}, timeout: time.Second, now: clock, log: slog.New(slog.DiscardHandler)}
			r.check(t.Context())
			now = now.Add(tt.keep - time.Nanosecond)
			r.check(t.Context())
			if calls.Load() != 1 {
				t.Errorf("within %s: probes ran %d times, want 1", tt.keep, calls.Load())
			}
			now = now.Add(time.Nanosecond)
			if failed := r.check(t.Context()); (len(failed) == 0) != (tt.err == nil) || calls.Load() != 2 {
				t.Errorf("after %s: failed = %v, probes ran %d times; want a fresh check", tt.keep, failed, calls.Load())
			}
		})
	}
}

func TestReadyIsBounded(t *testing.T) {
	t.Run("a probe that hangs fails at the timeout", func(t *testing.T) {
		hang := probe{name: "github", check: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
		r := &readiness{probes: []probe{hang}, timeout: 20 * time.Millisecond, now: time.Now, log: slog.New(slog.DiscardHandler)}
		start := time.Now()
		if failed := r.check(t.Context()); len(failed) != 1 || failed[0] != "github" {
			t.Errorf("failed = %v, want github", failed)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("check took %s", d)
		}
	})

	t.Run("concurrent callers share one check", func(t *testing.T) {
		var calls atomic.Int32
		slow := probe{name: "github", check: func(context.Context) error {
			calls.Add(1)
			time.Sleep(20 * time.Millisecond)
			return nil
		}}
		r := &readiness{probes: []probe{slow}, timeout: time.Second, now: time.Now, log: slog.New(slog.DiscardHandler)}
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() { r.check(context.Background()) })
		}
		wg.Wait()
		if calls.Load() != 1 {
			t.Errorf("probes ran %d times, want 1", calls.Load())
		}
	})

	t.Run("a caller that hangs up does not cancel the check", func(t *testing.T) {
		seen := make(chan error, 1)
		p := probe{name: "store", check: func(ctx context.Context) error { seen <- ctx.Err(); return nil }}
		r := &readiness{probes: []probe{p}, timeout: time.Second, now: time.Now, log: slog.New(slog.DiscardHandler)}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if failed := r.check(ctx); len(failed) != 0 || <-seen != nil {
			t.Errorf("failed = %v; the probe saw a cancelled context", failed)
		}
	})
}

func TestReadyNeedsNoCredentialOnAnyRole(t *testing.T) {
	for _, role := range []string{roleAll, rolePublic, roleWorker} {
		f := newRoleFixture(role)
		if rec := f.do(readyRequest()); rec.Code != http.StatusOK {
			t.Errorf("%s: GET /ready = %d, want 200 without a credential", role, rec.Code)
		}
	}
}

type failingStore struct{ store.Store }

func (failingStore) Get(context.Context, store.Tenant, string) ([]byte, error) {
	return nil, errors.New("store: unavailable")
}

func TestStoreProbe(t *testing.T) {
	missingDatabase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND"}}`))
	}))
	t.Cleanup(missingDatabase.Close)
	noDatabase, err := firestore.New(firestore.Config{
		ProjectID: "coupling-dev-510710", EmulatorHost: strings.TrimPrefix(missingDatabase.URL, "http://"),
	}, nil, missingDatabase.Client())
	if err != nil {
		t.Fatal(err)
	}
	written := store.NewMemory()
	if err := written.Put(context.Background(), store.SystemTenant, readinessKey, []byte("x")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		st    store.Store
		ready bool
	}{
		{"the reserved key is missing", store.NewMemory(), true},
		{"the store fails", failingStore{store.NewMemory()}, false},
		{"the Firestore database does not exist", noDatabase, false},
		{"the reserved key holds a value", written, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := storeProbe(tt.st).check(t.Context())
			if (err == nil) != tt.ready {
				t.Errorf("store probe = %v, want ready %v", err, tt.ready)
			}
		})
	}
}

func TestReadyIsWiredToGitHubAndTheStore(t *testing.T) {
	// noNetwork answers GET /app with 404: the App key is not proved, the memory store answers.
	base, network, stop := startRun(t, validConfig(t), slog.New(slog.DiscardHandler))
	resp := send(t, httptest.NewRequest(http.MethodGet, base+"/ready", nil))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /ready = %d, want 503", resp.StatusCode)
	}
	if got := network.requests(); got != 1 {
		t.Errorf("GET /ready made %d outbound requests, want 1 (GET /app)", got)
	}
	if err := stop(); err != nil {
		t.Errorf("run = %v", err)
	}
}
