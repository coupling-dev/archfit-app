package github_test

import (
	"context"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

const audience = "https://archfit.example"

// countingSource is the real TokenSource with a count of the invalidations api asks for.
type countingSource struct {
	auth.TokenSource
	invalidations atomic.Int32
}

func (c *countingSource) Invalidate(installationID int64) {
	c.invalidations.Add(1)
	c.TokenSource.Invalidate(installationID)
}

// world is the branch wired the way app wires it, against one fake GitHub.
type world struct {
	fake   *fakeGitHub
	hc     *http.Client
	tokens *countingSource
	oauth  auth.OAuth
	api    *api.Client
	oidc   oidc.Verifier
}

func newWorld(t *testing.T) *world {
	t.Helper()
	appKey, _ := keys(t)
	return newWorldWithKey(t, appKey)
}

func newWorldWithKey(t *testing.T, appKey *rsa.PrivateKey) *world {
	t.Helper()
	fake := newFakeGitHub(t)
	hc := fake.client(t)
	ts, oauth, err := auth.New(auth.Credentials{
		AppID:             appID,
		PrivateKeyPEM:     keyPEM(appKey),
		OAuthClientID:     oauthClientID,
		OAuthClientSecret: oauthClientSecret,
	}, hc)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	tokens := &countingSource{TokenSource: ts}
	return &world{fake: fake, hc: hc, tokens: tokens, oauth: oauth, api: api.New(tokens, hc), oidc: oidc.New(hc)}
}

func keyPEM(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func signDelivery(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// receive handles a delivery the way app does: no Event exists before the signature holds.
func receive(signature, eventType, deliveryID string, body []byte) (webhook.Event, error) {
	if err := webhook.Verify([]byte(webhookSecret), signature, body); err != nil {
		return webhook.Event{}, err
	}
	return webhook.Parse(eventType, deliveryID, body)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return b
}

func pullRequestDelivery(t *testing.T, installationID int64, fullName string) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{
		"action":       "opened",
		"number":       pullNumber,
		"installation": map[string]any{"id": installationID},
		"repository":   map[string]any{"full_name": fullName, "private": true, "default_branch": "main"},
		"pull_request": map[string]any{
			"number": pullNumber,
			"title":  "adds a widget",
			"head":   map[string]any{"sha": pullHeadSHA, "ref": "feature", "repo": map[string]string{"full_name": fullName}},
			"base":   map[string]any{"sha": pullBaseSHA, "ref": "main"},
		},
	})
}

// actionsClaims are the claims GitHub Actions signs for run runID of repoWidgets.
func actionsClaims(overrides map[string]any) map[string]any {
	now := time.Now()
	claims := map[string]any{
		"iss": "https://" + hostOIDC, "aud": audience,
		"iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"repository": repoWidgets, "repository_id": "5150", "run_id": "9001", "run_attempt": "1",
		"sha": pullHeadSHA, "ref": "refs/pull/7/merge", "event_name": "pull_request",
		"job_workflow_ref":   repoWidgets + "/" + workflowPath + "@refs/pull/7/merge",
		"runner_environment": "github-hosted",
	}
	maps.Copy(claims, overrides)
	return claims
}

func actionsToken(t *testing.T, key *rsa.PrivateKey, overrides map[string]any) string {
	t.Helper()
	return signRS256(t, key, map[string]any{"alg": "RS256", "typ": "JWT", "kid": jwksKid}, actionsClaims(overrides))
}
