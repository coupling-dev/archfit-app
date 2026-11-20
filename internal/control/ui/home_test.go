package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/control/answers"
	"github.com/coupling-dev/archfit-app/internal/control/onboarding"
	"github.com/coupling-dev/archfit-app/internal/control/passkey"
	"github.com/coupling-dev/archfit-app/internal/control/ui/session"
	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
	"github.com/coupling-dev/archfit-app/internal/wire/wording"
)

const homeAPI = "/api/repos/" + repoName + "/home"
const homePolicy = "version: 3\nowners: [alice]\nlayers: [core, cmd]\nmodules:\n  app:\n    paths: [cmd/**]\n    layer: cmd\n    volatility: high\n    depends_on: [util]\n  util:\n    paths: [internal/util/**]\n    layer: core\n    subdomain: generic\n  other:\n    paths: [other/**]\n"
const homeDeny = "rules:\n  - id: no-app-to-other\n    deny: {from: app, to: other}\n    why: Keep this import out.\n    instead: Pass a value in.\n"

func newHomeEnv(t *testing.T, pol string) *env {
	t.Helper()
	if _, err := policy3.Decode([]byte(pol)); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	e.passkeys.listed = []passkey.Passkey{{ID: fakePasskeyID, Created: fakeCreated}}
	e.now = time.Date(2026, 11, 10, 23, 45, 0, 0, time.UTC)
	e.setups.out = onboarding.Setup{Admitted: true, Policy: true, Workflow: true, State: "approved", Missing: []string{},
		Approval: gate.ApprovalSummary{Status: gate.ApprovalActive, Kind: gate.OwnerFirst},
		Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{
			{Code: "pull_requests", State: "ok"},
			{Code: "gate_check", State: "ok"},
		}}}
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", pol)
	e.gh.setFile(repoName, mainBranch, workflow, "the target workflow")
	homePut(t, e, "repo", map[string]any{
		"schema": "archfit-app.repo.v1", "approved_digest": policy3.Digest([]byte(pol)),
		"drift": map[string]any{"head": sha(10), "state": "clean", "errors": []any{}, "unread": false},
	})
	clock := func() time.Time { return e.now }
	sessions, err := session.New(session.Config{CookieKey: cookieKey, BaseURL: baseURL}, e.oauth, e.gh, e.gh, clock)
	if err != nil {
		t.Fatal(err)
	}
	e.handler = newHandler(Deps{Account: e.acct,
		Admissions: e.admit, Owners: e.owners, HomeViews: gate.NewHome(e.gh, e.store, e.admit), Setups: e.setups, Passkeys: e.passkeys, Now: clock,
	}, github{files: e.gh, pulls: e.gh, users: e.gh, installs: e.gh}, sessions, testWeb)
	return e
}

// Persisted boundary fixtures avoid importing gate children into control.
func homePut(t *testing.T, e *env, kind string, value any) {
	t.Helper()
	parts := strings.SplitN(kind, "/", 2)
	key := "v4/" + parts[0] + "/" + repoName
	if len(parts) > 1 {
		key += "/" + parts[1]
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Put(t.Context(), store.Tenant(installA), key, raw); err != nil {
		t.Fatal(err)
	}
}

func homeEditRepo(t *testing.T, e *env, edit func(map[string]any)) {
	t.Helper()
	raw, err := e.store.Get(t.Context(), store.Tenant(installA), "v4/repo/"+repoName)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	edit(rec)
	homePut(t, e, "repo", rec)
}

func homeRequest(t *testing.T, e *env, state string) {
	t.Helper()
	homePut(t, e, "request-index", map[string]any{"schema": "archfit-app.request-index.v1",
		"entries": []any{map[string]any{"key": "graph app other", "id": "431"}}})
	homePut(t, e, "request/431", map[string]any{"schema": "archfit-app.request.v1", "key": "graph app other",
		"state": state, "reason": "Use the helper.", "rule": "graph", "from": "app", "to": "other",
		"record": "cmd/run.go:12: graph: app -> other is not an allowed dependency\n  balance: 9/10 (min 7)\n"})
}

func homeWithException(date string) string {
	return homePolicy + "exceptions:\n  - rule: graph\n    from: app\n    to: other\n    expires: " + date + "\n    why: Fix the import before this date.\n"
}

func TestHomeAPIGoldens(t *testing.T) {
	tests := []struct {
		name, policy string
		setup        func(*testing.T, *env)
	}{
		{"clean", homePolicy, nil},
		{"one-request", homePolicy, func(t *testing.T, e *env) { homeRequest(t, e, "open") }},
		{"exception-nine-days", homeWithException("2026-11-19"), nil},
		{"drift", homePolicy, func(t *testing.T, e *env) {
			homeEditRepo(t, e, func(rec map[string]any) {
				rec["drift_issue"] = 72
				rec["drift"] = map[string]any{"head": sha(10), "state": "drift", "unread": false,
					"errors": []any{map[string]any{"rule": "graph", "from": "app", "to": "other"}}}
			})
		}},
		{"outside-flow", homePolicy, func(t *testing.T, e *env) {
			homeEditRepo(t, e, func(rec map[string]any) {
				rec["outside_flow"] = map[string]any{"digest": "sha256:changed", "issue": 73}
			})
			e.setups.out.State = "outside_flow"
			e.setups.out.Issue = 73
		}},
		{"deny-request", homePolicy + homeDeny, func(t *testing.T, e *env) { homeRequest(t, e, "open") }},
		{"workflow-review", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out = onboarding.Setup{Approval: gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: gate.OwnerWorkflow, Pull: 203}, Policy: true, OpenPull: 203, State: "not_admitted", Missing: []string{},
				Rules: e.setups.out.Rules}
		}},
		{"rules-incomplete", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out.Rules.Findings = []onboarding.RulesetFinding{
				{Code: "gate_check", State: "warning", Text: "Require archfit/gate."},
			}
		}},
		{"approval-pending", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out.State, e.setups.out.Workflow, e.setups.out.OpenPull = "not_admitted", false, 203
			e.setups.out.Approval = gate.ApprovalSummary{Status: gate.ApprovalPending, Kind: gate.OwnerWorkflow, Pull: 203, CurrentHead: sha(3), ApprovedHead: sha(3)}
		}},
		{"approval-stale", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out.State, e.setups.out.Workflow, e.setups.out.OpenPull = "not_admitted", false, 203
			e.setups.out.Approval = gate.ApprovalSummary{Status: gate.ApprovalStale, Kind: gate.OwnerWorkflow, Pull: 203, CurrentHead: sha(4), ApprovedHead: sha(3)}
		}},
		{"approval-unknown", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out.Approval = gate.ApprovalSummary{Status: gate.ApprovalUnknown, Kind: gate.OwnerWorkflow, Pull: 203}
		}},
		{"approval-merged", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out.Approval = gate.ApprovalSummary{Status: gate.ApprovalPending, Kind: gate.OwnerWorkflow, Pull: 203, CurrentHead: sha(3), ApprovedHead: sha(3), Merged: true}
		}},
		{"not-set-up", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out = onboarding.Setup{Approval: gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: gate.OwnerFirst, Pull: 12}, State: "first_approval", FirstPull: 12, Missing: []string{"Register a passkey and approve the first policy."},
				Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{
					{Code: "pull_requests", State: "warning", Text: "Add a pull request rule to the default branch."},
					{Code: "gate_check", State: "warning", Text: "Add the App's archfit/gate check as a required status check."},
				}}}
			e.gh.fail["BranchHead"] = errors.New("Home must return the existing setup answer without loading the map")
		}},
		{"exception-seven-days", homeWithException("2026-11-17"), func(t *testing.T, e *env) {
			homeEditRepo(t, e, func(rec map[string]any) {
				rec["expiry_issues"] = map[string]any{"exception graph app other 2026-11-17": 74}
			})
		}},
		{"exception-last-day", homeWithException("2026-11-10"), nil},
		{"exception-expired", homeWithException("2026-11-09"), nil},
		{"expired", homeWithException("2026-11-09"), func(t *testing.T, e *env) {
			homeEditRepo(t, e, func(rec map[string]any) {
				rec["drift"] = map[string]any{"head": sha(10), "state": "expired", "errors": []any{}}
			})
		}},
		{"partial-evidence", homePolicy, func(t *testing.T, e *env) {
			homeEditRepo(t, e, func(rec map[string]any) {
				rec["drift"] = map[string]any{"head": sha(10), "state": "clean", "unread": true, "errors": []any{}}
			})
		}},
		{"no-evidence", homePolicy, func(t *testing.T, e *env) { homeEditRepo(t, e, func(rec map[string]any) { delete(rec, "drift") }) }},
		{"workflow-change", homePolicy, func(_ *testing.T, e *env) {
			e.gh.pulls[repoName+"#4"] = api.Pull{Number: 4, State: "open", HeadSHA: sha(4), HeadRepo: repoName, BaseRef: mainBranch}
			e.gh.setFile(repoName, sha(4), workflow, "the changed workflow")
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", homePolicy)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newHomeEnv(t, tt.policy)
			if tt.setup != nil {
				tt.setup(t, e)
			}
			if tt.name == "workflow-review" || tt.name == "workflow-change" || tt.name == "not-set-up" || tt.name == "approval-stale" {
				e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
					if tt.name == "not-set-up" && a.Kind == gate.OwnerFirst && a.Pull == 12 ||
						tt.name != "not-set-up" && a.Kind == gate.OwnerWorkflow && a.Pull > 0 {
						return nil
					}
					return gate.ErrOwnerNotDue
				}
			}
			response := e.as("alice", permAdmin).get(homeAPI)
			wantStatus(t, response, http.StatusOK)
			if words := wording.NotUsedIn(withoutOwnerReviewCopy(response.Body.String())); len(words) > 0 {
				t.Fatalf("Home uses %v:\n%s", words, response.Body.String())
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, response.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			path := filepath.Join("testdata", "home", tt.name+".json")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, pretty.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pretty.Bytes(), want) {
				t.Fatalf("Home differs from %s:\n%s", path, pretty.String())
			}
		})
	}
}

func TestHomeExceptionWindowUsesUTCDate(t *testing.T) {
	for _, tt := range []struct {
		date  string
		needs int
		days  int
	}{
		{"2026-11-18", 0, 8}, {"2026-11-17", 1, 7}, {"2026-11-11", 1, 1}, {"2026-11-10", 1, 0}, {"2026-11-09", 1, -1},
	} {
		t.Run(tt.date, func(t *testing.T) {
			e := newHomeEnv(t, homeWithException(tt.date))
			e.now = time.Date(2026, 11, 11, 1, 45, 0, 0, time.FixedZone("UTC+2", 2*60*60))
			var got homeJSON
			e.as("alice", permRead).getJSON(homeAPI, &got)
			if len(got.NeedsYou) != tt.needs {
				t.Fatalf("Needs you = %+v, want %d", got.NeedsYou, tt.needs)
			}
			var line homeLineJSON
			for _, v := range got.Map.Lines {
				if v.Kind == "exception" {
					line = v
				}
			}
			if line.DaysLeft == nil || *line.DaysLeft != tt.days {
				t.Fatalf("line = %+v, want %d days", line, tt.days)
			}
		})
	}
}

func TestHomeAPIRefusesUnauthorizedAndFailedReads(t *testing.T) {
	t.Run("signed out", func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		wantStatus(t, e.anonymous().get(homeAPI), http.StatusUnauthorized)
		if len(e.setups.repos) != 0 {
			t.Fatal("read setup while signed out")
		}
	})
	t.Run("no repository permission", func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		wantStatus(t, e.signIn("mallory").get(homeAPI), http.StatusNotFound)
		if len(e.setups.repos) != 0 {
			t.Fatal("read setup without repository permission")
		}
	})
	for _, method := range []string{"BranchHead", "File", "OpenPulls"} {
		t.Run(method, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			e.gh.fail[method] = errors.New("read failed")
			wantStatus(t, e.as("alice", permRead).get(homeAPI), http.StatusBadGateway)
		})
	}
	t.Run("corrupt repository record", func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		homePut(t, e, "repo", map[string]any{"schema": "wrong"})
		wantStatus(t, e.as("alice", permRead).get(homeAPI), http.StatusBadGateway)
	})
	t.Run("missing indexed request", func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		homeRequest(t, e, "open")
		if err := e.store.Delete(context.Background(), store.Tenant(installA), "v4/request/"+repoName+"/431"); err != nil {
			t.Fatal(err)
		}
		wantStatus(t, e.as("alice", permRead).get(homeAPI), http.StatusBadGateway)
	})
}

func TestHomeMapOnlyShowsDenyRulesThatAreTouched(t *testing.T) {
	for _, tt := range []struct {
		name         string
		requestState string
		errors       []string
		head         string
		want         bool
	}{
		{name: "untouched"},
		{name: "open request", requestState: "open", want: true},
		{name: "answered request", requestState: "answered"},
		{name: "stored current error", errors: []string{"no-app-to-other app other other/pkg"}, head: sha(4), want: true},
		{name: "error for another module", errors: []string{"no-app-to-other app other2 other/pkg"}, head: sha(4)},
		{name: "stale head", errors: []string{"no-app-to-other app other other/pkg"}, head: sha(3)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy+homeDeny)
			if tt.requestState != "" {
				homeRequest(t, e, tt.requestState)
			}
			e.gh.pulls[repoName+"#4"] = api.Pull{Number: 4, State: "open", HeadSHA: sha(4), HeadRepo: repoName, BaseRef: mainBranch}
			if tt.errors != nil {
				homePut(t, e, "pull/4", map[string]any{"schema": "archfit-app.pull.v1", "head_sha": tt.head, "errors": tt.errors})
			}
			var got homeJSON
			e.as("alice", permRead).getJSON(homeAPI, &got)
			actual := slices.ContainsFunc(got.Map.Lines, func(l homeLineJSON) bool { return l.Kind == "deny" })
			if actual != tt.want {
				t.Fatalf("lines = %+v, want deny %v", got.Map.Lines, tt.want)
			}
		})
	}
}

func TestHomeWorkflowActionsIgnoreForksOtherTargetsAndApprovedHeads(t *testing.T) {
	for _, tt := range []struct {
		name, repo, target string
		approved           bool
		want               int
	}{
		{"same repository", repoName, mainBranch, false, 1},
		{"fork", "fork/app", mainBranch, false, 0},
		{"other target", repoName, "release", false, 0},
		{"already approved", repoName, mainBranch, true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
				if a.Kind == gate.OwnerWorkflow {
					return nil
				}
				return gate.ErrOwnerNotDue
			}
			e.gh.pulls[repoName+"#4"] = api.Pull{Number: 4, State: "open", HeadSHA: sha(4), HeadRepo: tt.repo, BaseRef: tt.target}
			e.gh.setFile(repoName, sha(4), workflow, "the changed workflow")
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", homePolicy)
			if tt.approved {
				homePut(t, e, "proposal-index", map[string]any{"schema": "archfit-app.proposal-index.v1", "entries": []any{map[string]any{"key": "4", "id": "p1"}}})
				homePut(t, e, "proposal/p1", map[string]any{"schema": "archfit-app.proposal.v1", "kind": "workflow", "pull_request": 4, "head_sha": sha(4), "state": "open"})
			}
			var got homeJSON
			e.as("alice", permRead).getJSON(homeAPI, &got)
			actual := 0
			for _, need := range got.NeedsYou {
				if need.Kind == "workflow" {
					actual++
					if !strings.HasSuffix(need.Href, "?pull="+strconv.Itoa(4)) {
						t.Fatal(need)
					}
				}
			}
			if actual != tt.want {
				t.Fatalf("Needs you = %+v, want %d workflow actions", got.NeedsYou, tt.want)
			}
		})
	}
}

func TestHomeDoesNotCallOldEvidenceCleanOrMissDigestMismatch(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"old result", func(r map[string]any) { r["drift"].(map[string]any)["head"] = "old" }, homeStateUnknown},
		{"unjudged policy push", func(r map[string]any) { r["approved_digest"] = "sha256:other" }, homeStateOutsideFlow},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			homeEditRepo(t, e, tt.edit)
			var got homeJSON
			e.as("alice", permRead).getJSON(homeAPI, &got)
			if got.State != tt.want {
				t.Fatalf("state = %q (%q), want %q", got.State, got.Status, tt.want)
			}
		})
	}
}

func TestHomeOutsideFlowCanRestoreMissingOrInvalidPolicy(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(strconv.FormatBool(invalid), func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			homeEditRepo(t, e, func(rec map[string]any) { rec["outside_flow"] = map[string]any{"digest": "changed", "issue": 73} })
			e.setups.out.State = "outside_flow"
			if invalid {
				e.gh.setFile(repoName, mainBranch, ".archfit.yaml", "invalid yaml")
			} else {
				delete(e.gh.files, repoName+"@"+mainBranch+":.archfit.yaml")
			}
			var got homeJSON
			e.as("alice", permRead).getJSON(homeAPI, &got)
			if got.State != homeStateOutsideFlow || got.Map != nil {
				t.Fatalf("got %+v", got)
			}
			if !slices.ContainsFunc(got.NeedsYou, func(n homeNeedJSON) bool { return n.Kind == "revert" }) {
				t.Fatalf("no revert action: %+v", got.NeedsYou)
			}
			if slices.ContainsFunc(got.NeedsYou, func(n homeNeedJSON) bool { return n.Kind == "confirm" }) {
				t.Fatalf("invalid policy cannot be confirmed: %+v", got.NeedsYou)
			}
		})
	}
}

func TestHomeDenyPathSelectorUsesActualErrorModules(t *testing.T) {
	pol := homePolicy + "rules:\n  - id: no-app-to-other\n    deny: {from: 'cmd/**', to: other}\n    why: Keep this import out.\n    instead: Pass a value in.\n"
	e := newHomeEnv(t, pol)
	e.gh.pulls[repoName+"#4"] = api.Pull{Number: 4, State: "open", HeadSHA: sha(4), HeadRepo: repoName, BaseRef: mainBranch}
	homePut(t, e, "pull/4", map[string]any{"schema": "archfit-app.pull.v1", "head_sha": sha(4), "errors": []string{"no-app-to-other app other other/pkg"}})
	var got homeJSON
	e.as("alice", permRead).getJSON(homeAPI, &got)
	if !slices.ContainsFunc(got.Map.Lines, func(l homeLineJSON) bool { return l.Kind == "deny" && l.From == "app" && l.To == "other" }) {
		t.Fatalf("deny missing: %+v", got.Map.Lines)
	}
}

func TestHomeBodyChoosesOneStateAndSaysWhatWaits(t *testing.T) {
	known := gate.HomeState{DriftKnown: true}
	with := func(edit func(*gate.HomeState)) gate.HomeState {
		v := known
		edit(&v)
		return v
	}
	today := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		view       gate.HomeState
		state      string
		status     string
		needsCount int
	}{
		{"clean and nothing waits", known, homeStateClean, "The default branch follows the policy. Nothing needs you.", 0},
		{"drift names its issue", with(func(v *gate.HomeState) { v.Drift = []gate.HomeError{{}}; v.DriftIssue = 210 }), homeStateDrift,
			"The default branch has dependencies the policy does not allow. Issue #210.", 1},
		{"an unread file beats drift", with(func(v *gate.HomeState) { v.Drift = []gate.HomeError{{}}; v.DriftUnread = true }), homeStateUnknown,
			"The last check could not read every file, so the state is unknown.", 1},
		{"an unread file is unknown, never clean", with(func(v *gate.HomeState) { v.DriftUnread = true }), homeStateUnknown,
			"The last check could not read every file, so the state is unknown. Nothing needs you.", 0},
		{"no evidence is unknown", gate.HomeState{}, homeStateUnknown, "The state of the default branch is not known. Nothing needs you.", 0},
		{"a result for an older head is unknown, not expired", gate.HomeState{RepositoryState: "expired", Drift: []gate.HomeError{{}}}, homeStateUnknown,
			"The state of the default branch is not known. Nothing needs you.", 0},
		{"an open drift issue stands when the stored result is old", gate.HomeState{DriftIssue: 210}, homeStateUnknown,
			"The state of the default branch is not known.", 1},
		{"an unread result is unknown before expired", with(func(v *gate.HomeState) { v.RepositoryState = "expired"; v.DriftUnread = true }), homeStateUnknown,
			"The last check could not read every file, so the state is unknown. Nothing needs you.", 0},
		{"an exception past its date is a row, not the state, when the code no longer breaks it", with(func(v *gate.HomeState) {
			v.RepositoryState = "clean"
			v.Policy.Exceptions = []policy3.Exception{{Rule: "graph", From: "app", To: "other", Expires: "2026-11-01"}}
		}), homeStateClean, "The default branch follows the policy.", 1},
		{"expired beats drift", with(func(v *gate.HomeState) { v.RepositoryState = "expired"; v.Drift = []gate.HomeError{{}} }), homeStateExpired,
			"An exception passed its date and the code still breaks the rule, so every pull request fails.", 1},
		{"outside the flow beats expired", with(func(v *gate.HomeState) { v.OutsideFlow = true; v.RepositoryState = "expired" }), homeStateOutsideFlow,
			"The policy on the default branch changed without a recorded approval, so every pull request is unknown.", 1},
		{"waiting work is said after the state", with(func(v *gate.HomeState) {
			v.Requests = []gate.HomeRequest{{Number: 5}, {Number: 6}}
			v.WorkflowPulls = []int{4}
		}), homeStateClean, "The default branch follows the policy. 2 requests wait. 1 workflow change waits.", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := homeBody(tt.view, setupJSON{}, nil, repoName, today)
			if got.State != tt.state || got.Status != tt.status || len(got.NeedsYou) != tt.needsCount {
				t.Fatalf("state %q status %q needs %d; want %q %q %d", got.State, got.Status, len(got.NeedsYou), tt.state, tt.status, tt.needsCount)
			}
		})
	}
}

func TestHomeBodySaysNothingNeedsYouOnlyWhenNoSetupIsOpen(t *testing.T) {
	view := gate.HomeState{DriftKnown: true}
	today := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	open := &setupJSON{}
	if got := homeBody(view, setupJSON{}, open, repoName, today); strings.Contains(got.Status, "Nothing needs you") || got.Setup != open {
		t.Fatalf("setup is open, but Home says %q", got.Status)
	}
	if got := homeBody(view, setupJSON{}, nil, repoName, today); !strings.Contains(got.Status, "Nothing needs you.") {
		t.Fatalf("nothing is open, but Home says %q", got.Status)
	}
}
