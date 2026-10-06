package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/alexei-led/archfit-app/"

func startRun(t *testing.T, c Config, log *slog.Logger) (base string, network *noNetwork, stop func() error) {
	t.Helper()
	network = &noNetwork{}
	base, stop = background(t, func(ctx context.Context, ready func(net.Addr)) error {
		return run(ctx, c, log, &http.Client{Transport: network}, ready)
	})
	return base, network, stop
}

func TestOptionalDependenciesAreOptional(t *testing.T) {
	c := validConfig(t)

	st, sandbox, provider, err := platform(c, nil, http.DefaultClient)
	if err != nil {
		t.Fatalf("platform: %v", err)
	}
	if st == nil || sandbox != nil || provider != nil {
		t.Errorf("platform = (%v, %v, %v), want a store, nil sandbox, nil provider", st, sandbox, provider)
	}

	base, network, stop := startRun(t, c, slog.New(slog.DiscardHandler))
	if got := network.requests(); got != 0 {
		t.Errorf("startup made %d outbound requests, want 0", got)
	}
	if resp := send(t, httptest.NewRequest(http.MethodGet, base+"/healthz", nil)); resp.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d, want 200", resp.StatusCode)
	}
	// The real gate refuses an upload without an OIDC token; any 4xx proves the gate route serves.
	resp := send(t, httptest.NewRequest(http.MethodPost, base+"/v1/reports", strings.NewReader("{}")))
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("unauthenticated upload = %d, want a 4xx from the gate", resp.StatusCode)
	}
	if err := stop(); err != nil {
		t.Errorf("run = %v, want nil after cancellation", err)
	}

	t.Run("firestore starts without contacting Google", func(t *testing.T) {
		c := validConfig(t)
		c.StoreDriver, c.FirestoreProject = storeFirestore, "coupling-dev-510710"
		st, _, _, err := platform(c, nil, http.DefaultClient)
		if err != nil || st == nil {
			t.Fatalf("platform = %v, %v, want a Firestore store", st, err)
		}
		_, network, stop := startRun(t, c, slog.New(slog.DiscardHandler))
		if got := network.requests(); got != 0 {
			t.Errorf("startup made %d outbound requests, want 0", got)
		}
		if err := stop(); err != nil {
			t.Errorf("run = %v, want nil after cancellation", err)
		}
	})

	t.Run("a configured key selects the Anthropic provider", func(t *testing.T) {
		_, _, provider, err := platform(c, []byte(testAnthropicKey), http.DefaultClient)
		if err != nil || provider == nil {
			t.Errorf("platform = (%v, %v), want a provider", provider, err)
		}
	})
}

func TestRequiredDependenciesFailStartup(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(t *testing.T, c *Config)
		want   string // what the error must name
	}{
		{"unreadable private key file", func(t *testing.T, c *Config) { c.PrivateKeyFile = t.TempDir() + "/absent.pem" }, envPrivateKeyFile},
		{"private key is not PEM", func(t *testing.T, c *Config) {
			c.PrivateKeyFile = writeFile(t, t.TempDir(), "key.pem", []byte("MARKER-not-a-key"))
		}, "credentials"},
		{"empty webhook secret file", func(t *testing.T, c *Config) {
			c.WebhookSecretFile = writeFile(t, t.TempDir(), "webhook", []byte(" \n"))
		}, envWebhookSecretFile},
		{"empty operator token file", func(t *testing.T, c *Config) {
			c.OperatorTokenFile = writeFile(t, t.TempDir(), "operator", nil)
		}, envOperatorTokenFile},
		{"configured but empty Anthropic key file", func(t *testing.T, c *Config) {
			c.AnthropicKeyFile = writeFile(t, t.TempDir(), "anthropic", nil)
		}, envAnthropicKeyFile},
		{"missing base URL", func(_ *testing.T, c *Config) { c.BaseURL = "" }, envBaseURL},
		{"missing app ID", func(_ *testing.T, c *Config) { c.AppID = 0 }, envAppID},
		{"unknown store driver", func(_ *testing.T, c *Config) { c.StoreDriver = "redis" }, envStore},
		{"unknown sandbox driver", func(_ *testing.T, c *Config) { c.SandboxDriver = "docker" }, envSandbox},
		{"firestore without a project", func(_ *testing.T, c *Config) { c.StoreDriver = storeFirestore }, envFirestoreProject},
		{"firestore with an invalid project", func(_ *testing.T, c *Config) {
			c.StoreDriver, c.FirestoreProject = storeFirestore, "MARKER/project"
		}, "invalid project ID"},
		{"firestore with an invalid database", func(_ *testing.T, c *Config) {
			c.StoreDriver, c.FirestoreProject, c.FirestoreDatabase = storeFirestore, "coupling-dev-510710", "MARKER/db"
		}, "invalid database ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig(t)
			tt.break_(t, &c)
			listening := false
			err := run(t.Context(), c, slog.New(slog.DiscardHandler), &http.Client{Transport: &noNetwork{}},
				func(net.Addr) { listening = true })
			if err == nil || listening {
				t.Fatalf("run = %v, listening = %v; want an error before listening", err, listening)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not name %s", err, tt.want)
			}
			if strings.Contains(err.Error(), "MARKER") {
				t.Errorf("error %q echoes secret content", err)
			}
		})
	}

	t.Run("unknown drivers are refused by platform too", func(t *testing.T) {
		for _, c := range []Config{{StoreDriver: "redis", SandboxDriver: sandboxNone}, {StoreDriver: storeMemory, SandboxDriver: "docker"}} {
			if _, _, _, err := platform(c, nil, http.DefaultClient); err == nil {
				t.Errorf("platform(%+v) = nil error", c)
			}
		}
	})
}

func TestGracefulShutdown(t *testing.T) {
	f := newFixture()
	started, release := make(chan struct{}), make(chan struct{})
	f.gate.upload = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusCreated)
	})
	f.rebuild()
	base, stop := background(t, func(ctx context.Context, ready func(net.Addr)) error {
		return serve(ctx, "127.0.0.1:0", f.handler, slog.Default(), ready)
	})

	status := make(chan int, 1)
	go func() {
		resp, err := http.DefaultTransport.RoundTrip(httptest.NewRequest(http.MethodPost, base+"/v1/reports", nil))
		if err != nil {
			status <- 0
			return
		}
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-started

	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	// Shutdown closes the listener first: once a dial is refused, the server is draining.
	for {
		conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
		if err != nil {
			break
		}
		_ = conn.Close()
	}
	select {
	case err := <-stopped:
		t.Fatalf("serve returned %v while an upload was in flight", err)
	default:
	}

	close(release)
	if got := <-status; got != http.StatusCreated {
		t.Errorf("in-flight upload = %d, want 201", got)
	}
	if err := <-stopped; err != nil {
		t.Errorf("serve = %v, want nil", err)
	}
}

// Not parallel: run installs the capturing logger as the slog default.
func TestLogsNeverContainSecretsOrBodies(t *testing.T) {
	c := validConfig(t)
	c.AnthropicKeyFile = writeFile(t, t.TempDir(), "anthropic", []byte(testAnthropicKey))
	logs := &lockedBuffer{}
	base, network, stop := startRun(t, c, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	withAuth := func(r *http.Request, token string) *http.Request {
		r.Header.Set("Authorization", bearerPrefix+token)
		r.AddCookie(&http.Cookie{Name: "session", Value: "MARKER-cookie"})
		return r
	}
	event := []byte(pullOpenedBody)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, base+"/healthz?q=MARKER-query", nil),
		withAuth(httptest.NewRequest(http.MethodPost, base+"/v1/reports", strings.NewReader(bodyMarker)), "MARKER-oidc-token"),
		withAuth(httptest.NewRequest(http.MethodPost, base+"/v1/discoveries", strings.NewReader(bodyMarker)), "MARKER-oidc-token"),
		webhookRequest(base+"/webhooks/github", "pull_request", "d-1", event, sign(testWebhookSecret, event)),
		webhookRequest(base+"/webhooks/github", "pull_request", "d-2", event, sign("MARKER-wrong-secret", event)),
		webhookRequest(base+"/webhooks/github", "pull_request", "d-3", []byte(bodyMarker), sign(testWebhookSecret, []byte(bodyMarker))),
		operatorRequest(base+"/internal/sweep", testOperatorToken, bodyMarker),
		operatorRequest(base+"/internal/sweep", "MARKER-wrong-token", bodyMarker),
		operatorRequest(base+"/internal/grants", testOperatorToken, `{"installation_id": 42, "until": "2999-01-01T00:00:00Z"}`),
		operatorRequest(base+"/internal/grants", testOperatorToken, `{"installation_id": "`+bodyMarker+`"}`),
		withAuth(httptest.NewRequest(http.MethodGet, base+"/", nil), "MARKER-user-token"),
		withAuth(httptest.NewRequest(http.MethodGet, base+"/MARKER-path/settings", nil), "MARKER-user-token"),
		withAuth(httptest.NewRequest(http.MethodPost, base+"/MARKER-path", strings.NewReader(bodyMarker)), "MARKER-user-token"),
	}
	for _, r := range requests {
		send(t, r)
	}
	if err := stop(); err != nil {
		t.Fatalf("run = %v", err)
	}
	if network.requests() == 0 {
		t.Error("no handler reached the outbound client; the scenario exercises less than it claims")
	}

	got := logs.String()
	if n := strings.Count(got, `"msg":"request"`); n != len(requests) {
		t.Errorf("request log lines = %d, want %d", n, len(requests))
	}
	pemBody := strings.Split(strings.TrimSpace(string(testPrivateKeyPEM())), "\n")
	for _, marker := range []string{"MARKER", pemBody[1], pemBody[len(pemBody)-2]} {
		if strings.Contains(got, marker) {
			t.Errorf("logs contain %q", marker)
		}
	}
	for _, want := range []string{`"request_id"`, `"route":"POST /webhooks/github"`, `"status":202`, `"duration_ms"`, `"installation_id":42`, `"outcome"`} {
		if !strings.Contains(got, want) {
			t.Errorf("logs lack %s", want)
		}
	}
}

func TestImportsStayInsideTheDesign(t *testing.T) {
	allowed := map[string]bool{}
	for _, p := range []string{"gate", "control", "control/assist/anthropic", "store", "store/firestore", "gcptoken", "github/auth", "github/api", "github/webhook", "github/oidc"} {
		allowed[modulePath+"internal/"+p] = true
	}
	out, err := exec.CommandContext(t.Context(), "go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for imp := range strings.FieldsSeq(string(out)) {
		// Standard library import paths have no dot in their first element.
		first, _, _ := strings.Cut(imp, "/")
		if strings.Contains(first, ".") && !allowed[imp] {
			t.Errorf("import %s is not listed under Integrations", imp)
		}
	}
}
