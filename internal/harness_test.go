package internal_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/app"
	"github.com/alexei-led/archfit-app/internal/control"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	baseURL       = "https://archfit.test"
	oidcIssuer    = "https://token.actions.githubusercontent.com"
	oidcKeyID     = "test-key"
	webhookSecret = "MARKER-webhook-secret"
	operatorToken = "MARKER-operator-token"
	oauthSecret   = "MARKER-oauth-secret"
	cookieKey     = "MARKER-cookie-key-0123456789abcd" // 32 bytes

	envelopeHeader = "X-Archfit-Envelope"

	demoInstallation = 42
	demoRepo         = "alexei-led/archfit-demo"
	demoRepoID       = 9001
	demoPull         = 42
	demoRun          = 17654321098
	workflowPath     = ".github/workflows/archfit.yaml"
	owner            = "olivia"

	checkGate     = "archfit/gate"
	checkState    = "archfit/state"
	commentMarker = "<!-- archfit:comment -->"
)

var (
	demoHead      = strings.Repeat("1f", 20)
	demoBase      = strings.Repeat("2e", 20)
	demoMergeBase = strings.Repeat("3d", 20)

	// testNow is the injected clock's start. OIDC and token expiry use the real clock; nothing else does.
	testNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
)

// ---- keys ----

func newKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

var (
	oidcKey     = sync.OnceValue(newKey) // the Actions OIDC issuer's key, published by the fake JWKS
	attackerKey = sync.OnceValue(newKey) // signs the "bad signature" token
	appKeyPEM   = sync.OnceValue(func() []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(newKey())})
	})
)

func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signed := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": oidcKeyID}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// ---- clock ----

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ---- outbound routing for app.Run, which builds its own http.Client ----

// hostRouter replaces http.DefaultTransport for the whole test binary: loopback goes to the real
// transport, the GitHub hosts go to the current test's world, and nothing else leaves the process.
type hostRouter struct {
	real    http.RoundTripper
	current atomic.Pointer[world]
}

func (h *hostRouter) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() == "127.0.0.1" {
		return h.real.RoundTrip(r)
	}
	if w := h.current.Load(); w != nil {
		return w.RoundTrip(r)
	}
	return nil, fmt.Errorf("test: outbound request to %s refused", r.URL.Host)
}

var router = &hostRouter{real: http.DefaultTransport}

// ---- log capture ----

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

// captureLogs installs a JSON logger as the slog default until the test ends. Call it before the
// system is built: modules bind slog.Default() when they are constructed.
func captureLogs(t *testing.T) (*lockedBuffer, *slog.Logger) {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	logs := &lockedBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(log)
	return logs, log
}

// ---- fixtures ----

type fixture struct {
	name     string
	report   []byte
	policy   []byte
	baseline []byte // nil when the fixture has none
	labels   []byte
}

func readFixture(t *testing.T, elem ...string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"wire", "testdata"}, elem...)...))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// loadFixture reads a report of the shared corpus with its trusted-input siblings.
func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	f := fixture{name: name, report: readFixture(t, "report", name+".json"), policy: readFixture(t, "report", name+".policy.yaml")}
	if raw, err := os.ReadFile(filepath.Join("wire", "testdata", "report", name+".baseline.json")); err == nil {
		f.baseline = raw
	}
	if raw, err := os.ReadFile(filepath.Join("wire", "testdata", "report", name+".labels.yaml")); err == nil {
		f.labels = raw
	}
	return f
}

func digest(raw []byte) string {
	if raw == nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func settingsYAML(gateMode, dataMode string) string {
	return fmt.Sprintf("version: 1\nowners: [%s]\ngate: %s\nworkflow: %s\ndata_mode: %s\n", owner, gateMode, workflowPath, dataMode)
}

// seedDemo builds the repository every shared envelope describes: installation 42, PR 42 from
// feature (1f…) into main (2e…), merge base 3d…, run 17654321098, the fixture's trusted inputs on main.
func (w *world) seedDemo(f fixture, gateMode string, private bool) {
	w.install(demoInstallation)
	w.addRepo(demoInstallation, demoRepoID, demoRepo, private)
	w.edit(demoRepo, func(rp *repo) {
		files := map[string]string{
			".archfit-app.yaml": settingsYAML(gateMode, "customer"),
			workflowPath:        "name: archfit\n",
			".archfit.yaml":     string(f.policy),
			"pkg/a/a.go":        "package a\n",
		}
		if f.baseline != nil {
			files[".archfit-baseline.json"] = string(f.baseline)
		}
		if f.labels != nil {
			files[".archfit-labels.yaml"] = string(f.labels)
		}
		rp.push("main", demoBase, files)
		rp.branches["feature"] = demoBase
		rp.push("feature", demoHead, map[string]string{"pkg/a/a.go": "package a\n\nimport _ \"example/pkg/c\"\n"})
		rp.openPull(demoPull, "feature", "main").MergeBase = demoMergeBase
		rp.runs[demoRun] = &run{
			ID: demoRun, Attempt: 1, Event: "pull_request", HeadSHA: demoHead, HeadRepo: demoRepo,
			Path: workflowPath, Branch: "feature", Status: "completed", Conclusion: "success", Pulls: []int{demoPull},
		}
	})
}

// ---- uploads ----

type envelope struct {
	SchemaVersion  string `json:"schema_version"`
	Kind           string `json:"kind"`
	Repository     string `json:"repository"`
	Event          string `json:"event"`
	PullRequest    int    `json:"pull_request"`
	HeadSHA        string `json:"head_sha"`
	BaseSHA        string `json:"base_sha"`
	MergeBaseSHA   string `json:"merge_base_sha"`
	RunID          int64  `json:"run_id"`
	RunAttempt     int    `json:"run_attempt"`
	EngineVersion  string `json:"engine_version"`
	ImageDigest    string `json:"image_digest"`
	Platform       string `json:"platform"`
	PayloadDigest  string `json:"payload_digest"`
	BaselineDigest string `json:"baseline_digest"`
	LabelsDigest   string `json:"labels_digest"`
}

// upload is one producer request. The zero overrides give a request an approved producer would send.
type upload struct {
	env    envelope
	body   []byte
	claims map[string]any
	key    *rsa.PrivateKey

	rawEnvelope string // replaces the marshalled envelope
	noToken     bool
}

// demoUpload is the upload of fixture f for the seeded demo pull request.
func demoUpload(f fixture) upload {
	now := time.Now()
	return upload{
		env: envelope{
			SchemaVersion: "archfit.report-envelope.v1", Kind: "report", Repository: demoRepo, Event: "pull_request",
			PullRequest: demoPull, HeadSHA: demoHead, BaseSHA: demoBase, MergeBaseSHA: demoMergeBase,
			RunID: demoRun, RunAttempt: 1, EngineVersion: "v2.3.1", ImageDigest: "sha256:" + strings.Repeat("a", 64),
			Platform: "linux/amd64", PayloadDigest: "sha256:" + digest(f.report),
			BaselineDigest: digest(f.baseline), LabelsDigest: digest(f.labels),
		},
		body: f.report,
		claims: map[string]any{
			"iss": oidcIssuer, "aud": baseURL, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(),
			"repository": demoRepo, "repository_id": fmt.Sprint(demoRepoID), "run_id": fmt.Sprint(int64(demoRun)),
			"run_attempt": "1", "sha": demoHead, "ref": fmt.Sprintf("refs/pull/%d/merge", demoPull),
			"event_name": "pull_request", "runner_environment": "github-hosted",
			"job_workflow_ref": fmt.Sprintf("%s/%s@refs/pull/%d/merge", demoRepo, workflowPath, demoPull),
		},
		key: oidcKey(),
	}
}

// attempt rebinds the upload to a later attempt of the same run.
func (u upload) attempt(n int) upload {
	u.env.RunAttempt = n
	claims := map[string]any{}
	for k, v := range u.claims {
		claims[k] = v
	}
	claims["run_attempt"] = fmt.Sprint(n)
	u.claims = claims
	return u
}

func (u upload) request(t *testing.T, path string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(u.body))
	env := u.rawEnvelope
	if env == "" {
		raw, err := json.Marshal(u.env)
		if err != nil {
			t.Fatal(err)
		}
		env = string(raw)
	}
	r.Header.Set(envelopeHeader, env)
	if !u.noToken {
		r.Header.Set("Authorization", "Bearer "+signJWT(t, u.key, u.claims))
	}
	return r
}

// answer is the JSON body of an upload response.
type answer struct {
	Status     int
	Conclusion string `json:"conclusion"`
	Reason     string `json:"reason"`
	Error      string `json:"error"`
}

func readAnswer(t *testing.T, resp *http.Response) answer {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	a := answer{Status: resp.StatusCode}
	_ = json.Unmarshal(raw, &a)
	return a
}

// receipt is what the gate check remembers about the upload it was decided from.
type receipt struct {
	RunID          int64  `json:"run_id"`
	RunAttempt     int    `json:"run_attempt"`
	ReportDigest   string `json:"report_digest"`
	PolicyDigest   string `json:"policy_digest"`
	BaselineDigest string `json:"baseline_digest"`
	LabelsDigest   string `json:"labels_digest"`
	Conclusion     string `json:"conclusion"`
	Reason         string `json:"reason"`
}

var receiptPattern = regexp.MustCompile(`<!-- archfit:receipt (\{.*?\}) -->`)

func (c *checkRun) receipt(t *testing.T) receipt {
	t.Helper()
	m := receiptPattern.FindAllStringSubmatch(c.Summary, -1)
	if len(m) == 0 {
		t.Fatalf("check %s has no receipt in its summary", c.Name)
	}
	var rc receipt
	if err := json.Unmarshal([]byte(m[len(m)-1][1]), &rc); err != nil {
		t.Fatalf("check %s receipt: %v", c.Name, err)
	}
	return rc
}

// ---- the composed system ----

type options struct {
	store    store.Store
	sandbox  control.Sandbox
	provider control.Provider
}

// system is the real gate and the real control plane wired as internal/app wires them, over the
// world instead of GitHub, with a clock and platform seams the test owns.
type system struct {
	t     *testing.T
	w     *world
	gate  *gate.Gate
	ctl   *control.Control
	store store.Store
	clock *clock
	mux   *http.ServeMux
}

func newSystem(t *testing.T, w *world, o options) *system {
	t.Helper()
	hc := w.client()
	tokens, oauth, err := auth.New(auth.Credentials{
		AppID: appID, PrivateKeyPEM: appKeyPEM(), OAuthClientID: "Iv1.client", OAuthClientSecret: oauthSecret,
	}, hc)
	if err != nil {
		t.Fatal(err)
	}
	if o.store == nil {
		o.store = store.NewMemory()
	}
	gh := api.New(tokens, hc)
	verifier := oidc.New(hc)
	s := &system{t: t, w: w, store: o.store, clock: &clock{now: testNow}, mux: http.NewServeMux()}
	s.gate = gate.New(gate.Deps{
		GitHub: gh, OIDC: verifier, Store: o.store, Audience: baseURL,
		Sweep: gate.Config{MissingUploadAfter: time.Hour, WaiverWarnBefore: 7 * 24 * time.Hour}, Now: s.clock.Now,
	})
	s.ctl, err = control.New(control.Deps{
		GitHub: gh, Tokens: tokens, OAuth: oauth, Store: o.store, Receiver: gate.NewReceiver(gh, verifier, baseURL),
		Sandbox: o.sandbox, Provider: o.provider, CookieKey: []byte(cookieKey), BaseURL: baseURL, Now: s.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mux.Handle("POST /v1/reports", s.gate.UploadHandler())
	s.mux.Handle("POST /v1/discoveries", s.ctl.DiscoveryHandler())
	s.mux.Handle("/", s.ctl.UIHandler())
	return s
}

func (s *system) do(r *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	return rec.Result()
}

func (s *system) upload(u upload) answer {
	s.t.Helper()
	return readAnswer(s.t, s.do(u.request(s.t, "/v1/reports")))
}

// deliver parses a webhook payload and fans it out as internal/app does: gate first, then control.
func (s *system) deliver(event string, payload any) (gateErr, controlErr error) {
	s.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		s.t.Fatal(err)
	}
	e, err := webhook.Parse(event, "delivery-"+fmt.Sprint(s.w.requestCount()), raw)
	if err != nil {
		s.t.Fatalf("webhook %s: %v", event, err)
	}
	return s.gate.HandleEvent(context.Background(), e), s.ctl.HandleEvent(context.Background(), e)
}

func (s *system) sweep() gate.Summary {
	s.t.Helper()
	sum, err := s.gate.Sweep(context.Background())
	if err != nil {
		s.t.Fatalf("gate sweep: %v", err)
	}
	return sum
}

// ---- webhook payloads ----

func repoPayload(fullName string, private bool) map[string]any {
	return map[string]any{"full_name": fullName, "private": private, "default_branch": "main"}
}

func pullPayload(action string, installationID int64, fullName, headRepo string, private bool, number int, head string) map[string]any {
	return map[string]any{
		"action": action, "number": number, "installation": map[string]any{"id": installationID},
		"repository": repoPayload(fullName, private),
		"pull_request": map[string]any{
			"number": number,
			"head":   map[string]any{"sha": head, "ref": "feature", "repo": map[string]any{"full_name": headRepo}},
			"base":   map[string]any{"ref": "main", "repo": map[string]any{"full_name": fullName}},
		},
	}
}

func installationPayload(action string, id int64) map[string]any {
	return map[string]any{"action": action, "installation": map[string]any{"id": id}}
}

func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ---- the real process entry: app.Run ----

type runningApp struct {
	t    *testing.T
	base string
	logs *lockedBuffer
}

// listenWatcher hands the bound address of app.Run to the test: the "listening" record is the only
// place the exported surface reveals it.
type listenWatcher struct {
	slog.Handler
	addr chan string
}

func (l listenWatcher) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "listening" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "addr" {
				select {
				case l.addr <- a.Value.String():
				default:
				}
			}
			return true
		})
	}
	return l.Handler.Handle(ctx, r)
}

func (l listenWatcher) WithAttrs(as []slog.Attr) slog.Handler {
	return listenWatcher{l.Handler.WithAttrs(as), l.addr}
}

func (l listenWatcher) WithGroup(name string) slog.Handler {
	return listenWatcher{l.Handler.WithGroup(name), l.addr}
}

// startApp runs the production entry point, app.Run, on a loopback port with the world behind
// http.DefaultTransport. It must not run in parallel with another startApp.
func startApp(t *testing.T, w *world) *runningApp {
	t.Helper()
	prev := slog.Default()
	router.current.Store(w)
	dir := t.TempDir()
	file := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c := app.Config{
		Addr: "127.0.0.1:0", BaseURL: baseURL, AppID: appID, OAuthClientID: "Iv1.client",
		PrivateKeyFile:    file("key.pem", appKeyPEM()),
		WebhookSecretFile: file("webhook", []byte(webhookSecret)),
		OAuthSecretFile:   file("oauth", []byte(oauthSecret)),
		CookieKeyFile:     file("cookie", []byte(cookieKey)),
		OperatorTokenFile: file("operator", []byte(operatorToken)),
		StoreDriver:       "memory", SandboxDriver: "none",
	}
	logs := &lockedBuffer{}
	addr := make(chan string, 1)
	log := slog.New(listenWatcher{slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}), addr})

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- app.Run(ctx, c, log) }()
	t.Cleanup(func() {
		cancel()
		<-finished
		router.current.Store(nil)
		slog.SetDefault(prev)
	})
	select {
	case a := <-addr:
		return &runningApp{t: t, base: "http://" + a, logs: logs}
	case err := <-finished:
		finished <- err
		t.Fatalf("app.Run did not start: %v", err)
		return nil
	}
}

// do sends r to the running process. The request keeps its https Host, as behind a TLS proxy.
func (a *runningApp) do(r *http.Request) *http.Response {
	a.t.Helper()
	u, err := url.Parse(a.base + r.URL.RequestURI())
	if err != nil {
		a.t.Fatal(err)
	}
	out := r.Clone(r.Context())
	out.URL, out.RequestURI = u, ""
	resp, err := router.real.RoundTrip(out)
	if err != nil {
		a.t.Fatalf("%s %s: %v", r.Method, r.URL.Path, err)
	}
	return resp
}

func (a *runningApp) webhook(event string, payload any) *http.Response {
	a.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		a.t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, baseURL+"/webhooks/github", bytes.NewReader(raw))
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", "delivery-1")
	r.Header.Set("X-Hub-Signature-256", sign(webhookSecret, raw))
	return a.do(r)
}

func (a *runningApp) operator(path, payload string) *http.Response {
	r := httptest.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	return a.do(r)
}

func drain(resp *http.Response) int {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// ---- a browser with a cookie map ----

type browser struct {
	t       *testing.T
	do      func(*http.Request) *http.Response
	cookies map[string]string
}

func newBrowser(t *testing.T, do func(*http.Request) *http.Response) *browser {
	return &browser{t: t, do: do, cookies: map[string]string{}}
}

func (b *browser) send(r *http.Request) (*http.Response, string) {
	b.t.Helper()
	for name, value := range b.cookies {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	resp := b.do(r)
	defer func() { _ = resp.Body.Close() }()
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func (b *browser) get(path string) (*http.Response, string) {
	b.t.Helper()
	return b.send(httptest.NewRequest(http.MethodGet, baseURL+path, nil))
}

func (b *browser) post(path string, form url.Values) (*http.Response, string) {
	b.t.Helper()
	r := httptest.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.send(r)
}

// signIn walks the OAuth login: the world's exchange accepts the login as the code.
func (b *browser) signIn(login string) {
	b.t.Helper()
	resp, _ := b.get("/auth/login")
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("state") == "" {
		b.t.Fatalf("login: status %d, Location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = b.get("/auth/callback?code=" + login + "&state=" + loc.Query().Get("state"))
	if resp.StatusCode != http.StatusSeeOther || b.cookies["archfit_session"] == "" {
		b.t.Fatalf("callback: status %d, no session", resp.StatusCode)
	}
}

var csrfPattern = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// csrf scrapes the form token from a signed-in page.
func (b *browser) csrf(path string) string {
	b.t.Helper()
	_, page := b.get(path)
	m := csrfPattern.FindStringSubmatch(page)
	if m == nil {
		b.t.Fatalf("no csrf field on %s", path)
	}
	return m[1]
}

// ---- platform fakes ----

type failingStore struct{ err error }

func (f failingStore) Get(context.Context, store.Tenant, string) ([]byte, error) { return nil, f.err }
func (f failingStore) Put(context.Context, store.Tenant, string, []byte) error   { return f.err }
func (f failingStore) Delete(context.Context, store.Tenant, string) error        { return f.err }
func (f failingStore) Add(context.Context, store.Tenant, string, int64) (int64, error) {
	return 0, f.err
}
func (f failingStore) DeleteTenant(context.Context, store.Tenant) error { return f.err }
func (f failingStore) Tenants(context.Context) ([]store.Tenant, error)  { return nil, f.err }

// recordingStore remembers every key written, so a test can prove a tenant is empty.
type recordingStore struct {
	store.Store
	mu   sync.Mutex
	keys map[store.Tenant]map[string]bool
}

func newRecordingStore() *recordingStore {
	return &recordingStore{Store: store.NewMemory(), keys: map[store.Tenant]map[string]bool{}}
}

func (s *recordingStore) note(t store.Tenant, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys[t] == nil {
		s.keys[t] = map[string]bool{}
	}
	s.keys[t][key] = true
}

func (s *recordingStore) Put(ctx context.Context, t store.Tenant, key string, value []byte) error {
	s.note(t, key)
	return s.Store.Put(ctx, t, key, value)
}

func (s *recordingStore) Add(ctx context.Context, t store.Tenant, key string, delta int64) (int64, error) {
	s.note(t, key)
	return s.Store.Add(ctx, t, key, delta)
}

func (s *recordingStore) written(t store.Tenant) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.keys[t] {
		out = append(out, k)
	}
	return out
}

type fakeSandbox struct {
	mu    sync.Mutex
	jobs  []control.DiscoveryJob
	draft []byte
}

func (f *fakeSandbox) Discover(_ context.Context, job control.DiscoveryJob) (control.DiscoveryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	return control.DiscoveryResult{DraftPolicy: f.draft}, nil
}

func (f *fakeSandbox) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.jobs)
}

type fakeProvider struct {
	mu    sync.Mutex
	calls int
	err   error
	text  string
}

func (f *fakeProvider) MaxCostMicroUSD(control.Prompt) int64 { return 1000 }

func (f *fakeProvider) Complete(context.Context, control.Prompt) (control.Completion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return control.Completion{}, f.err
	}
	return control.Completion{Text: f.text, CostMicroUSD: 500}, nil
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var errDown = errors.New("test: platform down")

// discoveryUpload is a discovery result (a draft policy) produced by a workflow_dispatch run on main.
func discoveryUpload(fullName string, repoID, runID int64, head string, policy []byte) upload {
	now := time.Now()
	return upload{
		env: envelope{
			SchemaVersion: "archfit.report-envelope.v1", Kind: "discovery", Repository: fullName, Event: "workflow_dispatch",
			HeadSHA: head, RunID: runID, RunAttempt: 1, EngineVersion: "v2.3.1",
			ImageDigest: "sha256:" + strings.Repeat("a", 64), Platform: "linux/amd64", PayloadDigest: "sha256:" + digest(policy),
		},
		body: policy,
		claims: map[string]any{
			"iss": oidcIssuer, "aud": baseURL, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(),
			"repository": fullName, "repository_id": fmt.Sprint(repoID), "run_id": fmt.Sprint(runID), "run_attempt": "1",
			"sha": head, "ref": "refs/heads/main", "event_name": "workflow_dispatch", "runner_environment": "github-hosted",
			"job_workflow_ref": fullName + "/" + workflowPath + "@refs/heads/main",
		},
		key: oidcKey(),
	}
}
