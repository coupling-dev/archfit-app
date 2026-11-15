package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

func (f *fakeAnswers) Editor(_ context.Context, _ api.Repo) (gate.EditBase, error) {
	return f.editor, f.err
}

func (f *fakeAnswers) ReloadEditor(_ context.Context, _ api.Repo, _ passkey.User) (gate.EditBase, error) {
	return f.editor, f.err
}

func (f *fakeAnswers) PreviewEdit(_ context.Context, r api.Repo, u passkey.User, a answers.EditAnswer) (gate.EditView, error) {
	f.repos = append(f.repos, r)
	f.users = append(f.users, u)
	f.edits = append(f.edits, a)
	if problems := policy3.Diagnostics([]byte(a.YAML)); len(problems) > 0 {
		return gate.EditView{Errors: problems}, f.err
	}
	return gate.EditView{Digest: policy3.Digest([]byte(a.YAML)), Target: sha(7), Diff: "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n", Errors: policy3.Diagnostics([]byte(a.YAML))}, f.err
}

func (f *fakeAnswers) Edit(_ context.Context, r api.Repo, u passkey.User, a answers.EditAnswer) (gate.AnswerOutcome, error) {
	f.repos = append(f.repos, r)
	f.users = append(f.users, u)
	f.edits = append(f.edits, a)
	return f.out, f.err
}

func TestEditAPIHasStrictScopeCSRFAndJSON(t *testing.T) {
	for _, suffix := range []string{"/edit/preview", "/edit"} {
		t.Run(suffix, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permRead)
			payload := map[string]any{"answer": "edit", "yaml": policyText, "base_digest": "sha256:base", "target": sha(7), "token": "tok"}
			res := c.postJSON(policyAPI+suffix, payload)
			wantStatus(t, res, http.StatusOK)
			if len(e.answers.edits) != 1 || e.answers.edits[0].YAML != policyText || e.answers.edits[0].Token != "tok" ||
				e.answers.users[0].Login != "alice" || e.answers.repos[0].FullName != repoName {
				t.Fatal("input not scoped", e.answers.edits)
			}
			wantStatus(t, c.postJSONWith(policyAPI+suffix, payload, ""), http.StatusForbidden)
			payload["user"] = "mallory"
			wantStatus(t, c.postJSON(policyAPI+suffix, payload), http.StatusUnprocessableEntity)
			delete(payload, "user")
			payload["yaml"] = strings.Repeat("x", gate.MaxEditBytes+1)
			wantStatus(t, c.postJSON(policyAPI+suffix, payload), http.StatusRequestEntityTooLarge)
			wantStatus(t, e.signIn("mallory").postJSON(policyAPI+suffix, payload), http.StatusNotFound)
			if len(e.answers.edits) != 1 {
				t.Fatal("invalid write reached answers")
			}
		})
	}
}

func TestEditPreviewShowsAllLineErrorsAndNoPasskey(t *testing.T) {
	e := newEnv(t)
	res := e.as("alice", permRead).postJSON(policyAPI+"/edit/preview", map[string]any{
		"answer": "edit", "yaml": "version: 3\nowners: []\nmodules: {}\n", "base_digest": "sha256:base",
	})
	wantStatus(t, res, http.StatusOK)
	var body editPreviewJSON
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/policy/edit-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, res.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	if pretty.String()+"\n" != string(want) {
		t.Errorf("API golden mismatch:\n%s", pretty.String())
	}
	if len(body.Errors) != 2 {
		t.Fatalf("errors %+v", body.Errors)
	}
	for _, line := range body.Errors {
		if !strings.HasPrefix(line, ".archfit.yaml:") || !strings.Contains(line, ": policy: ") {
			t.Error(line)
		}
		if words := wording.NotUsedIn(line); len(words) > 0 {
			t.Errorf("%s uses %v", line, words)
		}
	}
	if len(e.passkeys.calls) != 0 {
		t.Fatal("preview asked for passkey")
	}
}

func TestEditorReloadUsesTheOrdinaryBoundaryAndNoPasskey(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	res := c.postJSON(policyAPI+"/edit/reload", map[string]any{})
	wantStatus(t, res, http.StatusOK)
	var got policyEditJSON
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.File != policyText || !got.Available || len(e.passkeys.calls) > 0 {
		t.Fatal(got)
	}
	wantStatus(t, c.postJSONWith(policyAPI+"/edit/reload", map[string]any{}, ""), http.StatusForbidden)
	wantStatus(t, c.postJSON(policyAPI+"/edit/reload", map[string]any{"yaml": "injected"}), http.StatusUnprocessableEntity)
	wantStatus(t, e.signIn("mallory").postJSON(policyAPI+"/edit/reload", map[string]any{}), http.StatusNotFound)
}

func TestEditJSONRefusesTrailingValues(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	raw := strings.NewReader("{\"answer\":\"edit\",\"yaml\":\"x\"} {}")
	req := httptest.NewRequest(http.MethodPost, baseURL+policyAPI+"/edit/preview", raw)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, c.csrf())
	wantStatus(t, c.do(req), http.StatusUnprocessableEntity)
	if len(e.answers.edits) != 0 {
		t.Fatal("trailing JSON reached answers")
	}
}

func TestInvalidEditCannotBeginAPasskeyPrompt(t *testing.T) {
	e := newEnv(t)
	res := e.as("alice", permRead).postJSON("/api/repos/"+repoName+"/passkeys/step-up", map[string]any{
		"action": "edit", "yaml": "version: 3\nowners: []\nmodules: {}\n", "base_digest": "sha256:base", "target": sha(7), "digest": "sha256:new",
	})
	wantStatus(t, res, http.StatusUnprocessableEntity)
	if len(e.passkeys.calls) != 0 {
		t.Fatal("invalid YAML began passkey step")
	}
}

func TestEditReloadSentence(t *testing.T) {
	e := newEnv(t)
	e.answers.err = gate.ErrEditChanged
	res := e.as("alice", permRead).postJSON(policyAPI+"/edit", map[string]any{"answer": "edit", "yaml": policyText, "base_digest": "old"})
	wantStatus(t, res, http.StatusConflict)
	var body errorJSON
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "the policy changed; reload" {
		t.Error(body.Error)
	}
	if words := wording.NotUsedIn(body.Error); len(words) > 0 {
		t.Fatal(words)
	}
}
