package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	maxBodyBytes = 512 << 10

	permRead  = "read"
	permWrite = "write"
	permAdmin = "admin"

	// contentSecurityPolicy is the policy of every response but the shell. The shell adds a style nonce (shellPolicy);
	// web/csp.txt is shellPolicy with the placeholder nonce, and TestWebPreviewUsesTheProductionCSP keeps them equal.
	contentSecurityPolicy = cspHead + cspTail
	cspHead               = "default-src 'none'; script-src 'self'; style-src 'self'"
	cspTail               = "; connect-src 'self'; img-src 'self' data:; font-src 'self'; base-uri 'none'; " +
		"form-action 'self'; frame-ancestors 'none'; object-src 'none'; worker-src 'self'"
)

// scope is what the chain hands to an API handler: the signed-in user and, on /api/repos routes, the repository
// authorized on this request. It is the only tenant identity a handler may use.
type scope struct {
	user session.User
	repo api.Repo
}

// reqLog collects the identifiers of one request for its single log line.
type reqLog struct {
	userID, repoID, installationID int64
	outcome                        string
}

type reqLogKey struct{}

func logOf(r *http.Request) *reqLog {
	if l, ok := r.Context().Value(reqLogKey{}).(*reqLog); ok {
		return l
	}
	return &reqLog{}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	// beforeHeader runs once, just before the status line goes out, while headers can still change.
	beforeHeader func(http.Header)
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		if w.beforeHeader != nil {
			w.beforeHeader(w.Header())
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// serverTiming is the Server-Timing value of one response: time to the header, GitHub time summed with the call
// count, each read stage, and "cold" on the first request this instance serves. Only numbers and the fixed metric
// names the code passes to api.StartStage go out, never tenant data.
func serverTiming(total time.Duration, gh *api.CallStats, stages map[string]api.Stage, cold bool) string {
	parts := []string{fmt.Sprintf("total;dur=%.1f", ms(total))}
	if n := gh.Calls(); n > 0 {
		parts = append(parts, fmt.Sprintf(`gh;dur=%.1f;desc="%d calls"`, ms(gh.Duration()), n))
	}
	names := make([]string, 0, len(stages))
	for name := range stages {
		if metricName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s;dur=%.1f", name, stages[name].DurationMS))
	}
	if cold {
		parts = append(parts, "cold")
	}
	return strings.Join(parts, ", ")
}

var metricName = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// chain is the part of the handler chain every route shares: recover, security headers, timing, body limit, log line.
// It is built once per process, so its first request is the instance's cold one.
func chain(next http.Handler) http.Handler {
	var served atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		cold := !served.Swap(true)
		l := &reqLog{outcome: "ok"}
		ctx, gh := api.WithCallStats(context.WithValue(r.Context(), reqLogKey{}, l))
		ctx, stages := api.WithStageStats(ctx)
		r = r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w, beforeHeader: func(h http.Header) {
			h.Set("Server-Timing", serverTiming(time.Since(start), gh, stages.Snapshot(), cold))
		}}

		id := requestID()
		h := sw.Header()
		h.Set("X-Request-Id", id)
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(sw, r.Body, maxBodyBytes)
		}

		defer func() {
			if p := recover(); p != nil {
				l.outcome = "panic"
				switch {
				case sw.status != 0:
				case strings.HasPrefix(r.URL.Path, apiPrefix):
					writeJSON(sw, r, http.StatusInternalServerError, errorJSON{Error: "internal error"})
				default:
					http.Error(sw, "internal error", http.StatusInternalServerError)
				}
			}
			slog.Info("ui request",
				"request_id", id,
				"route", r.Pattern,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"user_id", l.userID,
				"repository_id", l.repoID,
				"installation_id", l.installationID,
				"outcome", l.outcome,
				// GitHub round trips of the request and their time summed: concurrent calls add up past duration_ms.
				"gh_calls", gh.Calls(),
				"gh_ms", gh.Duration().Milliseconds(),
				"stages", stages.Snapshot(),
			)
		}()
		next.ServeHTTP(sw, r)
	})
}

func requestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// signedIn requires a session and, on POST, a parsed form with the CSRF token of that session.
func (s *server) signedIn(next func(http.ResponseWriter, *http.Request, session.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.sessions.User(r)
		if !ok {
			signedOut(w, r)
			return
		}
		logOf(r).userID = u.UserID
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				s.fail(w, r, formError(err))
				return
			}
			if !validCSRF(u, r.PostForm.Get("csrf")) {
				s.fail(w, r, errCSRF)
				return
			}
		}
		next(w, r, u)
	})
}

// signedOut answers a request that has no usable session: 401 on POST and on /api (JSON there), else a redirect to
// sign in that returns here.
func signedOut(w http.ResponseWriter, r *http.Request) {
	logOf(r).outcome = "signed_out"
	if strings.HasPrefix(r.URL.Path, apiPrefix) {
		writeJSON(w, r, http.StatusUnauthorized, errorJSON{Error: "Sign in to continue."})
		return
	}
	if r.Method == http.MethodPost {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/auth/login?return="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

// endIfTokenRejected ends the session and answers as signed out when GitHub rejected the user's token.
// It reports whether it answered.
func (s *server) endIfTokenRejected(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, api.ErrUserTokenRejected) {
		return false
	}
	s.sessions.End(w)
	signedOut(w, r)
	return true
}

// formError keeps the body-limit error apart from every other unreadable form.
func formError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return err
	}
	return invalid("The form could not be read.")
}
