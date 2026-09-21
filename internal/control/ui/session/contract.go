// Package session is the GitHub OAuth login and the encrypted session cookie.
package session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
)

var errNotImplemented = errors.New("session: not implemented")

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

// New fails unless CookieKey is 32 bytes and BaseURL is an absolute https URL (http allowed for localhost).
func New(c Config, o auth.OAuth, u api.Users, repos api.Repos, now func() time.Time) (Sessions, error) {
	return nil, errNotImplemented
}
