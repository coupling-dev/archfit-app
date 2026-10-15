package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const modulePath = "github.com/alexei-led/archfit-app/"

func startRun(t *testing.T, c Config, log *slog.Logger) (base string, network *noNetwork, stop func() error) {
	t.Helper()
	network = &noNetwork{}
	base, stop = background(t, func(ctx context.Context, ready func(net.Addr)) error {
		return run(ctx, c, log, &http.Client{Transport: network}, fakeGoogleTokens{}, ready)
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

	t.Run("a previous webhook secret is accepted next to the current one", func(t *testing.T) {
		c := validConfig(t)
		c.WebhookPreviousFile = writeFile(t, t.TempDir(), "webhook-previous", []byte(testWebhookPrevious+"\n"))
		base, _, stop := startRun(t, c, slog.New(slog.DiscardHandler))
		event := []byte(`{"zen":"ping","hook_id":1}`)
		for _, tt := range []struct {
			secret string
			want   int
		}{{testWebhookSecret, http.StatusAccepted}, {testWebhookPrevious, http.StatusAccepted}, {"MARKER-neither", http.StatusUnauthorized}} {
			resp := send(t, webhookRequest(base+"/webhooks/github", "ping", "d-7", event, sign(tt.secret, event)))
			if resp.StatusCode != tt.want {
				t.Errorf("signed with %s: status = %d, want %d", tt.secret, resp.StatusCode, tt.want)
			}
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
		{"empty previous webhook secret file", func(t *testing.T, c *Config) {
			c.WebhookPreviousFile = writeFile(t, t.TempDir(), "webhook-previous", []byte("\n"))
		}, envWebhookPreviousFile},
		{"unreadable previous webhook secret file", func(t *testing.T, c *Config) {
			c.WebhookPreviousFile = t.TempDir() + "/absent"
		}, envWebhookPreviousFile},
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
			err := run(t.Context(), c, slog.New(slog.DiscardHandler), &http.Client{Transport: &noNetwork{}}, nil,
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
	for _, p := range []string{"gate", "control", "control/assist/anthropic", "store", "store/firestore", "gcptoken", "github/auth", "github/api", "github/webhook", "github/oidc", "queue", "queue/cloudtasks"} {
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

// secretField is one secret setting of Config: the variable the startup error names, and the field it sets.
type secretField struct {
	env string
	set func(c *Config, v string)
}

var (
	privateKeyField = secretField{envPrivateKeyFile, func(c *Config, v string) { c.PrivateKeyFile = v }}
	webhookField    = secretField{envWebhookSecretFile, func(c *Config, v string) { c.WebhookSecretFile = v }}
	oauthIDField    = secretField{envOAuthClientID, func(c *Config, v string) { c.OAuthClientID = v }}
	oauthField      = secretField{envOAuthSecretFile, func(c *Config, v string) { c.OAuthSecretFile = v }}
	cookieField     = secretField{envCookieKeyFile, func(c *Config, v string) { c.CookieKeyFile = v }}
	operatorField   = secretField{envOperatorTokenFile, func(c *Config, v string) { c.OperatorTokenFile = v }}
	anthropicField  = secretField{envAnthropicKeyFile, func(c *Config, v string) { c.AnthropicKeyFile = v }}
)

// roleConfig is validConfig for role with only the given secret settings kept; every other one is empty.
func roleConfig(t *testing.T, role string, own []secretField) Config {
	t.Helper()
	full := validConfig(t)
	c := full
	for _, f := range []secretField{privateKeyField, webhookField, oauthIDField, oauthField, cookieField, operatorField, anthropicField} {
		f.set(&c, "")
	}
	for _, f := range own {
		f.set(&c, map[string]string{
			envPrivateKeyFile: full.PrivateKeyFile, envWebhookSecretFile: full.WebhookSecretFile,
			envOAuthClientID: full.OAuthClientID, envOAuthSecretFile: full.OAuthSecretFile,
			envCookieKeyFile: full.CookieKeyFile, envOperatorTokenFile: full.OperatorTokenFile,
		}[f.env])
	}
	c.Role = role
	if role == roleWorker || role == rolePublic {
		c.WorkerURL, c.Invoker = testWorkerURL, testInvoker
	}
	if role == rolePublic {
		c.QueueDriver = queueCloudTasks
		c.TasksQueue = "projects/coupling-test/locations/europe-west1/queues/webhooks"
	}
	return c
}

func TestEachRoleStartsWithOnlyItsSecrets(t *testing.T) {
	roles := []struct {
		role    string
		own     []secretField
		foreign []secretField // never read, even when configured
	}{
		{roleAll, []secretField{privateKeyField, webhookField, oauthIDField, oauthField, cookieField, operatorField}, nil},
		{rolePublic, []secretField{privateKeyField, webhookField, oauthIDField, oauthField, cookieField}, []secretField{operatorField}},
		{roleWorker, []secretField{privateKeyField, webhookField, operatorField}, []secretField{oauthField, cookieField, anthropicField}},
	}
	for _, r := range roles {
		t.Run(r.role+" starts with only its own secrets", func(t *testing.T) {
			base, network, stop := startRun(t, roleConfig(t, r.role, r.own), slog.New(slog.DiscardHandler))
			if resp := send(t, httptest.NewRequest(http.MethodGet, base+"/healthz", nil)); resp.StatusCode != http.StatusOK {
				t.Errorf("healthz = %d, want 200", resp.StatusCode)
			}
			if got := network.requests(); got != 0 {
				t.Errorf("startup made %d outbound requests, want 0", got)
			}
			if err := stop(); err != nil {
				t.Errorf("run = %v", err)
			}
		})
		if len(r.foreign) > 0 {
			t.Run(r.role+" never reads a foreign secret", func(t *testing.T) {
				c := roleConfig(t, r.role, r.own)
				for _, f := range r.foreign {
					f.set(&c, "/nonexistent/"+f.env)
				}
				_, _, stop := startRun(t, c, slog.New(slog.DiscardHandler))
				if err := stop(); err != nil {
					t.Errorf("run = %v", err)
				}
			})
		}
		for _, missing := range r.own {
			for _, value := range []string{"", "/nonexistent/" + missing.env} {
				if missing.env == envOAuthClientID && value != "" {
					continue // the client ID is a value, not a file
				}
				t.Run(r.role+" fails closed without "+missing.env+" "+strconv.Quote(value), func(t *testing.T) {
					c := roleConfig(t, r.role, r.own)
					missing.set(&c, value)
					listening := false
					err := run(t.Context(), c, slog.New(slog.DiscardHandler), &http.Client{Transport: &noNetwork{}}, nil,
						func(net.Addr) { listening = true })
					if err == nil || listening || !strings.Contains(err.Error(), missing.env) {
						t.Errorf("run = %v, listening = %v; want an error naming %s before listening", err, listening, missing.env)
					}
				})
			}
		}
	}

	t.Run("the public role serves no operator route", func(t *testing.T) {
		base, _, stop := startRun(t, roleConfig(t, rolePublic, roles[1].own), slog.New(slog.DiscardHandler))
		for _, path := range []string{"/internal/sweep", "/internal/grants", taskRoute} {
			resp := send(t, operatorRequest(base+path, testOperatorToken, "{}"))
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("POST %s = %d, want 404 on the public role", path, resp.StatusCode)
			}
		}
		if err := stop(); err != nil {
			t.Errorf("run = %v", err)
		}
	})
}

type fakeGoogleTokens struct{}

func (fakeGoogleTokens) Token(context.Context) (string, error) {
	return "MARKER-google-access-token", nil
}

// cloudTasksAPI answers tasks.create for the Cloud Tasks host and 404 for every other host.
type cloudTasksAPI struct {
	mu      sync.Mutex
	created []string // request paths and task names
}

func (a *cloudTasksAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "cloudtasks.googleapis.com" {
		return (&noNetwork{}).RoundTrip(r)
	}
	var req struct {
		Task struct {
			Name        string `json:"name"`
			HTTPRequest struct {
				URL string `json:"url"`
			} `json:"httpRequest"`
		} `json:"task"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	a.mu.Lock()
	a.created = append(a.created, r.URL.Path+" "+req.Task.Name+" "+req.Task.HTTPRequest.URL)
	a.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
}

func TestCloudTasksQueueWiring(t *testing.T) {
	const queueName = "projects/coupling-test/locations/europe-west1/queues/webhooks"
	cloud := func(t *testing.T) Config {
		c := validConfig(t)
		c.Role, c.OperatorTokenFile = rolePublic, ""
		c.QueueDriver, c.TasksQueue, c.WorkerURL, c.Invoker = queueCloudTasks, queueName, testWorkerURL, testInvoker
		return c
	}

	t.Run("intake creates one task per half", func(t *testing.T) {
		api := &cloudTasksAPI{}
		base, stop := background(t, func(ctx context.Context, ready func(net.Addr)) error {
			return run(ctx, cloud(t), slog.New(slog.DiscardHandler), &http.Client{Transport: api}, fakeGoogleTokens{}, ready)
		})
		event := []byte(pullOpenedBody)
		if resp := send(t, webhookRequest(base+"/webhooks/github", "pull_request", "d-1", event, sign(testWebhookSecret, event))); resp.StatusCode != http.StatusAccepted {
			t.Errorf("intake = %d, want 202", resp.StatusCode)
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		want := []string{
			"/v2/" + queueName + "/tasks " + queueName + "/tasks/d-1_gate " + testWorkerURL + taskRoute,
			"/v2/" + queueName + "/tasks " + queueName + "/tasks/d-1_control " + testWorkerURL + taskRoute,
		}
		if !slices.Equal(api.created, want) {
			t.Errorf("created %v, want %v", api.created, want)
		}
	})

	t.Run("no Google token source is a startup error", func(t *testing.T) {
		err := run(t.Context(), cloud(t), slog.New(slog.DiscardHandler), &http.Client{Transport: &noNetwork{}}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), envQueue) {
			t.Errorf("run = %v, want an error naming %s", err, envQueue)
		}
	})

	t.Run("an invalid queue name is a startup error", func(t *testing.T) {
		c := cloud(t)
		c.TasksQueue = "webhooks"
		err := run(t.Context(), c, slog.New(slog.DiscardHandler), &http.Client{Transport: &noNetwork{}}, fakeGoogleTokens{}, nil)
		if err == nil || !strings.Contains(err.Error(), envTasksQueue) {
			t.Errorf("run = %v, want an error naming %s", err, envTasksQueue)
		}
	})
}
