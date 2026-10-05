package app

import (
	"strings"
	"testing"
)

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func requiredEnv() map[string]string {
	return map[string]string{
		envBaseURL:           "https://archfit.example",
		envAppID:             "12345",
		envPrivateKeyFile:    "/run/secrets/key.pem",
		envWebhookSecretFile: "/run/secrets/webhook",
		envOAuthClientID:     "Iv1.client",
		envOAuthSecretFile:   "/run/secrets/oauth",
		envCookieKeyFile:     "/run/secrets/cookie",
		envOperatorTokenFile: "/run/secrets/operator",
	}
}

func with(m map[string]string, k, v string) map[string]string {
	m[k] = v
	return m
}

func without(m map[string]string, k string) map[string]string {
	delete(m, k)
	return m
}

func TestLoadDefaultsAndRequired(t *testing.T) {
	requiredOnly := Config{
		Addr:              ":8080",
		BaseURL:           "https://archfit.example",
		AppID:             12345,
		PrivateKeyFile:    "/run/secrets/key.pem",
		WebhookSecretFile: "/run/secrets/webhook",
		OAuthClientID:     "Iv1.client",
		OAuthSecretFile:   "/run/secrets/oauth",
		CookieKeyFile:     "/run/secrets/cookie",
		OperatorTokenFile: "/run/secrets/operator",
		StoreDriver:       "memory",
		SandboxDriver:     "none",
	}
	everything := requiredOnly
	everything.Addr = "127.0.0.1:9000"
	everything.AnthropicKeyFile = "/run/secrets/anthropic"
	underPath := requiredOnly
	underPath.BaseURL = "https://archfit.example/app"

	all := requiredEnv()
	all[envAddr] = "127.0.0.1:9000"
	all[envAnthropicKeyFile] = "/run/secrets/anthropic"
	all[envStore] = "memory"
	all[envSandbox] = "none"

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string // the variable the error must name
	}{
		{name: "empty", env: map[string]string{}, wantErr: envBaseURL},
		{name: "only required", env: requiredEnv(), want: requiredOnly},
		{name: "every variable", env: all, want: everything},
		{name: "non-numeric app ID", env: with(requiredEnv(), envAppID, "MARKER-not-a-number"), wantErr: envAppID},
		{name: "missing app ID", env: without(requiredEnv(), envAppID), wantErr: envAppID},
		{name: "missing private key file", env: without(requiredEnv(), envPrivateKeyFile), wantErr: envPrivateKeyFile},
		{name: "missing webhook secret file", env: without(requiredEnv(), envWebhookSecretFile), wantErr: envWebhookSecretFile},
		{name: "missing OAuth client ID", env: without(requiredEnv(), envOAuthClientID), wantErr: envOAuthClientID},
		{name: "missing OAuth secret file", env: without(requiredEnv(), envOAuthSecretFile), wantErr: envOAuthSecretFile},
		{name: "missing cookie key file", env: without(requiredEnv(), envCookieKeyFile), wantErr: envCookieKeyFile},
		{name: "missing operator token file", env: without(requiredEnv(), envOperatorTokenFile), wantErr: envOperatorTokenFile},
		{name: "first missing wins", env: without(without(requiredEnv(), envOperatorTokenFile), envAppID), wantErr: envAppID},
		{name: "unknown store", env: with(requiredEnv(), envStore, "MARKER-redis"), wantErr: envStore},
		{name: "unknown sandbox", env: with(requiredEnv(), envSandbox, "MARKER-docker"), wantErr: envSandbox},
		// The base URL is the OIDC audience, compared byte for byte, and the Action refuses an endpoint ending in '/'.
		{name: "base URL with trailing slash", env: with(requiredEnv(), envBaseURL, "https://MARKER.example/"), wantErr: envBaseURL},
		{name: "base URL path with trailing slash", env: with(requiredEnv(), envBaseURL, "https://archfit.example/app/"), wantErr: envBaseURL},
		{name: "base URL slash only", env: with(requiredEnv(), envBaseURL, "/"), wantErr: envBaseURL},
		{name: "base URL with a path", env: with(requiredEnv(), envBaseURL, "https://archfit.example/app"), want: underPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(mapEnv(tt.env))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if got != tt.want {
					t.Errorf("Load = %+v, want %+v", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load = %+v, want an error naming %s", got, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr+" ") {
				t.Errorf("error %q does not name %s", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "MARKER") {
				t.Errorf("error %q echoes a value", err)
			}
		})
	}
}

func TestLoadNeverReadsSecretValuesFromEnv(t *testing.T) {
	const inlinePEM = "-----BEGIN RSA PRIVATE KEY-----\nMARKER\n-----END RSA PRIVATE KEY-----"
	inline := map[string]string{
		"ARCHFIT_GITHUB_PRIVATE_KEY":    inlinePEM,
		"ARCHFIT_GITHUB_WEBHOOK_SECRET": "MARKER-webhook",
		"ARCHFIT_GITHUB_OAUTH_SECRET":   "MARKER-oauth",
		"ARCHFIT_COOKIE_KEY":            "MARKER-cookie",
		"ARCHFIT_OPERATOR_TOKEN":        "MARKER-operator",
		"ARCHFIT_ANTHROPIC_KEY":         "MARKER-anthropic",
	}

	t.Run("inline values do not satisfy the file variables", func(t *testing.T) {
		env := map[string]string{envBaseURL: "https://archfit.example", envAppID: "12345"}
		for k, v := range inline {
			env[k] = v
		}
		_, err := Load(mapEnv(env))
		if err == nil || !strings.Contains(err.Error(), envPrivateKeyFile) {
			t.Fatalf("Load error = %v, want one naming %s", err, envPrivateKeyFile)
		}
	})

	t.Run("inline values are never read", func(t *testing.T) {
		env := requiredEnv()
		for k, v := range inline {
			env[k] = v
		}
		var asked []string
		c, err := Load(func(k string) string {
			asked = append(asked, k)
			return env[k]
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, k := range asked {
			if _, isInline := inline[k]; isInline {
				t.Errorf("Load read %s", k)
			}
		}
		if c.AnthropicKeyFile != "" {
			t.Errorf("AnthropicKeyFile = %q, want empty", c.AnthropicKeyFile)
		}
	})
}
