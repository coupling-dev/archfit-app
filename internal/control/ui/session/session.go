package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
)

const (
	callbackPath = "/auth/callback"
	keySize      = 32
	stateSize    = 32
)

type sessions struct {
	aead        cipher.AEAD
	callbackURL string
	secure      bool
	oauth       auth.OAuth
	users       api.Users
	repos       api.Repos
	now         func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cacheEntry
}

// New fails unless CookieKey is 32 bytes and BaseURL is an absolute https URL (http allowed for localhost).
func New(c Config, o auth.OAuth, u api.Users, repos api.Repos, now func() time.Time) (Sessions, error) {
	if len(c.CookieKey) != keySize {
		return nil, fmt.Errorf("session: CookieKey must be %d bytes", keySize)
	}
	// The parse error is dropped on purpose: it quotes the value.
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" || base.RawQuery != "" || base.Fragment != "" || base.User != nil {
		return nil, errors.New("session: BaseURL must be an absolute https URL")
	}
	switch {
	case base.Scheme == "https":
	case base.Scheme == "http" && isLoopback(base.Hostname()):
	default:
		return nil, errors.New("session: BaseURL must use https (http only for localhost)")
	}
	if o == nil || u == nil || repos == nil || now == nil {
		return nil, errors.New("session: OAuth, Users, Repos and the clock are required")
	}
	block, err := aes.NewCipher(c.CookieKey)
	if err != nil {
		return nil, errors.New("session: CookieKey is not a usable AES key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("session: CookieKey is not a usable AES-GCM key")
	}
	return &sessions{
		aead:        aead,
		callbackURL: strings.TrimSuffix(c.BaseURL, "/") + callbackPath,
		secure:      base.Scheme == "https",
		oauth:       o,
		users:       u,
		repos:       repos,
		now:         now,
		cache:       make(map[cacheKey]cacheEntry),
	}, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *sessions) User(r *http.Request) (User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return User{}, false
	}
	return s.openSession(c.Value)
}

func (s *sessions) LoginHandler() http.Handler    { return http.HandlerFunc(s.login) }
func (s *sessions) CallbackHandler() http.Handler { return http.HandlerFunc(s.callback) }
func (s *sessions) LogoutHandler() http.Handler   { return http.HandlerFunc(s.logout) }

func (s *sessions) login(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, stateSize)
	if _, err := rand.Read(raw); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "login", "state_failed")
		return
	}
	state := hex.EncodeToString(raw)
	sealed, err := s.seal(stateCookie, statePayload{
		State:  state,
		Return: safeReturn(r.URL.Query().Get("return")),
		Exp:    s.now().Add(stateTTL).Unix(),
	})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "login", "seal_failed")
		return
	}
	s.setCookie(w, stateCookie, sealed, stateTTL)
	redirect(w, s.oauth.AuthorizeURL(state, s.callbackURL))
}

func (s *sessions) callback(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")

	var st statePayload
	ok := false
	if c, err := r.Cookie(stateCookie); err == nil {
		st, ok = s.openState(c.Value)
	}
	if !ok || code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(st.State)) != 1 {
		s.fail(w, r, http.StatusBadRequest, "callback", "bad_state_or_code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), githubTimeout)
	defer cancel()
	// The exchange error is not logged: nothing guarantees that it does not quote the code.
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil || tok.AccessToken == "" {
		s.fail(w, r, http.StatusBadGateway, "callback", "exchange_failed")
		return
	}
	sealed, err := s.sealSession(User{Login: tok.Login, UserID: tok.UserID, Token: tok.AccessToken})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "callback", "seal_failed")
		return
	}
	s.setCookie(w, sessionCookie, sealed, sessionTTL)
	s.deleteCookie(w, stateCookie)
	slog.Default().InfoContext(r.Context(), "session: callback",
		"user_id", tok.UserID,
		"outcome", "signed_in",
		"duration_ms", s.now().Sub(start).Milliseconds(),
	)
	redirect(w, safeReturn(st.Return))
}

func (s *sessions) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	s.deleteCookie(w, sessionCookie)
	redirect(w, "/")
}

// fail answers with the bare status text. Every error path ends here, without a session cookie.
func (s *sessions) fail(w http.ResponseWriter, r *http.Request, status int, step, outcome string) {
	slog.Default().WarnContext(r.Context(), "session: "+step, "outcome", outcome, "status", status)
	http.Error(w, http.StatusText(status), status)
}

// redirect is http.Redirect without the HTML body, which would repeat the target URL
// (and with it the OAuth state) in a response body.
func redirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusSeeOther)
}

// safeReturn keeps a local path and turns anything else into "/". No open redirect.
func safeReturn(p string) string {
	if p == "" || p[0] != '/' || (len(p) > 1 && (p[1] == '/' || p[1] == '\\')) {
		return "/"
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	if u, err := url.Parse(p); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return p
}
