package auth

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	apiHost   = "api.github.com"
	webHost   = "github.com"
	userPath  = "/user"
	tokenPath = "/login/oauth/access_token"
	tokenTTL  = 60 * time.Minute
)

var testEpoch = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

func testKeyPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey())})
}

func validCredentials() Credentials {
	return Credentials{AppID: 12345, PrivateKeyPEM: testKeyPEM(), OAuthClientID: "client-id", OAuthClientSecret: "client-secret"}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: testEpoch} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type recorded struct {
	Method string
	Host   string
	Path   string
	Header http.Header
	Body   []byte
}

// fakeGitHub is an httptest server behind a transport that keeps the fixed GitHub hosts visible to the handler.
type fakeGitHub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

func newFakeGitHub(t *testing.T, handler http.HandlerFunc) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{Method: r.Method, Host: r.Host, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Host = req.URL.Host
	r.URL.Scheme = "http"
	r.URL.Host = f.srv.Listener.Addr().String()
	return f.srv.Client().Transport.RoundTrip(r)
}

func (f *fakeGitHub) client() *http.Client { return &http.Client{Transport: f} }

func (f *fakeGitHub) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeGitHub) count() int { return len(f.requests()) }

func (f *fakeGitHub) countPath(path string) int {
	n := 0
	for _, r := range f.requests() {
		if r.Path == path {
			n++
		}
	}
	return n
}

func mintPath(id int64) string { return fmt.Sprintf("/app/installations/%d/access_tokens", id) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRaw(w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// issuer hands out a distinct token per mint, named after the request path, expiring tokenTTL after the fake clock.
type issuer struct {
	clock   *fakeClock
	mu      sync.Mutex
	n       int
	expires map[string]time.Time
}

func newIssuer(clock *fakeClock) *issuer {
	return &issuer{clock: clock, expires: map[string]time.Time{}}
}

func (i *issuer) issue(path string) (string, time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	token := fmt.Sprintf("ghs_%s_%d", strings.ReplaceAll(strings.Trim(path, "/"), "/", "-"), i.n)
	exp := i.clock.now().Add(tokenTTL)
	i.expires[token] = exp
	return token, exp
}

func (i *issuer) expiry(token string) (time.Time, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	exp, ok := i.expires[token]
	return exp, ok
}

func (i *issuer) handler(w http.ResponseWriter, r *http.Request) {
	token, exp := i.issue(r.URL.Path)
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_at": exp.Format(time.RFC3339)})
}

func newTestSource(t *testing.T, clock *fakeClock, hc *http.Client) *source {
	t.Helper()
	s, err := newWithClock(validCredentials(), hc, clock.now)
	if err != nil {
		t.Fatalf("newWithClock: %v", err)
	}
	return s
}

func mustToken(t *testing.T, s TokenSource, id int64) string {
	t.Helper()
	token, err := s.InstallationToken(t.Context(), id)
	if err != nil {
		t.Fatalf("InstallationToken(%d): %v", id, err)
	}
	if token == "" {
		t.Fatalf("InstallationToken(%d): empty token", id)
	}
	return token
}

// verifyJWT checks the shape and RS256 signature and returns the decoded header and claims.
func verifyJWT(t *testing.T, jwt string, pub *rsa.PublicKey) (header, claims map[string]any) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts, want 3", len(parts))
	}
	decoded := make([][]byte, 3)
	for i, p := range parts {
		b, err := base64.RawURLEncoding.DecodeString(p)
		if err != nil {
			t.Fatalf("JWT part %d is not unpadded base64url: %v", i, err)
		}
		decoded[i] = b
	}
	if err := json.Unmarshal(decoded[0], &header); err != nil {
		t.Fatalf("JWT header: %v", err)
	}
	if err := json.Unmarshal(decoded[1], &claims); err != nil {
		t.Fatalf("JWT claims: %v", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], decoded[2]); err != nil {
		t.Fatalf("JWT signature: %v", err)
	}
	return header, claims
}
