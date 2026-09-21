// Package oidc verifies GitHub Actions OIDC tokens.
package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	issuer        = "https://token.actions.githubusercontent.com"
	algorithm     = "RS256"
	maxTokenBytes = 16 << 10
	leeway        = 60 * time.Second
	clientTimeout = 10 * time.Second
	errPrefix     = "github/oidc: "
)

// Claims are the verified GitHub Actions OIDC claims the App uses.
type Claims struct {
	Repository        string // "owner/name"
	RepositoryID      int64
	RunID             int64
	RunAttempt        int
	SHA               string
	Ref               string
	EventName         string
	JobWorkflowRef    string // "owner/name/<path>@<ref>"
	RunnerEnvironment string // "github-hosted" | "self-hosted"
}

// ErrUnavailable means the signing keys could not be fetched; the caller answers 503 so the producer retries.
var ErrUnavailable = errors.New("oidc: signing keys unavailable")

// Verifier validates signature (cached JWKS), issuer, audience, and time claims.
// A verified token authenticates the producer; it never proves the payload is true.
type Verifier interface {
	Verify(ctx context.Context, token, audience string) (Claims, error)
}

// New returns a Verifier for the GitHub Actions issuer. It makes no network call; a nil client means a client
// with a 10 s timeout.
func New(hc *http.Client) Verifier {
	return newWithClock(hc, time.Now)
}

func newWithClock(hc *http.Client, now func() time.Time) *verifier {
	if hc == nil {
		hc = &http.Client{Timeout: clientTimeout}
	}
	return &verifier{now: now, keys: keyCache{hc: hc}}
}

type verifier struct {
	now  func() time.Time
	keys keyCache
}

// failed names the failed check and nothing else: never the token, a claim value, or a key.
func failed(check string) error { return errors.New(errPrefix + check) }

func (v *verifier) Verify(ctx context.Context, token, audience string) (Claims, error) {
	switch {
	case token == "":
		return Claims{}, failed("empty token")
	case audience == "":
		return Claims{}, failed("audience")
	case len(token) > maxTokenBytes:
		return Claims{}, failed("token size")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, failed("malformed token")
	}
	headerJSON, errHeader := base64.RawURLEncoding.DecodeString(parts[0])
	payloadJSON, errPayload := base64.RawURLEncoding.DecodeString(parts[1])
	signature, errSignature := base64.RawURLEncoding.DecodeString(parts[2])
	if errHeader != nil || errPayload != nil || errSignature != nil {
		return Claims{}, failed("malformed token")
	}

	// Only alg and kid are read: jku, x5u and an embedded jwk are never honored.
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headerJSON, &header) != nil {
		return Claims{}, failed("malformed token")
	}
	if header.Alg != algorithm {
		return Claims{}, failed("algorithm")
	}
	if header.Kid == "" {
		return Claims{}, failed("kid")
	}

	now := v.now()
	key, err := v.keys.lookup(ctx, header.Kid, now)
	if err != nil {
		return Claims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return Claims{}, failed("signature")
	}
	return readClaims(payloadJSON, audience, now)
}

func unavailable() error { return fmt.Errorf(errPrefix+"keys: %w", ErrUnavailable) }
