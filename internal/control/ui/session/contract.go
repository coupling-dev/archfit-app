// Package session is the GitHub OAuth login and the encrypted session cookie.
package session

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

type User struct {
	Login  string
	UserID int64
	Token  string // OAuth access token; lives only in the encrypted, signed cookie
}

type Sessions interface {
	// LoginHandler and CallbackHandler run the GitHub OAuth flow with a state cookie.
	LoginHandler() http.Handler
	CallbackHandler() http.Handler
	LogoutHandler() http.Handler
	// User returns the signed-in user, or false.
	User(r *http.Request) (User, bool)
	// Authorize returns the repository if the user has at least minPermission ("read" | "write" | "admin")
	// on it right now (cached for 60 seconds). The Repo carries the authoritative installation ID.
	Authorize(ctx context.Context, u User, fullName, minPermission string) (api.Repo, error)
}

var ErrForbidden = errors.New("session: not permitted")

type Config struct {
	CookieKey []byte // 32 bytes, from a secret reference
	BaseURL   string
}
