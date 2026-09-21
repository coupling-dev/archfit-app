package session

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	sessionCookie = "archfit_session"
	stateCookie   = "archfit_oauth"

	sessionTTL = 12 * time.Hour
	stateTTL   = 10 * time.Minute
)

type sessionPayload struct {
	Login  string `json:"login"`
	UserID int64  `json:"uid"`
	Token  string `json:"token"`
	Exp    int64  `json:"exp"` // Unix seconds
}

type statePayload struct {
	State  string `json:"state"`
	Return string `json:"return"`
	Exp    int64  `json:"exp"` // Unix seconds
}

// seal encrypts payload as nonce || ciphertext. The cookie name is the additional
// authenticated data, so a value sealed for one cookie never opens under another.
func (s *sessions) seal(name string, payload any) (string, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("session: encode cookie: %w", err)
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("session: nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, []byte(name))), nil
}

func (s *sessions) open(name, value string, out any) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	n := s.aead.NonceSize()
	if err != nil || len(raw) < n {
		return false
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(name))
	if err != nil {
		return false
	}
	return json.Unmarshal(plain, out) == nil
}

func (s *sessions) expired(exp int64) bool {
	return s.now().Unix() >= exp
}

func (s *sessions) sealSession(u User) (string, error) {
	return s.seal(sessionCookie, sessionPayload{
		Login: u.Login, UserID: u.UserID, Token: u.Token, Exp: s.now().Add(sessionTTL).Unix(),
	})
}

func (s *sessions) openSession(value string) (User, bool) {
	var p sessionPayload
	if !s.open(sessionCookie, value, &p) || s.expired(p.Exp) || p.Token == "" {
		return User{}, false
	}
	return User{Login: p.Login, UserID: p.UserID, Token: p.Token}, true
}

func (s *sessions) openState(value string) (statePayload, bool) {
	var p statePayload
	if !s.open(stateCookie, value, &p) || s.expired(p.Exp) || p.State == "" {
		return statePayload{}, false
	}
	return p, true
}

func (s *sessions) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl / time.Second),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *sessions) deleteCookie(w http.ResponseWriter, name string) {
	s.setCookie(w, name, "", -time.Second)
}
