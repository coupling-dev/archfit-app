// Package auth mints GitHub App credentials and runs the GitHub user login flow.
package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	errPrefix      = "github/auth: "
	minKeyBits     = 2048
	defaultTimeout = 10 * time.Second
)

var errKey = errors.New(errPrefix + "private key is not a PEM-encoded RSA key of at least 2048 bits")

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
	s, err := newWithClock(c, hc, time.Now)
	if err != nil {
		return nil, nil, err
	}
	return s, s, nil
}

// source implements TokenSource and OAuth over one parsed key and one HTTP client.
type source struct {
	appID        int64
	key          *rsa.PrivateKey
	clientID     string
	clientSecret string
	hc           *http.Client
	now          func() time.Time

	mu      sync.Mutex // guards entries, not the entries' fields
	entries map[int64]*entry
}

func newWithClock(c Credentials, hc *http.Client, now func() time.Time) (*source, error) {
	if c.AppID <= 0 {
		return nil, errors.New(errPrefix + "app ID must be positive")
	}
	if c.OAuthClientID == "" || c.OAuthClientSecret == "" {
		return nil, errors.New(errPrefix + "OAuth client ID and secret are required")
	}
	key, err := parsePrivateKey(c.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &source{
		appID:        c.AppID,
		key:          key,
		clientID:     c.OAuthClientID,
		clientSecret: c.OAuthClientSecret,
		hc:           hc,
		now:          now,
		entries:      map[int64]*entry{},
	}, nil
}

// String and GoString keep the key and client secret out of every fmt verb.
func (s *source) String() string   { return "github/auth" }
func (s *source) GoString() string { return s.String() }

// parsePrivateKey reads the first PEM block. Parser errors are dropped: they may quote key bytes.
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errKey
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errKey
		}
		key = k
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errKey
		}
		rsaKey, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errKey
		}
		key = rsaKey
	default:
		return nil, errKey
	}
	if key.N.BitLen() < minKeyBits {
		return nil, errKey
	}
	return key, nil
}
