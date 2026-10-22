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
	// envWebhookPreviousFile names the secret that a rotation replaces. Both are accepted while it is set.
	envWebhookPreviousFile = "ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE"
	envOAuthClientID       = "ARCHFIT_GITHUB_OAUTH_CLIENT_ID"
	envOAuthSecretFile     = "ARCHFIT_GITHUB_OAUTH_SECRET_FILE"
	envCookieKeyFile       = "ARCHFIT_COOKIE_KEY_FILE"
	envOperatorTokenFile   = "ARCHFIT_OPERATOR_TOKEN_FILE"
	envAnthropicKeyFile    = "ARCHFIT_ANTHROPIC_KEY_FILE"
	envStore               = "ARCHFIT_STORE"
	envFirestoreProject    = "ARCHFIT_FIRESTORE_PROJECT"
	envFirestoreDatabase   = "ARCHFIT_FIRESTORE_DATABASE"
	// envFirestoreEmulator is the name every Firestore client reads; the emulator prints it on start.
	envFirestoreEmulator = "FIRESTORE_EMULATOR_HOST"
	envSandbox           = "ARCHFIT_SANDBOX"
	envRole              = "ARCHFIT_ROLE"
	envQueue             = "ARCHFIT_QUEUE"
	envTasksQueue        = "ARCHFIT_TASKS_QUEUE"
	envWorkerURL         = "ARCHFIT_WORKER_URL"
	envInvoker           = "ARCHFIT_INVOKER"
	// envApprovedAccounts and envOperators override the built-in lists below. Both are comma-separated.
	envApprovedAccounts = "ARCHFIT_APPROVED_ACCOUNTS"
	envOperators        = "ARCHFIT_OPERATORS"
	// envTraceProject is the name Google client libraries read. It completes the trace field of a log line.
	envTraceProject = "GOOGLE_CLOUD_PROJECT"
	// envRevision is set by Cloud Run on every revision.
	envRevision = "K_REVISION"

	defaultAddr = ":8080"

	storeMemory    = "memory"
	storeFirestore = "firestore"
	sandboxNone    = "none"

	queueInline     = "inline"
	queueCloudTasks = "cloudtasks"
)

// Roles. A role selects the mounted routes and the secrets the process reads.
const (
	roleAll    = "all"    // one process with every route except the worker task route; the local default
	rolePublic = "public" // intake, uploads and the UI; no /internal/* route and no operator token
	// roleWorker serves queued webhooks, the sweep and grants, each behind the invoker identity. It builds no UI,
	// so it reads no OAuth secret, cookie key or Anthropic key.
	roleWorker = "worker"
)

var roles = map[string]bool{roleAll: true, rolePublic: true, roleWorker: true}

// The App is public on GitHub, so anyone can install it; it serves only approved accounts. These are approved by
// configuration: a change is a pull request and a release. Every other account waits for an operator.
var defaultApprovedAccounts = []string{"alexei-led", "vladikk", "coupling-dev"}

// defaultOperators approve installations on /admin: GitHub user IDs, not logins, because a renamed login can be
// registered by someone else. 1898375 is alexei-led, 1788671 is vladikk.
var defaultOperators = []int64{1898375, 1788671}

type Config struct {
	Addr                string   // ARCHFIT_ADDR, default ":8080"
	BaseURL             string   // ARCHFIT_BASE_URL; also the OIDC audience
	AppID               int64    // ARCHFIT_GITHUB_APP_ID
	PrivateKeyFile      string   // ARCHFIT_GITHUB_PRIVATE_KEY_FILE
	WebhookSecretFile   string   // ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE
	WebhookPreviousFile string   // ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE; optional, set only during a rotation
	OAuthClientID       string   // ARCHFIT_GITHUB_OAUTH_CLIENT_ID; not used by the worker role
	OAuthSecretFile     string   // ARCHFIT_GITHUB_OAUTH_SECRET_FILE; never read by the worker role
	CookieKeyFile       string   // ARCHFIT_COOKIE_KEY_FILE; never read by the worker role
	OperatorTokenFile   string   // ARCHFIT_OPERATOR_TOKEN_FILE; authenticates the operator routes; never read by the public role
	AnthropicKeyFile    string   // ARCHFIT_ANTHROPIC_KEY_FILE; empty disables managed LLM; never read by the worker role
	StoreDriver         string   // ARCHFIT_STORE, "memory" (default) or "firestore"
	FirestoreProject    string   // ARCHFIT_FIRESTORE_PROJECT; required by "firestore"
	FirestoreDatabase   string   // ARCHFIT_FIRESTORE_DATABASE; empty selects "(default)"
	FirestoreEmulator   string   // FIRESTORE_EMULATOR_HOST; host:port, plain HTTP and no credentials
	SandboxDriver       string   // ARCHFIT_SANDBOX, "none" until docs/tech-stack.md picks one
	Role                string   // ARCHFIT_ROLE: "all" (default), "public" or "worker"
	QueueDriver         string   // ARCHFIT_QUEUE: "inline" (local all-role default); public requires "cloudtasks"
	TasksQueue          string   // ARCHFIT_TASKS_QUEUE: "projects/<p>/locations/<l>/queues/<q>"; cloudtasks only
	WorkerURL           string   // ARCHFIT_WORKER_URL: the worker service URL; the dispatch OIDC audience
	Invoker             string   // ARCHFIT_INVOKER: the service account e-mail of the queue and Scheduler dispatches
	TraceProject        string   // GOOGLE_CLOUD_PROJECT; optional; names the trace of a log line; empty logs no trace
	Revision            string   // K_REVISION; set by Cloud Run; logged on every line
	ApprovedAccounts    []string // ARCHFIT_APPROVED_ACCOUNTS: logins served without an operator's approval
	Operators           []int64  // ARCHFIT_OPERATORS: GitHub user IDs that may approve installations
}

// Load reads Config from the environment. Secrets are file references only.
func Load(getenv func(string) string) (Config, error) {
	// A non-numeric app ID stays 0 and is reported by validate.
	appID, _ := strconv.ParseInt(getenv(envAppID), 10, 64)
	c := Config{
		Addr:                cmp.Or(getenv(envAddr), defaultAddr),
		BaseURL:             getenv(envBaseURL),
		AppID:               appID,
		PrivateKeyFile:      getenv(envPrivateKeyFile),
		WebhookSecretFile:   getenv(envWebhookSecretFile),
		WebhookPreviousFile: getenv(envWebhookPreviousFile),
		OAuthClientID:       getenv(envOAuthClientID),
		OAuthSecretFile:     getenv(envOAuthSecretFile),
		CookieKeyFile:       getenv(envCookieKeyFile),
		OperatorTokenFile:   getenv(envOperatorTokenFile),
		AnthropicKeyFile:    getenv(envAnthropicKeyFile),
		StoreDriver:         cmp.Or(getenv(envStore), storeMemory),
		FirestoreProject:    getenv(envFirestoreProject),
		FirestoreDatabase:   getenv(envFirestoreDatabase),
		FirestoreEmulator:   getenv(envFirestoreEmulator),
		SandboxDriver:       cmp.Or(getenv(envSandbox), sandboxNone),
		Role:                cmp.Or(getenv(envRole), roleAll),
		QueueDriver:         cmp.Or(getenv(envQueue), queueInline),
		TasksQueue:          getenv(envTasksQueue),
		WorkerURL:           getenv(envWorkerURL),
		Invoker:             getenv(envInvoker),
		TraceProject:        getenv(envTraceProject),
		Revision:            getenv(envRevision),
		ApprovedAccounts:    defaultApprovedAccounts,
		Operators:           defaultOperators,
	}
	if v := getenv(envApprovedAccounts); v != "" {
		c.ApprovedAccounts = strings.Split(v, ",")
	}
	if v := getenv(envOperators); v != "" {
		c.Operators = parseIDs(v) // an invalid list is nil, which validate reports
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
	servesUI := c.Role != roleWorker
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
		// The UI secrets: the worker serves no UI and never reads them.
		{envOAuthClientID, !servesUI || c.OAuthClientID != ""},
		{envOAuthSecretFile, !servesUI || c.OAuthSecretFile != ""},
		{envCookieKeyFile, !servesUI || c.CookieKeyFile != ""},
		// The public role never reads the operator token: it serves no route that checks it.
		{envOperatorTokenFile, c.Role == rolePublic || c.OperatorTokenFile != ""},
		{envStore, storeDrivers[c.StoreDriver] != nil},
		{envFirestoreProject, c.StoreDriver != storeFirestore || c.FirestoreProject != ""},
		{envSandbox, sandboxDrivers[c.SandboxDriver] != nil},
		// Local all-role work is inline; public intake must use the durable queue.
		{envQueue, c.QueueDriver == queueInline && c.Role != rolePublic || c.QueueDriver == queueCloudTasks && c.Role != roleAll},
		{envTasksQueue, !enqueuesToCloudTasks || c.TasksQueue != ""},
		// The worker URL is an OIDC audience, compared byte for byte like the base URL.
		{envWorkerURL, !needsWorker || strings.HasPrefix(c.WorkerURL, "https://") && !strings.HasSuffix(c.WorkerURL, "/")},
		{envInvoker, !needsWorker || strings.Contains(c.Invoker, "@")},
		{envOperators, len(c.Operators) > 0},
	}
	for _, ch := range checks {
		if !ch.ok {
			return fmt.Errorf("app: %s is missing or invalid", ch.env)
		}
	}
	return nil
}

// parseIDs reads comma-separated positive integers; any other element makes the whole list nil.
func parseIDs(v string) []int64 {
	var ids []int64
	for f := range strings.SplitSeq(v, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
		if err != nil || id <= 0 {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}

// secrets are the contents of the secret files. They never enter Config, a log line or an error.
type secrets struct {
	privateKeyPEM   []byte
	webhookSecret   []byte
	webhookPrevious []byte // nil outside a rotation
	oauthSecret     []byte // nil in the worker role
	cookieKey       []byte // nil in the worker role
	operatorToken   []byte // nil in the public role
	anthropicKey    []byte // nil when no key file is configured, and in the worker role
}

func loadSecrets(c Config) (secrets, error) {
	var s secrets
	// A role never reads a secret it does not use, even when it is configured.
	oauthSecretFile, cookieKeyFile, anthropicKeyFile, operatorTokenFile :=
		c.OAuthSecretFile, c.CookieKeyFile, c.AnthropicKeyFile, c.OperatorTokenFile
	switch c.Role {
	case rolePublic:
		operatorTokenFile = ""
	case roleWorker:
		oauthSecretFile, cookieKeyFile, anthropicKeyFile = "", "", ""
	}
	files := []struct {
		env, path string
		dst       *[]byte
	}{
		{envPrivateKeyFile, c.PrivateKeyFile, &s.privateKeyPEM},
		{envWebhookSecretFile, c.WebhookSecretFile, &s.webhookSecret},
		{envWebhookPreviousFile, c.WebhookPreviousFile, &s.webhookPrevious},
		{envOAuthSecretFile, oauthSecretFile, &s.oauthSecret},
		{envCookieKeyFile, cookieKeyFile, &s.cookieKey},
		{envOperatorTokenFile, operatorTokenFile, &s.operatorToken},
		{envAnthropicKeyFile, anthropicKeyFile, &s.anthropicKey},
	}
	for _, f := range files {
		if f.path == "" { // an optional secret, or one that the role never reads
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

// webhookSecrets are the secrets a delivery signature may use: the current one and, during a rotation, the previous.
func (s secrets) webhookSecrets() [][]byte {
	if s.webhookPrevious == nil {
		return [][]byte{s.webhookSecret}
	}
	return [][]byte{s.webhookSecret, s.webhookPrevious}
}
