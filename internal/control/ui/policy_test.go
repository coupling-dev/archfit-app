package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

type fakePolicyViews struct {
	state gate.PolicyState
	err   error
}

func (f *fakePolicyViews) PolicyState(_ context.Context, _ api.Repo) (gate.PolicyState, error) {
	return f.state, f.err
}

const policyAPI = "/api/repos/" + repoName + "/policy"

func TestPolicyGolden(t *testing.T) {
	for _, name := range []string{"typical", "pumba"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata/policy", name+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			e := newEnv(t)
			e.gh.setFile(repoName, mainBranch, ".archfit.yaml", string(raw))
			e.policy.state.ApprovedDigest = policy3.Digest(raw)
			res := e.as("alice", permRead).get(policyAPI)
			wantStatus(t, res, http.StatusOK)
			var body policyPageJSON
			if err = json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Digest != policy3.Digest(raw) || body.YAML != string(raw) || !body.Edit.Available {
				t.Fatalf("wrong content/digest/edit: %+v", body.Edit)
			}
			var pretty bytes.Buffer
			if err = json.Indent(&pretty, res.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata/policy", name+".json")
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if pretty.String()+"\n" != string(want) {
				t.Errorf("response does not match %s", path)
			}
		})
	}
}

func TestPolicyRequestLinksOnlyFromFirstLineComment(t *testing.T) {
	const input = `version: 3
owners: [alice]
modules:
  a: {paths: [a/**]}
exceptions:
  - rule: unowned # request #12
    from: a/**
    why: Reason
  - rule: unowned
    from: a/**
    why: "Request #12"
  - rule: "unowned #12"
    from: a/**
    to: a
    why: Reason
  # request #12
  - rule: unowned
    from: a/**
    why: Reason
`
	e := newEnv(t)
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", input)
	var body policyPageJSON
	e.as("alice", permRead).getJSON(policyAPI, &body)
	if len(body.Exceptions) != 4 {
		t.Fatalf("exceptions: %+v", body.Exceptions)
	}
	for i, x := range body.Exceptions {
		if i == 0 {
			if x.Request != 12 || x.RequestHref != "/r/acme/app/requests/12" {
				t.Errorf("first: %+v", x)
			}
		} else if x.Request != 0 || x.RequestHref != "" {
			t.Errorf("%d: %+v", i, x)
		}
	}
}

func TestPolicyErrorsAndEdit(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	res := c.get(policyAPI)
	wantStatus(t, res, http.StatusConflict)
	if !strings.Contains(res.Body.String(), "version 2") || !strings.Contains(res.Body.String(), "archfit init") {
		t.Fatal(res.Body.String())
	}
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", "version: [")
	wantStatus(t, c.get(policyAPI), http.StatusConflict)
	raw, err := os.ReadFile("testdata/policy/typical.yaml")
	if err != nil {
		t.Fatal(err)
	}
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", string(raw))
	for _, tt := range []struct {
		name      string
		state     gate.PolicyState
		available bool
	}{{"unapproved", gate.PolicyState{}, false}, {"outside", gate.PolicyState{ApprovedDigest: "x", OutsideFlow: true}, false}, {"approved", gate.PolicyState{ApprovedDigest: "x"}, true}} {
		t.Run(tt.name, func(t *testing.T) {
			e.policy.state = tt.state
			var body policyPageJSON
			c.getJSON(policyAPI+"?edit=1", &body)
			if body.Edit.Available != tt.available || (!tt.available && body.Edit.Reason == "") {
				t.Fatalf("edit: %+v", body.Edit)
			}
			if tt.available && (body.Edit.File != string(raw) || body.Edit.Digest != policy3.Digest(raw)) {
				t.Fatalf("edit: %+v", body.Edit)
			}
		})
	}
	denied := e.signIn("mallory").get(policyAPI)
	missing := e.signIn("mallory").get("/api/repos/acme/missing/policy")
	wantStatus(t, denied, http.StatusNotFound)
	wantStatus(t, missing, http.StatusNotFound)
	if denied.Body.String() != missing.Body.String() {
		t.Fatalf("access denied differs from missing: %s vs %s", denied.Body.String(), missing.Body.String())
	}
}

func TestPolicyEditUsesOpenProposalHead(t *testing.T) {
	e := newEnv(t)
	raw, err := os.ReadFile("testdata/policy/typical.yaml")
	if err != nil {
		t.Fatal(err)
	}
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", string(raw))
	proposal := string(raw) + "\n# proposal\n"
	head := sha(123)
	e.gh.setFile(repoName, head, ".archfit.yaml", proposal)
	e.gh.pulls[repoName+"#11"] = api.Pull{Number: 11, State: "open", HeadRef: "archfit/policy", HeadRepo: repoName, BaseRef: mainBranch, HeadSHA: head}
	e.policy.state.ApprovedDigest = policy3.Digest(raw)
	var body policyPageJSON
	e.as("alice", permRead).getJSON(policyAPI+"?edit=1", &body)
	if body.YAML != string(raw) || body.Edit.File != proposal || body.Edit.Digest != policy3.Digest([]byte(proposal)) {
		t.Fatalf("proposal: %+v", body.Edit)
	}
}

func TestPolicyBuiltinsAndExpiry(t *testing.T) {
	raw := `version: 3
owners: [alice]
modules:
  a: {paths: [a/**], public: [a/api], volatility: high, layer: core, depends_on: [b]}
builtin: {graph: deny, coupling: ask, duplication: off, cycle: deny, layers: ask, unowned: off, min_balance: 7}
exceptions:
  - rule: unowned # request #431
    from: a/**
    expires: 2026-09-22
    why: accepted
`
	e := newEnv(t)
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", raw)
	var body policyPageJSON
	e.as("alice", permRead).getJSON(policyAPI, &body)
	if body.Builtin.Graph != "deny" || body.Builtin.Coupling != "ask" || body.Builtin.Duplication != "off" || body.Builtin.Cycle != "deny" || body.Builtin.Layers != "ask" || body.Builtin.Unowned != "off" || body.Builtin.MinBalance == nil || *body.Builtin.MinBalance != 7 {
		t.Fatalf("builtin: %+v", body.Builtin)
	}
	if len(body.Exceptions) != 1 || body.Exceptions[0].DaysLeft == nil || *body.Exceptions[0].DaysLeft != 1 || body.Exceptions[0].Request != 431 {
		t.Fatalf("exception: %+v", body.Exceptions)
	}
	if len(body.Modules) != 1 || body.Modules[0].Name != "a" || body.Modules[0].Public[0] != "a/api" || body.Modules[0].DependsOn[0] != "b" {
		t.Fatalf("module: %+v", body.Modules)
	}
}
