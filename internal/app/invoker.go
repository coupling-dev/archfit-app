package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// googleIssuers are the two spellings of the issuer of Google-signed ID tokens.
var googleIssuers = map[string]bool{"https://accounts.google.com": true, "accounts.google.com": true}

// invokerOnly admits a worker request only when it carries an ID token of the configured invoker for the
// worker audience. Cloud Run's invoker check verifies the token signature before the request reaches the
// container, and Cloud Run can remove the signature before it passes the token on (SIGNATURE_REMOVED_BY_GOOGLE),
// so this check reads the claims only. It is the App's own refusal of any other caller, a guard in depth behind
// internal ingress, the invoker check and the queue routing; it does not replace the invoker check.
func (s *server) invokerOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.fromInvoker(r) {
			info(r).outcome = "not_invoker"
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) fromInvoker(r *http.Request) bool {
	if s.invoker == "" || s.workerURL == "" {
		return false
	}
	raw := r.Header.Get(headerServerlessAuth)
	if raw == "" {
		raw = r.Header.Get("Authorization")
	}
	token, ok := strings.CutPrefix(raw, bearerPrefix)
	if !ok {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var c struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Exp           int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &c) != nil {
		return false
	}
	return googleIssuers[c.Iss] && c.Aud == s.workerURL && c.Email == s.invoker && c.EmailVerified &&
		s.now().Before(time.Unix(c.Exp, 0))
}
