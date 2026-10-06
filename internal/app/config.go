// Package app is the composition root: it loads Config, wires every module and serves HTTP.
package app

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	envAddr              = "ARCHFIT_ADDR"
	envBaseURL           = "ARCHFIT_BASE_URL"
	envAppID             = "ARCHFIT_GITHUB_APP_ID"
	envPrivateKeyFile    = "ARCHFIT_GITHUB_PRIVATE_KEY_FILE"
	envWebhookSecretFile = "ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE"
	envOAuthClientID     = "ARCHFIT_GITHUB_OAUTH_CLIENT_ID"
	envOAuthSecretFile   = "ARCHFIT_GITHUB_OAUTH_SECRET_FILE"
	envCookieKeyFile     = "ARCHFIT_COOKIE_KEY_FILE"
	envOperatorTokenFile = "ARCHFIT_OPERATOR_TOKEN_FILE"
	envAnthropicKeyFile  = "ARCHFIT_ANTHROPIC_KEY_FILE"
	envStore             = "ARCHFIT_STORE"
	envSandbox           = "ARCHFIT_SANDBOX"
	envRole              = "ARCHFIT_ROLE"
	envQueue             = "ARCHFIT_QUEUE"
	envTasksQueue        = "ARCHFIT_TASKS_QUEUE"
	envWorkerURL         = "ARCHFIT_WORKER_URL"
	envInvoker           = "ARCHFIT_INVOKER"

	defaultAddr = ":8080"

	storeMemory = "memory"
	sandboxNone = "none"

	queueInline     = "inline"
	queueCloudTasks = "cloudtasks"
)

// Roles. A role selects the mounted routes and the secrets the process reads.
const (
	roleAll    = "all"    // one process with every route except the worker task route; the local default
	rolePublic = "public" // intake, uploads and the UI; no /internal/* route and no operator token
	roleWorker = "worker" // queued webhooks, the sweep and grants, each behind the invoker identity
)

var roles = map[string]bool{roleAll: true, rolePublic: true, roleWorker: true}

type Config struct {
	Addr              string // ARCHFIT_ADDR, default ":8080"
	BaseURL           string // ARCHFIT_BASE_URL; also the OIDC audience
	AppID             int64  // ARCHFIT_GITHUB_APP_ID
	PrivateKeyFile    string // ARCHFIT_GITHUB_PRIVATE_KEY_FILE
	WebhookSecretFile string // ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE
	OAuthClientID     string // ARCHFIT_GITHUB_OAUTH_CLIENT_ID
	OAuthSecretFile   string // ARCHFIT_GITHUB_OAUTH_SECRET_FILE
	CookieKeyFile     string // ARCHFIT_COOKIE_KEY_FILE
	OperatorTokenFile string // ARCHFIT_OPERATOR_TOKEN_FILE; authenticates /internal/*
	AnthropicKeyFile  string // ARCHFIT_ANTHROPIC_KEY_FILE; empty disables managed LLM
	StoreDriver       string // ARCHFIT_STORE, "memory" until docs/tech-stack.md picks a KV
	SandboxDriver     string // ARCHFIT_SANDBOX, "none" until docs/tech-stack.md picks one
	Role              string // ARCHFIT_ROLE: "all" (default), "public" or "worker"
	QueueDriver       string // ARCHFIT_QUEUE: "inline" (default) or "cloudtasks"
	TasksQueue        string // ARCHFIT_TASKS_QUEUE: "projects/<p>/locations/<l>/queues/<q>"; cloudtasks only
	WorkerURL         string // ARCHFIT_WORKER_URL: the worker service URL; the dispatch OIDC audience
	Invoker           string // ARCHFIT_INVOKER: the service account e-mail of the queue and Scheduler dispatches
}

// Load reads Config from the environment. Secrets are file references only.
func Load(getenv func(string) string) (Config, error) {
	// A non-numeric app ID stays 0 and is reported by validate.
	appID, _ := strconv.ParseInt(getenv(envAppID), 10, 64)
	c := Config{
		Addr:              cmp.Or(getenv(envAddr), defaultAddr),
		BaseURL:           getenv(envBaseURL),
		AppID:             appID,
		PrivateKeyFile:    getenv(envPrivateKeyFile),
		WebhookSecretFile: getenv(envWebhookSecretFile),
		OAuthClientID:     getenv(envOAuthClientID),
		OAuthSecretFile:   getenv(envOAuthSecretFile),
		CookieKeyFile:     getenv(envCookieKeyFile),
		OperatorTokenFile: getenv(envOperatorTokenFile),
		AnthropicKeyFile:  getenv(envAnthropicKeyFile),
		StoreDriver:       cmp.Or(getenv(envStore), storeMemory),
		SandboxDriver:     cmp.Or(getenv(envSandbox), sandboxNone),
		Role:              cmp.Or(getenv(envRole), roleAll),
		QueueDriver:       cmp.Or(getenv(envQueue), queueInline),
		TasksQueue:        getenv(envTasksQueue),
		WorkerURL:         getenv(envWorkerURL),
		Invoker:           getenv(envInvoker),
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// validate names the first missing or invalid variable and never echoes a value.
// Run repeats it because a Config can be built by hand.
func (c Config) validate() error {
	// The worker never enqueues; the public role and "all" enqueue through the configured driver.
	enqueuesToCloudTasks := c.Role != roleWorker && c.QueueDriver == queueCloudTasks
	needsWorker := enqueuesToCloudTasks || c.Role == roleWorker
	checks := []struct {
		env string
		ok  bool
	}{
		{envRole, roles[c.Role]},
		{envAddr, c.Addr != ""},
		// The base URL is the OIDC audience, compared byte for byte; the Action refuses an endpoint ending in '/'.
		{envBaseURL, c.BaseURL != "" && !strings.HasSuffix(c.BaseURL, "/")},
		{envAppID, c.AppID > 0},
		{envPrivateKeyFile, c.PrivateKeyFile != ""},
		{envWebhookSecretFile, c.WebhookSecretFile != ""},
		{envOAuthClientID, c.OAuthClientID != ""},
		{envOAuthSecretFile, c.OAuthSecretFile != ""},
		{envCookieKeyFile, c.CookieKeyFile != ""},
		// The public role never reads the operator token: it serves no route that checks it.
		{envOperatorTokenFile, c.Role == rolePublic || c.OperatorTokenFile != ""},
		{envStore, storeDrivers[c.StoreDriver] != nil},
		{envSandbox, sandboxDrivers[c.SandboxDriver] != nil},
		{envQueue, c.QueueDriver == queueInline || c.QueueDriver == queueCloudTasks},
		{envTasksQueue, !enqueuesToCloudTasks || c.TasksQueue != ""},
		// The worker URL is an OIDC audience, compared byte for byte like the base URL.
		{envWorkerURL, !needsWorker || strings.HasPrefix(c.WorkerURL, "https://") && !strings.HasSuffix(c.WorkerURL, "/")},
		{envInvoker, !needsWorker || strings.Contains(c.Invoker, "@")},
	}
	for _, ch := range checks {
		if !ch.ok {
			return fmt.Errorf("app: %s is missing or invalid", ch.env)
		}
	}
	return nil
}

// secrets are the contents of the secret files. They never enter Config, a log line or an error.
type secrets struct {
	privateKeyPEM []byte
	webhookSecret []byte
	oauthSecret   []byte
	cookieKey     []byte
	operatorToken []byte // nil in the public role
	anthropicKey  []byte // nil when no key file is configured
}

func loadSecrets(c Config) (secrets, error) {
	var s secrets
	operatorTokenFile := c.OperatorTokenFile
	if c.Role == rolePublic {
		operatorTokenFile = "" // never read, even when configured
	}
	files := []struct {
		env, path string
		dst       *[]byte
	}{
		{envPrivateKeyFile, c.PrivateKeyFile, &s.privateKeyPEM},
		{envWebhookSecretFile, c.WebhookSecretFile, &s.webhookSecret},
		{envOAuthSecretFile, c.OAuthSecretFile, &s.oauthSecret},
		{envCookieKeyFile, c.CookieKeyFile, &s.cookieKey},
		{envOperatorTokenFile, operatorTokenFile, &s.operatorToken},
		{envAnthropicKeyFile, c.AnthropicKeyFile, &s.anthropicKey},
	}
	for _, f := range files {
		if f.path == "" { // the optional Anthropic key, and the operator token of the public role
			continue
		}
		b, err := os.ReadFile(f.path)
		if err != nil {
			return secrets{}, fmt.Errorf("app: read the file named by %s: %w", f.env, err)
		}
		if b = bytes.TrimSpace(b); len(b) == 0 {
			return secrets{}, fmt.Errorf("app: the file named by %s is empty", f.env)
		}
		*f.dst = b
	}
	return s, nil
}
