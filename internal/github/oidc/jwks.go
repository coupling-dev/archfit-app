package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

const (
	jwksURL          = issuer + "/.well-known/jwks"
	keyTTL           = time.Hour
	maxStale         = 24 * time.Hour
	minFetchInterval = time.Minute
	maxJWKSBytes     = 1 << 20
	minModulusBits   = 2048
)

var errFetch = errors.New("jwks fetch failed")

// keyCache holds the issuer's key set. One mutex covers the cache and the fetch, so fetches are single-flight;
// callers waiting on it are bounded by the client timeout.
type keyCache struct {
	hc *http.Client

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time // last successful fetch
	lastAttempt time.Time
	lastFailed  bool
}

func (c *keyCache) lookup(ctx context.Context, kid string, now time.Time) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.keys == nil || now.Sub(c.fetchedAt) >= keyTTL {
		c.refresh(ctx, now)
	}
	if _, ok := c.keys[kid]; !ok {
		c.refresh(ctx, now)
	}
	if now.Sub(c.fetchedAt) >= maxStale {
		c.keys = nil
	}
	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	if c.keys == nil || c.lastFailed {
		return nil, unavailable()
	}
	return nil, failed("unknown key")
}

// refresh fetches the key set at most once per minFetchInterval, whatever the reason, so neither a flood of
// unknown kid values nor an outage becomes a fetch flood. A failed fetch keeps the cached set.
func (c *keyCache) refresh(ctx context.Context, now time.Time) {
	if !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < minFetchInterval {
		return
	}
	keys, err := c.fetch(ctx)
	if err != nil && ctx.Err() != nil {
		// The caller gave up; that says nothing about the issuer, so the next caller may try at once.
		c.lastFailed = true
		return
	}
	c.lastAttempt = now
	c.lastFailed = err != nil
	if err == nil {
		c.keys, c.fetchedAt = keys, now
	}
}

func (c *keyCache) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, errFetch
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, errFetch
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errFetch
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil || len(body) > maxJWKSBytes {
		return nil, errFetch
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if json.Unmarshal(body, &set) != nil {
		return nil, errFetch
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if pub := k.rsaKey(); pub != nil {
			keys[k.Kid] = pub
		}
	}
	if len(keys) == 0 {
		return nil, errFetch
	}
	return keys, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// rsaKey returns nil for every key that is not a usable RS256 signing key.
func (k jwk) rsaKey() *rsa.PublicKey {
	if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != algorithm) {
		return nil
	}
	nBytes, errN := base64.RawURLEncoding.DecodeString(k.N)
	eBytes, errE := base64.RawURLEncoding.DecodeString(k.E)
	if errN != nil || errE != nil {
		return nil
	}
	n, e := new(big.Int).SetBytes(nBytes), new(big.Int).SetBytes(eBytes)
	if n.BitLen() < minModulusBits || e.BitLen() > 31 || e.Int64() < 3 || e.Bit(0) == 0 {
		return nil
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}
}
