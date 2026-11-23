package ui

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate"
)

const requiredAPI = "/api/repos/" + repoName + "/setup/required"

func TestRequiredPreviewReturnsTheDigestDiffAndTarget(t *testing.T) {
	e := newEnv(t)
	res := e.as("alice", permRead).postJSON(requiredAPI+"/preview", map[string]any{})
	wantStatus(t, res, http.StatusOK)
	var body editPreviewJSON
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Digest != "sha256:"+sha(3) || body.Target != sha(7) || body.Against != "the default branch" ||
		body.Diff != "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n+gate: required\n" {
		t.Fatalf("body %+v", body)
	}
	if len(e.answers.users) != 1 || e.answers.users[0].Login != "alice" || e.answers.repos[0].FullName != repoName {
		t.Fatalf("input not scoped: %+v %+v", e.answers.users, e.answers.repos)
	}
}

func TestRequiredAppliesWithTheTokenOnly(t *testing.T) {
	e := newEnv(t)
	e.answers.out = gate.AnswerOutcome{PullRequest: 42, State: gate.AnswerMerged}
	res := e.as("alice", permRead).postJSON(requiredAPI, map[string]any{"token": "tok"})
	wantStatus(t, res, http.StatusOK)
	var body answeredJSON
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PullRequest != 42 || body.State != gate.AnswerMerged ||
		body.Notice != "The check is required; the policy change merged in #42." {
		t.Fatalf("body %+v", body)
	}
	if len(e.answers.tokens) != 1 || e.answers.tokens[0] != "tok" || e.answers.users[0].Login != "alice" {
		t.Fatalf("apply got tokens %v users %+v", e.answers.tokens, e.answers.users)
	}
}

func TestRequiredNeverTakesPolicyBytesFromTheClient(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	payload := map[string]any{"token": "tok", "yaml": policyText}
	wantStatus(t, c.postJSON(requiredAPI, payload), http.StatusUnprocessableEntity)
	wantStatus(t, c.postJSON(requiredAPI+"/preview", payload), http.StatusUnprocessableEntity)
	// A body that names another repository or user cannot reach the service either.
	for _, extra := range []string{"repo", "user", "digest"} {
		payload := map[string]any{"token": "tok", extra: "x"}
		wantStatus(t, c.postJSON(requiredAPI, payload), http.StatusUnprocessableEntity)
	}
	if len(e.answers.tokens) != 0 {
		t.Fatalf("a rejected body reached the service: %v", e.answers.tokens)
	}
}

func TestRequiredRefusesANonOwner(t *testing.T) {
	e := newEnv(t)
	e.answers.err = gate.ErrNotPolicyOwner
	res := e.as("alice", permRead).postJSON(requiredAPI+"/preview", map[string]any{})
	wantStatus(t, res, http.StatusForbidden)
	res = e.as("alice", permRead).postJSON(requiredAPI, map[string]any{"token": "tok"})
	wantStatus(t, res, http.StatusForbidden)
}

func TestRequiredNeedsCSRFAndRepositoryAuthorization(t *testing.T) {
	e := newEnv(t)
	wantStatus(t, e.as("alice", permRead).postJSONWith(requiredAPI, map[string]any{"token": "tok"}, ""), http.StatusForbidden)
	wantStatus(t, e.signIn("mallory").postJSON(requiredAPI, map[string]any{"token": "tok"}), http.StatusNotFound)
	if len(e.answers.tokens) != 0 {
		t.Fatalf("an unauthorized caller reached the service: %v", e.answers.tokens)
	}
}

func TestRequiredStepUpBindsTheDigestTheAppComputed(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	digest := "sha256:" + sha(3)
	wantStatus(t, c.postJSON("/api/repos/"+repoName+"/passkeys/step-up",
		map[string]any{"action": gate.ActionRequired, "digest": digest}), http.StatusOK)
	calls := e.passkeys.all()
	if len(calls) != 1 || calls[0].purpose.Action != gate.ActionRequired || calls[0].purpose.Digest != digest {
		t.Fatalf("step-up %+v", calls)
	}
	// A digest the App did not compute does not start a step-up.
	wantStatus(t, c.postJSON("/api/repos/"+repoName+"/passkeys/step-up",
		map[string]any{"action": gate.ActionRequired, "digest": "sha256:" + sha(9)}), http.StatusConflict)
	if len(e.passkeys.all()) != 1 {
		t.Fatal("a mismatched digest began a step-up")
	}
	// The step-up never takes policy bytes from the page either.
	wantStatus(t, c.postJSON("/api/repos/"+repoName+"/passkeys/step-up",
		map[string]any{"action": gate.ActionRequired, "digest": digest, "yaml": policyText}), http.StatusUnprocessableEntity)
	if len(e.passkeys.all()) != 1 {
		t.Fatal("policy bytes began a step-up")
	}
}
