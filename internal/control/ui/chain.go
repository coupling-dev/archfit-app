package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	maxBodyBytes = 512 << 10

	permRead  = "read"
	permWrite = "write"
	permAdmin = "admin"

	// contentSecurityPolicy is the same string as web/csp.txt, which the web app's preview server and smoke test use;
	// TestWebPreviewUsesTheProductionCSP keeps them equal.
	contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"
)

// scope is what the chain hands to a page: the signed-in user and, on repository routes, the authorized
// repository. It is the only tenant identity a page may use.
type scope struct {
	user session.User
	repo api.Repo
}

type pageFunc func(w http.ResponseWriter, r *http.Request, sc scope)

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
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// chain is the part of the handler chain every route shares: recover, security headers, body limit, log line.
func chain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		l := &reqLog{outcome: "ok"}
		r = r.WithContext(context.WithValue(r.Context(), reqLogKey{}, l))
		sw := &statusWriter{ResponseWriter: w}

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
				s.fail(w, r, scope{user: u}, formError(err))
				return
			}
			if !validCSRF(u, r.PostForm.Get("csrf")) {
				s.fail(w, r, scope{user: u}, errCSRF)
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

// repo authorizes the {o}/{n} of the path at minPermission on every request and hands the Repo to the page.
func (s *server) repo(minPermission string, next pageFunc) http.Handler {
	return s.signedIn(func(w http.ResponseWriter, r *http.Request, u session.User) {
		sc := scope{user: u}
		repo, err := s.sessions.Authorize(r.Context(), u, r.PathValue("o")+"/"+r.PathValue("n"), minPermission)
		if err != nil {
			if !s.endIfTokenRejected(w, r, err) {
				s.fail(w, r, sc, err)
			}
			return
		}
		l := logOf(r)
		l.repoID, l.installationID = repo.ID, repo.InstallationID
		sc.repo = repo
		next(w, r, sc)
	})
}
