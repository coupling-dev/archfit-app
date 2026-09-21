// Package auth mints GitHub App credentials and runs the GitHub user login flow.
package auth

import (
	"context"
	"errors"
	"net/http"
)

var errNotImplemented = errors.New("auth: not implemented")

// Credentials are loaded by the composition root from secret references, never inline.
type Credentials struct {
	AppID             int64
	PrivateKeyPEM     []byte
	OAuthClientID     string
	OAuthClientSecret string
}

// TokenSource mints GitHub App credentials. Installation tokens are cached until 5 minutes before expiry.
type TokenSource interface {
	AppJWT() (string, error)
	InstallationToken(ctx context.Context, installationID int64) (string, error)
	// Invalidate drops the cached installation token (called after a 401).
	Invalidate(installationID int64)
	// RepoReadToken mints an uncached token limited to contents:read on one repository.
	RepoReadToken(ctx context.Context, installationID int64, fullName string) (string, error)
}

type UserToken struct {
	AccessToken string
	Login       string
	UserID      int64
}

// OAuth is the GitHub user login flow.
type OAuth interface {
	AuthorizeURL(state, redirectURL string) string
	Exchange(ctx context.Context, code string) (UserToken, error)
}

func New(c Credentials, hc *http.Client) (TokenSource, OAuth, error) {
	return nil, nil, errNotImplemented
}
