package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

const (
	testWebhookSecret = "MARKER-webhook-secret"
	testOperatorToken = "MARKER-operator-token"
	testOAuthSecret   = "MARKER-oauth-secret"
	testCookieKey     = "MARKER-cookie-key-0123456789abcd" // 32 bytes
	testAnthropicKey  = "MARKER-anthropic-key"
	bodyMarker        = "MARKER-request-body"
)

var testNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

// callLog records the order in which the fakes were reached.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, name)
}

func (l *callLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

func namedHandler(l *callLog, name string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		l.add(name)
		w.WriteHeader(status)
	})
}

type fakeGate struct {
	log      *callLog
	upload   http.Handler
	events   []webhook.Event
	pending  map[string]bool // head SHA → pending check; idempotent like the real gate
	summary  gate.Summary
	eventErr error
	sweepErr error
}

func newFakeGate(l *callLog) *fakeGate {
	return &fakeGate{log: l, upload: namedHandler(l, "gate.Upload", http.StatusCreated), pending: map[string]bool{}}
}

func (g *fakeGate) UploadHandler() http.Handler { return g.upload }

func (g *fakeGate) HandleEvent(_ context.Context, e webhook.Event) error {
	g.log.add("gate.HandleEvent")
	g.events = append(g.events, e)
	g.pending[e.HeadSHA] = true
	return g.eventErr
}

func (g *fakeGate) Sweep(context.Context) (gate.Summary, error) {
	g.log.add("gate.Sweep")
	return g.summary, g.sweepErr
}

type grantCall struct {
	installationID int64
	until          time.Time
}

type fakeControl struct {
	log        *callLog
	ui         http.Handler
	events     []webhook.Event
	grants     []grantCall
	eventErr   error
	sweepErr   error
	eventPanic bool
	sweepPanic bool
}

func newFakeControl(l *callLog) *fakeControl {
	return &fakeControl{log: l, ui: namedHandler(l, "control.UI", http.StatusOK)}
}

func (c *fakeControl) UIHandler() http.Handler { return c.ui }

func (c *fakeControl) DiscoveryHandler() http.Handler {
	return namedHandler(c.log, "control.Discovery", http.StatusAccepted)
}

func (c *fakeControl) HandleEvent(_ context.Context, e webhook.Event) error {
	c.log.add("control.HandleEvent")
	c.events = append(c.events, e)
	if c.eventPanic {
		panic("control event panic")
	}
	return c.eventErr
}

func (c *fakeControl) Sweep(context.Context) error {
	c.log.add("control.Sweep")
	if c.sweepPanic {
		panic("control sweep panic")
	}
	return c.sweepErr
}

func (c *fakeControl) Grant(_ context.Context, installationID int64, until time.Time) error {
	c.log.add("control.Grant")
	c.grants = append(c.grants, grantCall{installationID, until})
	return nil
}

// lockedBuffer lets the test read logs that server goroutines write.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fixture struct {
	calls   *callLog
	gate    *fakeGate
	control *fakeControl
	logs    *lockedBuffer
	handler http.Handler
}

// newFixture builds the HTTP assembly over fakes. Set fields on gate and control before the first request;
// call rebuild after replacing a handler the façade returns.
func newFixture() *fixture {
	l := &callLog{}
	f := &fixture{calls: l, gate: newFakeGate(l), control: newFakeControl(l), logs: &lockedBuffer{}}
	f.rebuild()
	return f
}

func (f *fixture) rebuild() {
	f.handler = newHandler(handlerDeps{
		gate:          f.gate,
		control:       f.control,
		webhookSecret: []byte(testWebhookSecret),
		operatorToken: []byte(testOperatorToken),
		now:           func() time.Time { return testNow },
		log:           slog.New(slog.NewJSONHandler(f.logs, nil)),
	})
}

func (f *fixture) do(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	return rec
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func webhookRequest(target, event, delivery string, body []byte, signature string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	r.Header.Set(headerEvent, event)
	r.Header.Set(headerDelivery, delivery)
	r.Header.Set(headerSignature, signature)
	return r
}

func operatorRequest(target, token, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", bearerPrefix+token)
	}
	return r
}

const pullOpenedBody = `{
  "action": "opened",
  "number": 7,
  "pull_request": {
    "number": 7,
    "title": "` + bodyMarker + `",
    "head": {"sha": "0123456789abcdef0123456789abcdef01234567", "ref": "feature", "repo": {"full_name": "octo/repo"}},
    "base": {"ref": "main", "repo": {"full_name": "octo/repo"}}
  },
  "repository": {"full_name": "octo/repo", "private": true},
  "installation": {"id": 42}
}`

// noNetwork answers every outbound request with a local 404 and counts them, so no test contacts a
// remote service. A response rather than a transport error keeps the API client's retry backoff out of the tests.
type noNetwork struct {
	mu    sync.Mutex
	count int
}

func (n *noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.count++
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)),
		Request:    r,
	}, nil
}

func (n *noNetwork) requests() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.count
}

var testPrivateKeyPEM = sync.OnceValue(func() []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
})

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// validConfig writes every secret to a temp file and returns a Config that starts.
func validConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Addr:              "127.0.0.1:0",
		BaseURL:           "https://archfit.example",
		AppID:             12345,
		PrivateKeyFile:    writeFile(t, dir, "key.pem", testPrivateKeyPEM()),
		WebhookSecretFile: writeFile(t, dir, "webhook", []byte(testWebhookSecret+"\n")),
		OAuthClientID:     "Iv1.client",
		OAuthSecretFile:   writeFile(t, dir, "oauth", []byte(testOAuthSecret+"\n")),
		CookieKeyFile:     writeFile(t, dir, "cookie", []byte(testCookieKey)),
		OperatorTokenFile: writeFile(t, dir, "operator", []byte(testOperatorToken+"\n")),
		StoreDriver:       storeMemory,
		SandboxDriver:     sandboxNone,
	}
}

// background runs f (run or serve) and returns the bound base URL plus a stop function that cancels
// the context and returns f's result. The previous slog default is restored afterwards.
func background(t *testing.T, f func(ctx context.Context, ready func(net.Addr)) error) (string, func() error) {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan net.Addr, 1)
	finished := make(chan struct{})
	var result error
	go func() {
		defer close(finished)
		result = f(ctx, func(a net.Addr) { addrc <- a })
	}()
	stop := func() error {
		cancel()
		<-finished
		return result
	}
	t.Cleanup(func() { _ = stop() })
	select {
	case a := <-addrc:
		return "http://" + a.String(), stop
	case <-finished:
		t.Fatalf("did not start: %v", result)
		return "", nil
	}
}

func send(t *testing.T, r *http.Request) *http.Response {
	t.Helper()
	r.RequestURI = ""
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		t.Fatalf("%s %s: %v", r.Method, r.URL, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}
