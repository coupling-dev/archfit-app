package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestRunFailsWithoutConfiguration(t *testing.T) {
	err := run(context.Background(), func(string) string { return "" }, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("run with an empty environment returned nil; want a configuration error")
	}
}

func TestRunServesUntilCancelled(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	env := map[string]string{
		"ARCHFIT_ADDR":                       "127.0.0.1:0",
		"ARCHFIT_BASE_URL":                   "https://archfit.example",
		"ARCHFIT_GITHUB_APP_ID":              "1",
		"ARCHFIT_GITHUB_PRIVATE_KEY_FILE":    file("key.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		"ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE": file("webhook", []byte("webhook-secret")),
		"ARCHFIT_GITHUB_OAUTH_CLIENT_ID":     "Iv1.client",
		"ARCHFIT_GITHUB_OAUTH_SECRET_FILE":   file("oauth", []byte("oauth-secret")),
		"ARCHFIT_COOKIE_KEY_FILE":            file("cookie", []byte("0123456789abcdef0123456789abcdef")),
		"ARCHFIT_OPERATOR_TOKEN_FILE":        file("operator", []byte("operator-token")),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context is the SIGTERM path: start, then shut down cleanly
	if err := run(ctx, func(k string) string { return env[k] }, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("run with a valid environment and a cancelled context: %v", err)
	}
}
