package github_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

func TestTrustRootsAreSeparate(t *testing.T) {
	appKey, jwksKey := keys(t)

	t.Run("webhook signed with the OAuth secret", func(t *testing.T) {
		body := pullRequestDelivery(t, instA, repoWidgets)
		ev, err := receive(signDelivery(oauthClientSecret, body), "pull_request", "delivery-1", body)
		if err == nil {
			t.Fatalf("delivery accepted: %+v", ev)
		}
	})

	t.Run("OIDC token signed with the App key", func(t *testing.T) {
		w := newWorld(t)
		claims, err := w.oidc.Verify(testContext(t), actionsToken(t, appKey, nil), audience)
		if err == nil {
			t.Fatalf("token accepted: %+v", claims)
		}
		if errors.Is(err, oidc.ErrUnavailable) {
			t.Errorf("error = %v, want a refusal, not an outage", err)
		}
	})

	t.Run("App JWT presented as an OIDC token", func(t *testing.T) {
		w := newWorld(t)
		jwt, err := w.tokens.AppJWT()
		if err != nil {
			t.Fatalf("AppJWT: %v", err)
		}
		if claims, err := w.oidc.Verify(testContext(t), jwt, audience); err == nil {
			t.Fatalf("App JWT accepted: %+v", claims)
		}
	})

	t.Run("App JWT signed with the JWKS key", func(t *testing.T) {
		w := newWorldWithKey(t, jwksKey)
		ctx := testContext(t)
		if repo, err := w.api.ResolveRepo(ctx, repoWidgets); err == nil {
			t.Fatalf("ResolveRepo accepted: %+v", repo)
		}
		if token, err := w.tokens.InstallationToken(ctx, instA); err == nil {
			t.Fatalf("installation token issued: %q", token)
		}
		for _, r := range w.fake.since(0) {
			if r.cred != credBadJWT || r.status != http.StatusUnauthorized {
				t.Errorf("%s %s: credential %s answered %d, want a refused JWT", r.method, r.path, r.cred, r.status)
			}
		}
		if got := w.fake.mintCount(instA); got != 0 {
			t.Errorf("token mints = %d, want 0", got)
		}
	})
}

func TestForgedAndCrossTenantIdentity(t *testing.T) {
	t.Run("forged delivery names installation B", func(t *testing.T) {
		body := pullRequestDelivery(t, instB, repoSecret)
		for name, signature := range map[string]string{
			"wrong secret":   signDelivery("not-the-secret", body),
			"other body":     signDelivery(webhookSecret, pullRequestDelivery(t, instA, repoWidgets)),
			"no signature":   "",
			"wrong encoding": "sha1=" + strings.Repeat("0", 40),
		} {
			ev, err := receive(signature, "pull_request", "delivery-1", body)
			if err == nil || ev.InstallationID != 0 || ev.Repo != "" {
				t.Errorf("%s: event %+v, error %v; want no event", name, ev, err)
			}
		}
	})

	t.Run("installation A paired with B's repository", func(t *testing.T) {
		w := newWorld(t)
		ctx := testContext(t)
		body := pullRequestDelivery(t, instA, repoWidgets)
		ev, err := receive(signDelivery(webhookSecret, body), "pull_request", "delivery-2", body)
		if err != nil {
			t.Fatalf("valid delivery of A: %v", err)
		}

		forged := api.RepoRef{InstallationID: ev.InstallationID, FullName: repoSecret}
		if pull, err := w.api.Pull(ctx, forged, ev.PullRequest); !errors.Is(err, api.ErrNotFound) {
			t.Errorf("Pull = %+v, %v; want ErrNotFound", pull, err)
		}
		if content, _, err := w.api.File(ctx, forged, "main", filePath); !errors.Is(err, api.ErrNotFound) || content != nil {
			t.Errorf("File = %q, %v; want ErrNotFound", content, err)
		}
		if got := w.fake.mintCount(instB); got != 0 {
			t.Errorf("token mints for B = %d, want 0", got)
		}
		for _, r := range w.fake.since(0) {
			if r.cred == credInst(instB) {
				t.Errorf("%s %s used B's token", r.method, r.path)
			}
		}
	})
}

func TestNoSecretInAnyError(t *testing.T) {
	appKey, jwksKey := keys(t)
	ref := api.RepoRef{InstallationID: instA, FullName: repoWidgets}
	const leak = `{"message":"` + marker + `-payload-text"}`
	apiFails := func(prefix string, status int, body string, headers ...string) func(http.ResponseWriter, *http.Request) bool {
		return answer(hostAPI, prefix, status, body, headers...)
	}

	cases := []struct {
		name     string
		override func(http.ResponseWriter, *http.Request) bool
		run      func(t *testing.T, w *world) error
	}{
		{"auth: malformed key", nil, func(*testing.T, *world) error {
			_, _, err := auth.New(auth.Credentials{
				AppID: appID, PrivateKeyPEM: []byte("-----BEGIN RSA PRIVATE KEY-----\n" + marker + "\n-----END RSA PRIVATE KEY-----\n"),
				OAuthClientID: oauthClientID, OAuthClientSecret: oauthClientSecret,
			}, nil)
			return err
		}},
		{"auth: refused code", nil, func(t *testing.T, w *world) error {
			_, err := w.oauth.Exchange(testContext(t), marker+"-wrong-code")
			return err
		}},
		{"auth: exchange fails", answer(hostWeb, "/login/", http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, err := w.oauth.Exchange(testContext(t), oauthCode)
			return err
		}},
		{"auth: user lookup fails", apiFails("/user", http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, err := w.oauth.Exchange(testContext(t), oauthCode)
			return err
		}},
		{"auth: token mint fails", apiFails("/app/installations/", http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, err := w.tokens.InstallationToken(testContext(t), instA)
			return err
		}},
		{"auth: malformed mint answer", apiFails("/app/installations/", http.StatusCreated, `{"token":"ghs_`+marker+`",`), func(t *testing.T, w *world) error {
			_, err := w.tokens.InstallationToken(testContext(t), instA)
			return err
		}},
		{"auth: unknown installation", nil, func(t *testing.T, w *world) error {
			_, err := w.tokens.InstallationToken(testContext(t), 999)
			return err
		}},
		{"auth: repository token not narrowed", nil, func(t *testing.T, w *world) error {
			_, err := w.tokens.RepoReadToken(testContext(t), instA, repoWidgets)
			return err
		}},
		{"api: server error", apiFails(repoPrefix, http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, _, err := w.api.File(testContext(t), ref, "main", marker+"-dir/"+filePath)
			return err
		}},
		{"api: malformed answer", apiFails(repoPrefix, http.StatusOK, `{"type":"`+marker), func(t *testing.T, w *world) error {
			_, _, err := w.api.File(testContext(t), ref, "main", filePath)
			return err
		}},
		{"api: not found", nil, func(t *testing.T, w *world) error {
			_, _, err := w.api.File(testContext(t), ref, "main", marker+"-dir/missing.yaml")
			return err
		}},
		{"api: every token refused", rejectEveryToken, func(t *testing.T, w *world) error {
			_, err := w.api.OpenPulls(testContext(t), ref)
			return err
		}},
		{"api: token mint fails", apiFails("/app/installations/", http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, err := w.api.OpenPulls(testContext(t), ref)
			return err
		}},
		{"api: rate limited", rateLimitAll(), func(t *testing.T, w *world) error {
			_, err := w.api.ResolveRepo(testContext(t), repoWidgets)
			return err
		}},
		{"api: user token refused", apiFails(repoPrefix, http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, err := w.api.Permission(testContext(t), userAccessToken, repoWidgets)
			return err
		}},
		{"api: check run refused", apiFails(repoPrefix, http.StatusUnprocessableEntity, leak), func(t *testing.T, w *world) error {
			_, err := w.api.CreateCheckRun(testContext(t), ref, api.CheckRun{
				Name: "archfit", HeadSHA: pullHeadSHA, Status: "in_progress", Title: "gate", Summary: marker + "-summary",
			})
			return err
		}},
		{"webhook: wrong secret", nil, func(t *testing.T, _ *world) error {
			body := pullRequestDelivery(t, instA, repoWidgets)
			return webhook.VerifyAny([][]byte{[]byte(webhookSecret)}, signDelivery(oauthClientSecret, body), body)
		}},
		{"webhook: malformed signature", nil, func(t *testing.T, _ *world) error {
			return webhook.VerifyAny([][]byte{[]byte(webhookSecret)}, "sha256="+marker, []byte(leak))
		}},
		{"webhook: malformed body", nil, func(*testing.T, *world) error {
			_, err := webhook.Parse("pull_request", "delivery-1", []byte(`{"action":"`+marker))
			return err
		}},
		{"webhook: no installation", nil, func(t *testing.T, _ *world) error {
			_, err := webhook.Parse("pull_request", "delivery-1", pullRequestDelivery(t, 0, marker+"/"+marker))
			return err
		}},
		{"webhook: bad head SHA", nil, func(t *testing.T, _ *world) error {
			body := mustJSON(t, map[string]any{
				"action": "opened", "installation": map[string]any{"id": instA},
				"repository":   map[string]any{"full_name": repoWidgets},
				"pull_request": map[string]any{"number": pullNumber, "head": map[string]any{"sha": marker + "-not-a-sha"}},
			})
			_, err := webhook.Parse("pull_request", "delivery-1", body)
			return err
		}},
		{"webhook: no delivery ID", nil, func(t *testing.T, _ *world) error {
			_, err := webhook.Parse("pull_request", "", pullRequestDelivery(t, instA, marker+"/"+marker))
			return err
		}},
		{"oidc: malformed token", nil, func(t *testing.T, w *world) error {
			_, err := w.oidc.Verify(testContext(t), marker+"."+marker+"."+marker, audience)
			return err
		}},
		{"oidc: wrong signing key", nil, func(t *testing.T, w *world) error {
			_, err := w.oidc.Verify(testContext(t), actionsToken(t, appKey, map[string]any{"ref": marker}), audience)
			return err
		}},
		{"oidc: wrong audience", nil, func(t *testing.T, w *world) error {
			_, err := w.oidc.Verify(testContext(t), actionsToken(t, jwksKey, map[string]any{"aud": marker + "-audience"}), audience)
			return err
		}},
		{"oidc: missing claim", nil, func(t *testing.T, w *world) error {
			_, err := w.oidc.Verify(testContext(t), actionsToken(t, jwksKey, map[string]any{"sha": "", "ref": marker}), audience)
			return err
		}},
		{"oidc: expired token", nil, func(t *testing.T, w *world) error {
			_, err := w.oidc.Verify(testContext(t), actionsToken(t, jwksKey, map[string]any{"exp": 1, "ref": marker}), audience)
			return err
		}},
		{"oidc: keys unavailable", answer(hostOIDC, "/", http.StatusInternalServerError, leak), func(t *testing.T, w *world) error {
			_, err := w.oidc.Verify(testContext(t), actionsToken(t, jwksKey, map[string]any{"ref": marker}), audience)
			if !errors.Is(err, oidc.ErrUnavailable) {
				t.Errorf("error = %v, want ErrUnavailable", err)
			}
			return err
		}},
	}

	keyText := string(keyPEM(appKey))[64:128]
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.fake.setOverride(tc.override)
			err := tc.run(t, w)
			if err == nil {
				t.Fatal("no error: the path under test did not fail")
			}
			text := err.Error()
			if strings.Contains(text, marker) {
				t.Errorf("error leaks a marker: %s", text)
			}
			if strings.Contains(text, keyText) || strings.Contains(text, "eyJ") {
				t.Errorf("error leaks key or token text: %s", text)
			}
		})
	}
}
