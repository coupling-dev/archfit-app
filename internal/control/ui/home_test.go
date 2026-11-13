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

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
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
	e.now = time.Date(2026, 11, 10, 23, 45, 0, 0, time.UTC)
	e.setups.out = onboarding.Setup{Admitted: true, State: "approved", Missing: []string{}}
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
	e.handler = newHandler(Deps{Account: e.acct, Consents: e.consents,
		Admissions: e.admit, HomeViews: gate.NewHome(e.gh, e.store, e.admit), Setups: e.setups, Now: clock,
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
		{"not-set-up", homePolicy, func(_ *testing.T, e *env) {
			e.setups.out = onboarding.Setup{State: "first_approval", FirstPull: 12, Missing: []string{"Register a passkey and approve the first policy."},
				Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{
					{Code: "pull_requests", State: "warning", Text: "Require a pull request before merging into the default branch."},
					{Code: "gate_check", State: "warning", Text: "Require archfit/gate with this App's integration_id (42)."},
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
			response := e.as("alice", permRead).get(homeAPI)
			wantStatus(t, response, http.StatusOK)
			if words := wording.NotUsedIn(response.Body.String()); len(words) > 0 {
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
		{"old result", func(r map[string]any) { r["drift"].(map[string]any)["head"] = "old" }, "repository state is not known"},
		{"unjudged policy push", func(r map[string]any) { r["approved_digest"] = "sha256:other" }, "policy changed outside the flow"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			homeEditRepo(t, e, tt.edit)
			var got homeJSON
			e.as("alice", permRead).getJSON(homeAPI, &got)
			if !strings.Contains(got.Status, tt.want) {
				t.Fatalf("status = %q, want %q", got.Status, tt.want)
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
			if !strings.Contains(got.Status, "policy changed outside the flow") || got.Map != nil {
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
