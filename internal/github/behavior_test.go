package github_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

func TestUninstallAndRepositoryRemoval(t *testing.T) {
	w := newWorld(t)
	ctx := testContext(t)
	widgets := api.RepoRef{InstallationID: instA, FullName: repoWidgets}
	gadgets := api.RepoRef{InstallationID: instA, FullName: repoGadgets}

	// Both answers are now in api's conditional-request cache and the token is in auth's.
	if _, _, err := w.api.File(ctx, widgets, "main", filePath); err != nil {
		t.Fatalf("File before removal: %v", err)
	}
	if _, err := w.api.OpenPulls(ctx, gadgets); err != nil {
		t.Fatalf("OpenPulls before removal: %v", err)
	}

	deliver := func(eventType string, payload map[string]any) webhook.Event {
		t.Helper()
		body := mustJSON(t, payload)
		ev, err := receive(signDelivery(webhookSecret, body), eventType, "delivery-"+eventType, body)
		if err != nil {
			t.Fatalf("%s delivery: %v", eventType, err)
		}
		return ev
	}

	ev := deliver("installation_repositories", map[string]any{
		"action": "removed", "installation": map[string]any{"id": instA},
		"repositories_removed": []map[string]any{{"full_name": repoWidgets, "private": true}},
	})
	if ev.Kind != webhook.ReposRemoved || ev.InstallationID != instA {
		t.Fatalf("event = %+v, want %s for installation %d", ev, webhook.ReposRemoved, instA)
	}
	w.fake.removeRepo(instA, repoWidgets)

	if content, _, err := w.api.File(ctx, widgets, "main", filePath); !errors.Is(err, api.ErrNotFound) || content != nil {
		t.Errorf("File of the removed repository = %q, %v; want ErrNotFound", content, err)
	}
	if _, err := w.api.OpenPulls(ctx, gadgets); err != nil {
		t.Errorf("OpenPulls of the remaining repository: %v", err)
	}

	ev = deliver("installation", map[string]any{"action": "deleted", "installation": map[string]any{"id": instA}})
	if ev.Kind != webhook.InstallationRemoved || ev.InstallationID != instA {
		t.Fatalf("event = %+v, want %s for installation %d", ev, webhook.InstallationRemoved, instA)
	}
	w.fake.deleteInstallation(instA)

	mark := w.fake.mark()
	pulls, err := w.api.OpenPulls(ctx, gadgets)
	if err == nil || pulls != nil {
		t.Fatalf("OpenPulls of the deleted installation = %v, %v; want an error and no list", pulls, err)
	}
	mint := w.fake.find(t, mark, "POST", "/app/installations/101/access_tokens")
	if mint.status != http.StatusNotFound {
		t.Errorf("token mint answered %d, want 404", mint.status)
	}
	if token, err := w.tokens.InstallationToken(ctx, instA); err == nil {
		t.Errorf("InstallationToken of the deleted installation = %q, want an error", token)
	}
}

func TestRateLimitedBranchNeverReturnsData(t *testing.T) {
	ref := api.RepoRef{InstallationID: instA, FullName: repoWidgets}
	check := api.CheckRun{Name: "archfit", HeadSHA: pullHeadSHA, Status: "in_progress", Title: "gate", Summary: "pending"}

	// sequence is what a gate consumer does for one pull request; every step must fail without data.
	sequence := func(t *testing.T, w *world, wantRateLimited bool) {
		t.Helper()
		ctx := testContext(t)
		verify := func(call string, err error, empty bool) {
			t.Helper()
			if err == nil || !empty {
				t.Errorf("%s: error %v, data returned %t; want an error and no data", call, err, !empty)
			}
			if wantRateLimited && !errors.Is(err, api.ErrRateLimited) {
				t.Errorf("%s: error = %v, want ErrRateLimited", call, err)
			}
		}
		start := time.Now()
		pulls, err := w.api.OpenPulls(ctx, ref)
		verify("OpenPulls", err, pulls == nil)
		runs, err := w.api.CheckRuns(ctx, ref, pullHeadSHA, check.Name)
		verify("CheckRuns", err, runs == nil)
		created, err := w.api.CreateCheckRun(ctx, ref, check)
		verify("CreateCheckRun", err, created.ID == 0)

		// A 10-minute limit cannot fit the 30 s budget, so nothing may wait for it.
		if elapsed := time.Since(start); elapsed > 5*time.Second || ctx.Err() != nil {
			t.Errorf("sequence took %s (context: %v), want an answer at once", elapsed, ctx.Err())
		}
		if got := w.fake.checkRunCount(); got != 0 {
			t.Errorf("check runs written = %d, want 0", got)
		}
	}

	t.Run("with a cached token", func(t *testing.T) {
		w := newWorld(t)
		if _, err := w.api.OpenPulls(testContext(t), ref); err != nil {
			t.Fatalf("OpenPulls before the limit: %v", err)
		}
		w.fake.setOverride(rateLimitAll())
		mark := w.fake.mark()
		sequence(t, w, true)
		if got := len(w.fake.since(mark)); got != 3 {
			t.Errorf("requests = %d, want 3: one per call, no retry inside a 10-minute limit", got)
		}
	})

	// auth has no rate-limit sentinel, so a limited token mint is a plain error: still never data.
	t.Run("with no token yet", func(t *testing.T) {
		w := newWorld(t)
		w.fake.setOverride(rateLimitAll())
		sequence(t, w, false)
	})
}

func TestUserLoginToAuthorizedRepository(t *testing.T) {
	w := newWorld(t)
	ctx := testContext(t)

	const state, redirect = "state-123", "https://archfit.example/auth/callback"
	login, err := url.Parse(w.oauth.AuthorizeURL(state, redirect))
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	q := login.Query()
	if login.Host != hostWeb || q.Get("client_id") != oauthClientID || q.Get("state") != state || q.Get("redirect_uri") != redirect {
		t.Errorf("AuthorizeURL = %s", login)
	}
	if strings.Contains(login.String(), marker) {
		t.Errorf("AuthorizeURL carries a secret: %s", login)
	}

	user, err := w.oauth.Exchange(ctx, oauthCode)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if user.AccessToken != userAccessToken || user.Login != userLogin || user.UserID != userID {
		t.Fatalf("Exchange = %+v", user)
	}

	mark := w.fake.mark()
	permission, err := w.api.Permission(ctx, user.AccessToken, repoWidgets)
	if err != nil || permission != "write" {
		t.Fatalf("Permission = %q, %v; want write", permission, err)
	}
	asked := w.fake.since(mark)
	if len(asked) != 1 || asked[0].cred != credUser || asked[0].path != repoPrefix+repoWidgets {
		t.Errorf("Permission requests = %+v, want one with the user token", asked)
	}

	mark = w.fake.mark()
	repo, err := w.api.ResolveRepo(ctx, repoWidgets)
	if err != nil || repo.InstallationID != instA {
		t.Fatalf("ResolveRepo = %+v, %v; want installation %d", repo, err, instA)
	}
	if got := w.fake.find(t, mark, "GET", repoPrefix+repoWidgets+"/installation").cred; got != credJWT {
		t.Errorf("installation lookup credential = %s, want the App JWT", got)
	}
	for _, r := range w.fake.since(mark) {
		if r.cred == credUser {
			t.Errorf("%s %s used the user token on the App path", r.method, r.path)
		}
	}

	// GitHub decides both answers: a repository the user cannot see is "none" even though the App covers it.
	if permission, err := w.api.Permission(ctx, user.AccessToken, repoGadgets); err != nil || permission != "none" {
		t.Errorf("Permission on an unshared repository = %q, %v; want none", permission, err)
	}
	if repo, err := w.api.ResolveRepo(ctx, repoGadgets); err != nil || repo.InstallationID != instA {
		t.Errorf("ResolveRepo of the unshared repository = %+v, %v", repo, err)
	}
}
