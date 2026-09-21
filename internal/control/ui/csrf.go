package ui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
)

const csrfMessage = "archfit-csrf"

// csrfToken is stateless and per session: it changes whenever the user's OAuth token does.
func csrfToken(u session.User) string {
	mac := hmac.New(sha256.New, []byte(u.Token))
	mac.Write([]byte(csrfMessage))
	return hex.EncodeToString(mac.Sum(nil))
}

func validCSRF(u session.User, got string) bool {
	return u.Token != "" && hmac.Equal([]byte(got), []byte(csrfToken(u)))
}
