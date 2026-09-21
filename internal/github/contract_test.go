package github_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

const repoPrefix = "/repos/"

func TestCredentialChain(t *testing.T) {
	w := newWorld(t)
	ctx := testContext(t)
	appKey, _ := keys(t)

	repo, err := w.api.ResolveRepo(ctx, repoWidgets)
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if repo.InstallationID != instA || repo.FullName != repoWidgets {
		t.Fatalf("ResolveRepo = %+v, want installation %d of %s", repo.RepoRef, instA, repoWidgets)
	}
	lookup := w.fake.find(t, 0, "GET", repoPrefix+repoWidgets+"/installation")
	claims, ok := verifyRS256(lookup.bearer, &appKey.PublicKey)
	if !ok {
		t.Fatal("ResolveRepo carried no JWT that verifies against the App key")
	}
	if claims["iss"] != strconv.Itoa(appID) {
		t.Errorf("JWT iss = %v, want %d", claims["iss"], appID)
	}

	for range 3 {
		mark := w.fake.mark()
		content, blobSHA, err := w.api.File(ctx, repo.RepoRef, "main", filePath)
		if err != nil {
			t.Fatalf("File: %v", err)
		}
		if string(content) != fileContent || blobSHA == "" {
			t.Errorf("File = %q, %q", content, blobSHA)
		}
		if got := w.fake.find(t, mark, "GET", repoPrefix+repoWidgets+"/contents/"+filePath).cred; got != credInst(instA) {
			t.Errorf("File credential = %s, want %s", got, credInst(instA))
		}
	}
	if got := w.fake.mintCount(instA); got != 1 {
		t.Errorf("token mints = %d, want 1 for repeated calls", got)
	}
	if got := w.fake.mintCount(instB); got != 0 {
		t.Errorf("token mints for the other installation = %d, want 0", got)
	}
}

func TestRevokedTokenIsReplacedOnce(t *testing.T) {
	ref := api.RepoRef{InstallationID: instA, FullName: repoWidgets}
	apiRequests := func(w *world, mark int) (n int) {
		for _, r := range w.fake.since(mark) {
			if !r.isMint() {
				n++
			}
		}
		return n
	}

	t.Run("revoked token", func(t *testing.T) {
		w := newWorld(t)
		ctx := testContext(t)
		if _, _, err := w.api.File(ctx, ref, "main", filePath); err != nil {
			t.Fatalf("File with a fresh token: %v", err)
		}
		w.fake.revokeTokens(instA)

		mark := w.fake.mark()
		content, _, err := w.api.File(ctx, ref, "main", filePath)
		if err != nil || string(content) != fileContent {
			t.Fatalf("File after revocation = %q, %v", content, err)
		}
		if got := w.tokens.invalidations.Load(); got != 1 {
			t.Errorf("invalidations = %d, want 1", got)
		}
		if got := w.fake.mintCount(instA); got != 2 {
			t.Errorf("token mints = %d, want 2 (first token, then its replacement)", got)
		}
		if got := apiRequests(w, mark); got != 2 {
			t.Errorf("API requests = %d, want 2 (the refused one and one retry)", got)
		}
	})

	t.Run("every token refused", func(t *testing.T) {
		w := newWorld(t)
		w.fake.setOverride(rejectEveryToken)

		content, _, err := w.api.File(testContext(t), ref, "main", filePath)
		if err == nil || content != nil {
			t.Fatalf("File = %q, %v; want an error and no content", content, err)
		}
		if errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrRateLimited) {
			t.Errorf("error = %v, want a plain authentication failure", err)
		}
		if got := apiRequests(w, 0); got != 2 {
			t.Errorf("API requests = %d, want exactly 2", got)
		}
		if got := w.tokens.invalidations.Load(); got != 1 {
			t.Errorf("invalidations = %d, want 1", got)
		}
	})
}

func TestWebhookEventAddressesAPI(t *testing.T) {
	w := newWorld(t)
	body := pullRequestDelivery(t, instA, repoWidgets)

	ev, err := receive(signDelivery(webhookSecret, body), "pull_request", "delivery-1", body)
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if ev.Kind != webhook.PullOpened {
		t.Fatalf("Kind = %s, want %s", ev.Kind, webhook.PullOpened)
	}

	ref := api.RepoRef{InstallationID: ev.InstallationID, FullName: ev.Repo}
	pull, err := w.api.Pull(testContext(t), ref, ev.PullRequest)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	got := w.fake.find(t, 0, "GET", repoPrefix+repoWidgets+"/pulls/"+strconv.Itoa(pullNumber))
	if got.cred != credInst(instA) {
		t.Errorf("Pull credential = %s, want the token of the event's installation", got.cred)
	}
	if pull.HeadSHA != ev.HeadSHA || pull.HeadSHA != pullHeadSHA {
		t.Errorf("HeadSHA: API %q, event %q, fixture %q", pull.HeadSHA, ev.HeadSHA, pullHeadSHA)
	}
	if pull.HeadRepo != ev.HeadRepo || pull.BaseRef != ev.BaseRef {
		t.Errorf("API %s→%s, event %s→%s", pull.HeadRepo, pull.BaseRef, ev.HeadRepo, ev.BaseRef)
	}
}

func TestOIDCClaimsResolveToInstallation(t *testing.T) {
	w := newWorld(t)
	ctx := testContext(t)
	_, jwksKey := keys(t)

	claims, err := w.oidc.Verify(ctx, actionsToken(t, jwksKey, nil), audience)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	repo, err := w.api.ResolveRepo(ctx, claims.Repository)
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if repo.InstallationID != instA {
		t.Fatalf("installation = %d, want %d", repo.InstallationID, instA)
	}

	mark := w.fake.mark()
	run, err := w.api.Run(ctx, repo.RepoRef, claims.RunID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := w.fake.find(t, mark, "GET", repoPrefix+repoWidgets+"/actions/runs/"+strconv.Itoa(runID)).cred; got != credInst(instA) {
		t.Errorf("Run credential = %s, want the resolved installation's token", got)
	}
	fromClaims := oidc.WorkflowPath(claims.JobWorkflowRef, claims.Repository)
	if fromClaims != workflowPath || run.WorkflowPath != fromClaims {
		t.Errorf("WorkflowPath: claims %q, run %q, fixture %q", fromClaims, run.WorkflowPath, workflowPath)
	}
	if run.HeadSHA != claims.SHA {
		t.Errorf("HeadSHA: run %q, claims %q", run.HeadSHA, claims.SHA)
	}
}
