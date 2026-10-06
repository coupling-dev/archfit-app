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
	envFirestoreProject  = "ARCHFIT_FIRESTORE_PROJECT"
	envFirestoreDatabase = "ARCHFIT_FIRESTORE_DATABASE"
	// envFirestoreEmulator is the name every Firestore client reads; the emulator prints it on start.
	envFirestoreEmulator = "FIRESTORE_EMULATOR_HOST"
	envSandbox           = "ARCHFIT_SANDBOX"

	defaultAddr = ":8080"

	storeMemory    = "memory"
	storeFirestore = "firestore"
	sandboxNone    = "none"
)

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
	StoreDriver       string // ARCHFIT_STORE, "memory" (default) or "firestore"
	FirestoreProject  string // ARCHFIT_FIRESTORE_PROJECT; required by "firestore"
	FirestoreDatabase string // ARCHFIT_FIRESTORE_DATABASE; empty selects "(default)"
	FirestoreEmulator string // FIRESTORE_EMULATOR_HOST; host:port, plain HTTP and no credentials
	SandboxDriver     string // ARCHFIT_SANDBOX, "none" until docs/tech-stack.md picks one
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
		FirestoreProject:  getenv(envFirestoreProject),
		FirestoreDatabase: getenv(envFirestoreDatabase),
		FirestoreEmulator: getenv(envFirestoreEmulator),
		SandboxDriver:     cmp.Or(getenv(envSandbox), sandboxNone),
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// validate names the first missing or invalid variable and never echoes a value.
// Run repeats it because a Config can be built by hand.
func (c Config) validate() error {
	checks := []struct {
		env string
		ok  bool
	}{
		{envAddr, c.Addr != ""},
		// The base URL is the OIDC audience, compared byte for byte; the Action refuses an endpoint ending in '/'.
		{envBaseURL, c.BaseURL != "" && !strings.HasSuffix(c.BaseURL, "/")},
		{envAppID, c.AppID > 0},
		{envPrivateKeyFile, c.PrivateKeyFile != ""},
		{envWebhookSecretFile, c.WebhookSecretFile != ""},
		{envOAuthClientID, c.OAuthClientID != ""},
		{envOAuthSecretFile, c.OAuthSecretFile != ""},
		{envCookieKeyFile, c.CookieKeyFile != ""},
		{envOperatorTokenFile, c.OperatorTokenFile != ""},
		{envStore, storeDrivers[c.StoreDriver] != nil},
		{envFirestoreProject, c.StoreDriver != storeFirestore || c.FirestoreProject != ""},
		{envSandbox, sandboxDrivers[c.SandboxDriver] != nil},
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
	operatorToken []byte
	anthropicKey  []byte // nil when no key file is configured
}

func loadSecrets(c Config) (secrets, error) {
	var s secrets
	files := []struct {
		env, path string
		dst       *[]byte
	}{
		{envPrivateKeyFile, c.PrivateKeyFile, &s.privateKeyPEM},
		{envWebhookSecretFile, c.WebhookSecretFile, &s.webhookSecret},
		{envOAuthSecretFile, c.OAuthSecretFile, &s.oauthSecret},
		{envCookieKeyFile, c.CookieKeyFile, &s.cookieKey},
		{envOperatorTokenFile, c.OperatorTokenFile, &s.operatorToken},
		{envAnthropicKeyFile, c.AnthropicKeyFile, &s.anthropicKey},
	}
	for _, f := range files {
		if f.path == "" { // only the optional Anthropic key passes validate with an empty path
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
