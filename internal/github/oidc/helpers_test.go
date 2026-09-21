package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const (
	testAudience = "https://archfit.example.com"
	testKid      = "k1"
	fixedJWKSURL = "https://token.actions.githubusercontent.com/.well-known/jwks"
)

var testStart = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

type testKeys struct {
	valid, rotated, attacker, weak *rsa.PrivateKey
}

var (
	keysOnce   sync.Once
	sharedKeys testKeys
	errKeys    error
)

func loadKeys(t *testing.T) testKeys {
	t.Helper()
	keysOnce.Do(func() {
		for _, k := range []struct {
			dst  **rsa.PrivateKey
			bits int
		}{
			{&sharedKeys.valid, 2048},
			{&sharedKeys.rotated, 2048},
			{&sharedKeys.attacker, 2048},
			{&sharedKeys.weak, 1024},
		} {
			key, err := rsa.GenerateKey(rand.Reader, k.bits)
			if err != nil {
				errKeys = err
				return
			}
			*k.dst = key
		}
	})
	if errKeys != nil {
		t.Fatalf("generate test keys: %v", errKeys)
	}
	return sharedKeys
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeIssuer is the JWKS endpoint: an http.RoundTripper, so no request ever reaches the network.
type fakeIssuer struct {
	mu     sync.Mutex
	status int
	body   string
	err    error
	delay  time.Duration
	urls   []string
}

func (f *fakeIssuer) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.urls = append(f.urls, req.URL.String())
	status, body, err, delay := f.status, f.body, f.err, f.delay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	rec.WriteHeader(status)
	_, _ = rec.WriteString(body)
	return rec.Result(), nil
}

func (f *fakeIssuer) serve(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body, f.err = status, body, nil
}

func (f *fakeIssuer) down() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = errors.New("connection refused")
}

func (f *fakeIssuer) fetches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.urls)
}

func (f *fakeIssuer) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

type harness struct {
	v      *verifier
	clock  *fakeClock
	issuer *fakeIssuer
	keys   testKeys
}

// newHarness serves the valid key as testKid on a cold verifier.
func newHarness(t *testing.T) *harness {
	t.Helper()
	keys := loadKeys(t)
	h := &harness{clock: &fakeClock{now: testStart}, issuer: &fakeIssuer{}, keys: keys}
	h.issuer.serve(http.StatusOK, jwksBody(t, rsaJWK(testKid, &keys.valid.PublicKey)))
	h.v = newWithClock(&http.Client{Transport: h.issuer}, h.clock.Now)
	return h
}

// warm verifies one good token so the key set is cached.
func (h *harness) warm(t *testing.T) {
	t.Helper()
	if _, err := h.verify(h.goodToken(t)); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
}

func (h *harness) verify(token string) (Claims, error) {
	return h.v.Verify(context.Background(), token, testAudience)
}

func (h *harness) goodToken(t *testing.T) string {
	t.Helper()
	return rs256Token(t, h.keys.valid, testKid, goodClaims(h.clock.Now()))
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWK(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func jwksBody(t *testing.T, keys ...map[string]any) string {
	t.Helper()
	return string(mustJSON(t, map[string]any{"keys": keys}))
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss":                "https://token.actions.githubusercontent.com",
		"aud":                testAudience,
		"exp":                now.Add(5 * time.Minute).Unix(),
		"nbf":                now.Add(-time.Minute).Unix(),
		"iat":                now.Unix(),
		"repository":         "o/r",
		"repository_id":      "4242",
		"run_id":             "987654321",
		"run_attempt":        "1",
		"sha":                "0123456789abcdef0123456789abcdef01234567",
		"ref":                "refs/pull/7/merge",
		"event_name":         "pull_request",
		"job_workflow_ref":   "o/r/.github/workflows/archfit.yaml@refs/pull/7/merge",
		"runner_environment": "github-hosted",
	}
}

func with(claims map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(claims)+1)
	for k, v := range claims {
		out[k] = v
	}
	out[key] = value
	return out
}

func without(claims map[string]any, key string) map[string]any {
	out := with(claims, key, nil)
	delete(out, key)
	return out
}

// signedToken signs header.payload with RS256 whatever the header claims.
func signedToken(t *testing.T, key *rsa.PrivateKey, header map[string]any, payload []byte) string {
	t.Helper()
	input := b64(mustJSON(t, header)) + "." + b64(payload)
	return input + "." + b64(rsaSign(t, key, input))
}

func rsaSign(t *testing.T, key *rsa.PrivateKey, input string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

func rs256Token(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	return signedToken(t, key, map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}, mustJSON(t, claims))
}

func wantOrdinaryError(t *testing.T, got Claims, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("want an ordinary error, got ErrUnavailable: %v", err)
	}
	if got != (Claims{}) {
		t.Errorf("want zero Claims on error, got %+v", got)
	}
}

func wantUnavailable(t *testing.T, got Claims, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable, got %v", err)
	}
	if got != (Claims{}) {
		t.Errorf("want zero Claims on error, got %+v", got)
	}
}
