package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	// readyRoute does not end in "z": Cloud Run reserves some paths that do, so a deploy check through the service
	// URL could never reach /readyz.
	readyRoute = "GET /ready"
	// readyTimeout bounds one check of every probe, retries and rate-limit waits of the GitHub client included.
	readyTimeout = 5 * time.Second
	// A passed check answers for readyFor, and a failed one for notReadyFor: a probe flood, or a public caller,
	// makes at most one GitHub call in each window.
	readyFor    = 30 * time.Second
	notReadyFor = 5 * time.Second

	// readinessKey is the reserved key the store probe reads. It is never written; any answer but an error passes.
	readinessKey = "app/readiness"
)

// probe checks one dependency the role uses. Its error is logged and never answered.
type probe struct {
	name  string
	check func(ctx context.Context) error
}

// githubProbe proves the App key works with GitHub, without reading any installation.
func githubProbe(checkApp func(context.Context) error) probe {
	return probe{name: "github", check: checkApp}
}

// storeProbe proves the store answers. A missing key is the healthy answer; a missing database is an error, which
// the Firestore driver keeps apart from a missing document.
func storeProbe(st store.Store) probe {
	return probe{name: "store", check: func(ctx context.Context) error {
		if _, err := st.Get(ctx, store.SystemTenant, readinessKey); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return nil
	}}
}

// readiness runs the probes and remembers the result for a short time. One check runs at a time; concurrent
// callers wait for it and share its result.
type readiness struct {
	probes  []probe
	timeout time.Duration
	now     func() time.Time
	log     *slog.Logger

	mu     sync.Mutex
	until  time.Time
	failed []string
}

// check returns the names of the failed probes; empty means ready.
func (r *readiness) check(ctx context.Context) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.now().Before(r.until) {
		return r.failed
	}
	// A caller that hangs up does not cancel the check, so its result stays valid for the next caller.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	defer cancel()
	errs := make([]error, len(r.probes))
	var wg sync.WaitGroup
	for i, p := range r.probes {
		wg.Go(func() { errs[i] = p.check(ctx) })
	}
	wg.Wait()
	failed := []string{}
	for i, err := range errs {
		if err != nil {
			failed = append(failed, r.probes[i].name)
			r.log.ErrorContext(ctx, "readiness probe failed", "probe", r.probes[i].name, "error", err.Error())
		}
	}
	r.failed, r.until = failed, r.now().Add(readyFor)
	if len(failed) > 0 {
		r.until = r.now().Add(notReadyFor)
	}
	return failed
}

// ready answers 200 when every probe passed, else 503 with the names of the failed probes. The body names no
// error, secret or tenant.
func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	failed := s.readiness.check(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if len(failed) == 0 {
		info(r).outcome = "ready"
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	info(r).outcome = "not_ready"
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(struct {
		Status string   `json:"status"`
		Failed []string `json:"failed"`
	}{"unavailable", failed})
}
