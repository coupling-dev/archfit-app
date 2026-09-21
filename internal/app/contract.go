// Package app is the composition root: it loads Config, wires every module and serves HTTP.
package app

import (
	"context"
	"errors"
	"log/slog"
)

var errNotImplemented = errors.New("app: not implemented")

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
}

// Load reads Config from the environment. Secrets are file references only.
func Load(getenv func(string) string) (Config, error) {
	return Config{}, errNotImplemented
}

// Run wires every module and serves until ctx is cancelled.
func Run(ctx context.Context, c Config, log *slog.Logger) error {
	return errNotImplemented
}
