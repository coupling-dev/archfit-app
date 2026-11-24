package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/control/onboarding"
	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/wire/wording"
)

func TestReposGoldens(t *testing.T) {
	tests := []struct {
		name   string
		record map[string]any
		listed bool
	}{
		{"clean", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "clean"}}, true},
		{"drift", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "drift"}}, true},
		{"expired", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "expired"}}, true},
		{"outside-flow", map[string]any{"approved_digest": "approved", "outside_flow": map[string]any{"digest": "other"}, "drift": map[string]any{"head": "stored-head", "state": "expired"}}, true},
		{"not-set-up", nil, true},
		{"no-approval", map[string]any{"outside_flow": map[string]any{"digest": "other"}, "drift": map[string]any{"head": "stored-head", "state": "clean"}}, true},
		{"missing-evidence", map[string]any{"approved_digest": "approved"}, true},
		{"partial-evidence", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "clean", "unread": true}}, true},
		{"no-repositories", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.record != nil {
				tt.record["schema"] = "archfit-app.repo.v1"
				homePut(t, e, "repo", tt.record)
			}
			if tt.record["approved_digest"] != nil {
				e.setups.out = onboarding.Setup{Policy: true, Workflow: true, State: "approved"}
			}
			if tt.record["outside_flow"] != nil {
				e.setups.out.State = "outside_flow"
			}
			if tt.listed {
				e.grant("alice", repoName, permRead)
			}
			response := e.signIn("alice").get("/api/repos?probe=1")
			wantStatus(t, response, http.StatusOK)
			checkReposGolden(t, tt.name, response)
			for _, method := range []string{"BranchHead", "CheckRuns", "File", "OpenPulls"} {
				if calls := e.gh.called(method); len(calls) != 0 {
					t.Errorf("repositories read %s: %v", method, calls)
				}
			}
		})
	}
}

func TestReposSortAndTruncateBeforeReadingState(t *testing.T) {
	e := newEnv(t)
	for i := maxOverviewRepos; i >= 0; i-- {
		name := fmt.Sprintf("acme/repo-%03d", i)
		e.addRepo(name, installA, int64(100+i))
		e.grant("alice", name, permRead)
	}
	if err := e.store.Put(t.Context(), 1, "v4/repo/acme/repo-100", []byte("unreadable")); err != nil {
		t.Fatal(err)
	}
	response := e.signIn("alice").get("/api/repos")
	wantStatus(t, response, http.StatusOK)
	body := decodeAs[overviewBody](t, response)
	if !body.Truncated || len(body.Repos) != maxOverviewRepos {
		t.Fatalf("repositories = %+v", body)
	}
	for i, row := range body.Repos {
		if want := fmt.Sprintf("acme/repo-%03d", i); row.FullName != want {
			t.Errorf("repository %d = %s, want %s", i, row.FullName, want)
		}
	}
}

func TestReposUnreadableRecordIsAnError(t *testing.T) {
	e := newEnv(t)
	homePut(t, e, "repo", map[string]any{"schema": "unreadable"})
	wantStatus(t, e.as("alice", permRead).get("/api/repos"), http.StatusBadGateway)
}

func checkReposGolden(t *testing.T, name string, response *httptest.ResponseRecorder) {
	t.Helper()
	// P0.2 explicitly names these navigation actions; retain the wording check for all other copy.
	copy := strings.NewReplacer("Review first policy", "", "Review workflow change", "").Replace(response.Body.String())
	if words := wording.NotUsedIn(copy); len(words) != 0 {
		t.Fatalf("repositories use %v: %s", words, response.Body.String())
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, response.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", "repos", name+".json")
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
		t.Errorf("repositories differ from %s:\ngot %s\nwant %s", path, pretty.Bytes(), want)
	}
}

func TestReposAllStatesGolden(t *testing.T) {
	e := strangerEnv(t)
	for _, state := range []string{"clean", "drift", "expired", "outside-flow", "not-set-up", "unknown"} {
		name := "acme/" + state
		e.addRepo(name, installA, 50)
		e.grant("alice", name, permRead)
		rec := map[string]any{"schema": "archfit-app.repo.v1", "approved_digest": "approved"}
		switch state {
		case "outside-flow":
			rec["outside_flow"] = map[string]any{"digest": "other"}
		case "not-set-up":
			delete(rec, "approved_digest")
		case "unknown":
		default:
			rec["drift"] = map[string]any{"head": "stored-head", "state": state}
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.Put(t.Context(), 1, "v4/repo/"+name, raw); err != nil {
			t.Fatal(err)
		}
	}
	e.setups.byRepo = map[string]onboarding.Setup{
		"acme/clean":        {Policy: true, Workflow: true, State: "approved", Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{{Code: "gate_check", State: "ok"}, {Code: "pull_requests", State: "ok"}}}},
		"acme/drift":        {State: "first_approval", FirstPull: 8, Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{{Code: "gate_check", State: "warning"}, {Code: "pull_requests", State: "ok"}}}},
		"acme/expired":      {Policy: true, Workflow: true, State: "approved"},
		"acme/outside-flow": {Policy: true, Workflow: true, State: "outside_flow"},
		"acme/unknown":      {Policy: true, OpenPull: 203, State: "first_approval"},
	}
	e.addRepo("octo/tool", installC, 51)
	e.grant("alice", "octo/tool", permRead)
	// Unadmitted data must never be read, even when it would fail decoding.
	if err := e.store.Put(t.Context(), 3, "v4/repo/octo/tool", []byte("unreadable")); err != nil {
		t.Fatal(err)
	}
	res := e.signIn("alice").get("/api/repos?probe=1")
	wantStatus(t, res, http.StatusOK)
	checkReposGolden(t, "all-states", res)
	if len(e.setups.repos) != 6 {
		t.Fatalf("Setup calls = %d, want all six admitted rows only", len(e.setups.repos))
	}
	// The list without the probe is the page's first paint: the same rows, none checked yet.
	fast := e.signIn("alice").get("/api/repos")
	wantStatus(t, fast, http.StatusOK)
	checkReposGolden(t, "all-states-unprobed", fast)
	if len(e.setups.repos) != 6 {
		t.Fatalf("the list without the probe made %d more Setup calls", len(e.setups.repos)-6)
	}
}

func TestReposOnboardingStagesAreBoundedAndErrorsFallBack(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("acme/stage-%02d", i)
		e.addRepo(name, installA, int64(200+i))
		e.grant("alice", name, permRead)
	}
	e.setups.byRepo = map[string]onboarding.Setup{
		"acme/stage-00": {Policy: false},
		"acme/stage-01": {Policy: true, Workflow: false},
		"acme/stage-02": {Policy: true, Workflow: true, Admitted: true, State: "first_approval"},
		"acme/stage-03": {Policy: true, Workflow: true, Admitted: true, State: "approved"},
	}
	e.setups.errors = map[string]error{"acme/stage-04": errors.New("GitHub unavailable")}
	res := e.signIn("alice").get("/api/repos?probe=1")
	wantStatus(t, res, http.StatusOK)
	body := decodeAs[overviewBody](t, res)
	want := []string{"not_started", "not_started", "first_approval", "policy_approved", ""}
	for i, state := range want {
		if body.Repos[i].SetupStage != state {
			t.Errorf("%s stage = %q, want %q", body.Repos[i].FullName, body.Repos[i].SetupStage, state)
		}
	}
	for _, i := range []int{4, 25, 29} {
		row := body.Repos[i]
		if row.SetupStage != "" || row.Enforcement != "check" || row.EnforcementReason == "" || row.State != "not set up" {
			t.Errorf("unread setup %d = %+v", i, row)
		}
	}
	if len(e.setups.repos) != maxSetupProbes {
		t.Fatalf("Setup calls = %d, want cap %d", len(e.setups.repos), maxSetupProbes)
	}
	sort.Slice(e.setups.repos, func(i, j int) bool { return e.setups.repos[i].FullName < e.setups.repos[j].FullName })
	for i, repo := range e.setups.repos {
		if want := fmt.Sprintf("acme/stage-%02d", i); repo.FullName != want {
			t.Errorf("Setup probe %d = %s, want %s", i, repo.FullName, want)
		}
	}
}

// The list without ?probe=1 answers from the user's repositories and stored state: no Setup probe and no GitHub read of
// a repository. Its rows say the check has not run; the probed list fills the same rows in.
func TestTheListWithoutProbeReadsNoSetupAndFillsInWithTheProbe(t *testing.T) {
	e := strangerEnv(t)
	e.grant("alice", repoName, permRead)
	e.grant("alice", strangerRepo, permRead)
	homePut(t, e, "repo", map[string]any{"schema": "archfit-app.repo.v1", "approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "clean"}})
	e.setups.out = onboarding.Setup{
		Policy: true, Workflow: true, State: "approved",
		Rules: onboarding.RulesetReport{Findings: []onboarding.RulesetFinding{{Code: "gate_check", State: "ok"}, {Code: "pull_requests", State: "ok"}}},
	}
	alice := e.signIn("alice")

	fast := decodeAs[overviewBody](t, alice.get("/api/repos"))
	if len(e.setups.repos) != 0 {
		t.Fatalf("the list probed %d rows before it was asked to", len(e.setups.repos))
	}
	for _, method := range []string{"BranchHead", "CheckRuns", "File", "OpenPulls"} {
		if calls := e.gh.called(method); len(calls) != 0 {
			t.Errorf("the list read %s: %v", method, calls)
		}
	}
	rows := map[string]overviewRow{}
	for _, row := range fast.Repos {
		rows[row.FullName] = row
	}
	app := rows[repoName]
	if app.State != "clean" || app.Enforcement != "check" || app.EnforcementReason != "Required-check settings were not read." || app.SetupStage != "" || app.NextStep != (overviewNextStep{}) {
		t.Errorf("unprobed row = %+v", app)
	}
	if stranger := rows[strangerRepo]; !stranger.Waiting || stranger.State != stateNotAdmitted {
		t.Errorf("waiting row = %+v", stranger)
	}

	probed := decodeAs[overviewBody](t, alice.get("/api/repos?probe=1"))
	if len(probed.Repos) != len(fast.Repos) {
		t.Fatalf("probed list has %d rows, fast list %d", len(probed.Repos), len(fast.Repos))
	}
	for i, row := range probed.Repos {
		if row.FullName != fast.Repos[i].FullName || row.State != fast.Repos[i].State || row.Waiting != fast.Repos[i].Waiting {
			t.Errorf("row %d = %+v; the fast list has %+v", i, row, fast.Repos[i])
		}
	}
	if got := rowNamed(probed, repoName); got.Enforcement != "required" || got.SetupStage != "policy_approved" {
		t.Errorf("probed row = %+v", got)
	}
}

func rowNamed(b overviewBody, name string) overviewRow {
	for _, row := range b.Repos {
		if row.FullName == name {
			return row
		}
	}
	return overviewRow{}
}

// Before the first approval the stored state says not set up; the probe tells a repository whose advisory checks run
// apart from one whose checks give no verdict yet, as Home does.
func TestReposRowSaysAdvisoryWhenChecksRunBeforeTheFirstApproval(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup onboarding.Setup
		want  string
	}{
		{"setup merged, advisory", homeAfterSetupMerge("advisory"), repoStateAdvisory},
		{"setup merged, required", homeAfterSetupMerge("required"), gate.RepoNotSetUp},
		{"setup pull request open", onboarding.Setup{Mode: "unknown", State: "first_approval", FirstPull: 12, SetupPull: 12}, gate.RepoNotSetUp},
		{"nothing on the default branch", onboarding.Setup{Mode: "unknown", State: "not_admitted"}, gate.RepoNotSetUp},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.grant("alice", repoName, permRead)
			e.setups.out = tt.setup
			alice := e.signIn("alice")
			if got := rowNamed(decodeAs[overviewBody](t, alice.get("/api/repos")), repoName).State; got != gate.RepoNotSetUp {
				t.Errorf("unprobed state = %q, want %q", got, gate.RepoNotSetUp)
			}
			if got := rowNamed(decodeAs[overviewBody](t, alice.get("/api/repos?probe=1")), repoName).State; got != tt.want {
				t.Errorf("probed state = %q, want %q", got, tt.want)
			}
		})
	}
}
