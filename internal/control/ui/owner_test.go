package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type fakeSetups struct {
	out   onboarding.Setup
	repos []api.Repo
}

func (f *fakeSetups) Setup(_ context.Context, r api.Repo) (onboarding.Setup, error) {
	f.repos = append(f.repos, r)
	return f.out, nil
}

type fakeOwners struct {
	repos   []api.Repo
	users   []gate.OwnerUser
	actions []answers.OwnerAction
	err     error
	view    gate.OwnerView
}

func (f *fakeOwners) OwnerDigest(_ context.Context, r api.Repo, a answers.OwnerAction) (string, error) {
	f.repos = append(f.repos, r)
	f.actions = append(f.actions, a)
	return "sha256:bound", f.err
}
func (f *fakeOwners) OwnerAct(_ context.Context, r api.Repo, u gate.OwnerUser, a answers.OwnerAction) (gate.OwnerOutcome, error) {
	f.repos = append(f.repos, r)
	f.actions = append(f.actions, a)
	f.users = append(f.users, u)
	return gate.OwnerOutcome{Approval: "a1", PullRequest: 12, State: gate.AnswerLater, Merge: sha(3), Issue: 70, Check: gate.CheckPending}, f.err
}

const setupAPI = "/api/repos/" + repoName + "/setup"
const ownerAPI = "/api/repos/" + repoName + "/owner/"

func TestSetupAPIRequiresRepositoryReadAndReturnsExplicitState(t *testing.T) {
	for _, permission := range []string{permRead, permWrite, permAdmin} {
		t.Run(permission, func(t *testing.T) {
			e := newEnv(t)
			e.setups.out = onboarding.Setup{Admitted: true, State: "outside_flow", Missing: []string{}, FirstPull: 12, Issue: 70,
				Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{{Code: "gate_check", State: "warning", Text: "Require archfit/gate."}}}}
			res := e.as("alice", permission).get(setupAPI)
			wantStatus(t, res, http.StatusOK)
			var body setupJSON
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !body.Admitted || body.State != "outside_flow" || body.FirstPull != 12 || body.Issue != 70 || len(body.Rules) != 1 || body.Rules[0].Code != "gate_check" || body.Missing == nil {
				t.Fatalf("body = %s", res.Body.String())
			}
			if len(e.setups.repos) != 1 || e.setups.repos[0].FullName != repoName {
				t.Fatalf("repos = %+v", e.setups.repos)
			}
		})
	}
	t.Run("no repository access", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.signIn("mallory").get(setupAPI), http.StatusNotFound)
		if len(e.setups.repos) > 0 {
			t.Fatal("read without repository access")
		}
	})
	t.Run("signed out", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.anonymous().get(setupAPI), http.StatusUnauthorized)
		if len(e.setups.repos) > 0 {
			t.Fatal("read while signed out")
		}
	})
}

func TestOwnerAPIsPassSignedInUserAndPasskeyBinding(t *testing.T) {
	for _, tt := range []struct {
		kind string
		pull int
	}{
		{gate.OwnerFirst, 12}, {gate.OwnerFirst, 0}, {gate.OwnerWorkflow, 12}, {gate.OwnerConfirm, 0}, {gate.OwnerRevert, 0},
	} {
		name := tt.kind
		if tt.pull > 0 {
			name += "-pull"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			permission := permRead
			if tt.kind == gate.OwnerFirst {
				permission = permAdmin
			}
			c := e.as("alice", permission)
			res := c.postJSON(ownerAPI+tt.kind+"/digest", map[string]any{"pull_request": tt.pull})
			wantStatus(t, res, http.StatusOK)
			var digest answerDigestJSON
			if err := json.Unmarshal(res.Body.Bytes(), &digest); err != nil || digest.Digest != "sha256:bound" {
				t.Fatalf("digest %s, %v", res.Body.String(), err)
			}
			res = c.postJSON(ownerAPI+tt.kind, map[string]any{"pull_request": tt.pull, "digest": digest.Digest, "merge_later": true, "token": "tok"})
			wantStatus(t, res, http.StatusOK)
			var body ownerOutcomeJSON
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body != (ownerOutcomeJSON{Approval: "a1", Pull: 12, State: gate.AnswerLater, Merge: sha(3), Issue: 70, Check: gate.CheckPending}) {
				t.Fatalf("body = %+v", body)
			}
			if len(e.owners.actions) != 2 || e.owners.actions[0] != (answers.OwnerAction{Kind: tt.kind, Pull: tt.pull}) ||
				e.owners.actions[1] != (answers.OwnerAction{Kind: tt.kind, Pull: tt.pull, Digest: "sha256:bound", Later: true, Token: "tok"}) {
				t.Fatalf("actions = %+v", e.owners.actions)
			}
			if len(e.owners.users) != 1 || e.owners.users[0] != (gate.OwnerUser{ID: e.user("alice").UserID, Login: "alice", Admin: tt.kind == gate.OwnerFirst}) || e.owners.repos[1].FullName != repoName {
				t.Fatalf("users = %+v, repos = %+v", e.owners.users, e.owners.repos)
			}
		})
	}
}

func TestOwnerRouteAuthorizationDoesNotRequireGitHubWrite(t *testing.T) {
	for _, permission := range []string{permRead, permWrite, permAdmin} {
		t.Run(permission, func(t *testing.T) {
			e := newEnv(t)
			wantStatus(t, e.as("alice", permission).postJSON(ownerAPI+"confirm", map[string]any{"digest": "seen", "token": "tok"}), http.StatusOK)
			if len(e.owners.users) != 1 {
				t.Fatal("owner action did not reach the gate")
			}
		})
	}
	for _, suffix := range []string{"", "/digest"} {
		t.Run("no access"+suffix, func(t *testing.T) {
			e := newEnv(t)
			wantStatus(t, e.signIn("mallory").postJSON(ownerAPI+"first"+suffix, map[string]any{}), http.StatusNotFound)
			if len(e.owners.actions) > 0 {
				t.Fatal("action without repository access")
			}
		})
		t.Run("no CSRF"+suffix, func(t *testing.T) {
			e := newEnv(t)
			wantStatus(t, e.as("alice", permRead).postJSONWith(ownerAPI+"first"+suffix, map[string]any{}, ""), http.StatusForbidden)
			if len(e.owners.actions) > 0 {
				t.Fatal("action without CSRF")
			}
		})
	}
}

func TestOwnerAPIRefusals(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{
		{"first needs admin", gate.ErrOwnerNotAdmin, http.StatusForbidden},
		{"workflow confirm revert need approved owner", gate.ErrOwnerNotOwner, http.StatusForbidden},
		{"needs passkey", gate.ErrOwnerNeedsPasskey, http.StatusForbidden},
		{"used token", passkey.ErrToken, http.StatusForbidden},
		{"already approved", gate.ErrOwnerNotDue, http.StatusConflict},
		{"cannot approve", gate.ErrOwnerNotPossible, http.StatusConflict},
		{"head moved", gate.ErrOwnerMoved, http.StatusConflict},
		{"not outside flow", gate.ErrNotOutsideFlow, http.StatusConflict},
		{"nothing to revert", gate.ErrNothingToRevert, http.StatusConflict},
		{"revert moved", gate.ErrRevertMoved, http.StatusConflict},
		{"revert not owner", gate.ErrRevertNotOwner, http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, suffix := range []string{"", "/digest"} {
				e := newEnv(t)
				e.owners.err = tt.err
				wantStatus(t, e.as("alice", permAdmin).postJSON(ownerAPI+"first"+suffix, map[string]any{}), tt.status)
			}
		})
	}
}

func TestOwnerAPIRejectsInvalidActionsBeforeCallingGate(t *testing.T) {
	for _, tt := range []struct {
		kind   string
		body   map[string]any
		status int
	}{
		{"unknown", map[string]any{}, http.StatusNotFound},
		{"first", map[string]any{"pull_request": -1}, http.StatusUnprocessableEntity},
		{"workflow", map[string]any{}, http.StatusUnprocessableEntity},
		{"confirm", map[string]any{"pull_request": 12}, http.StatusUnprocessableEntity},
		{"revert", map[string]any{"pull_request": 12}, http.StatusUnprocessableEntity},
		{"first", map[string]any{"user": "mallory"}, http.StatusUnprocessableEntity},
		{"first", map[string]any{"repo": "rival/secret"}, http.StatusUnprocessableEntity},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			e := newEnv(t)
			wantStatus(t, e.as("alice", permRead).postJSON(ownerAPI+tt.kind, tt.body), tt.status)
			if len(e.owners.actions) > 0 {
				t.Fatalf("gate called with invalid action: %+v", e.owners.actions)
			}
		})
	}
}

func (f *fakeOwners) OwnerView(_ context.Context, r api.Repo, a answers.OwnerAction) (gate.OwnerView, error) {
	f.repos = append(f.repos, r)
	f.actions = append(f.actions, a)
	return f.view, f.err
}

func TestOwnerAPIPreservesBothDisplayedBindings(t *testing.T) {
	e := newEnv(t)
	res := e.as("alice", permRead).postJSON(ownerAPI+"revert", map[string]any{"digest": "sha256:original", "view_digest": "sha256:view", "token": "view-token"})
	wantStatus(t, res, http.StatusOK)
	if len(e.owners.actions) != 1 {
		t.Fatal("action not forwarded")
	}
	a := e.owners.actions[0]
	if a.Digest != "sha256:original" || a.ViewDigest != "sha256:view" || a.Token != "view-token" {
		t.Fatalf("bindings = %+v", a)
	}
}
