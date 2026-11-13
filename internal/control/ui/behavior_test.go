package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// allNotFound sends every repository route and wants 404 from each.
func allNotFound(t *testing.T, e *env, c *client) {
	t.Helper()
	for _, rt := range repoRoutes(repoName) {
		if !rt.authorized() {
			continue
		}
		if got := c.send(rt); got != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, got)
		}
	}
	e.noWrites(t)
}

func TestRevokedUserLosesEveryPage(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	for _, path := range []string{"", "/graph"} {
		wantStatus(t, c.get("/r/"+repoName+path), http.StatusOK)
	}
	wantContains(t, c.get("/api/repos").Body.String(), repoName)

	e.grant("alice", repoName, "none")
	e.now = e.now.Add(61 * time.Second)

	allNotFound(t, e, c)
	if strings.Contains(c.get("/api/repos").Body.String(), repoName) {
		t.Error("the overview still lists the repository")
	}
}

func TestRemovedRepositoryDisappears(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	wantStatus(t, c.get("/r/"+repoName), http.StatusOK)

	e.gh.mu.Lock()
	delete(e.gh.repos, repoName) // removed from the installation; alice is still its administrator
	e.gh.mu.Unlock()
	e.now = e.now.Add(61 * time.Second)

	allNotFound(t, e, c)
}

func TestRepositoryGraphReadsThePolicyOnTheDefaultBranch(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	payload := artifact(t, "healthy")
	rep, err := report.Decode(payload, "v3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	e.publishReport(repoName, payload)
	edited := policyText + "  c:\n    layer: edge\n"
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", edited)

	var b graphJSON
	c.getJSON("/api/repos/"+repoName+"/graph", &b)
	if b.View == nil {
		t.Fatalf("no view: %+v", b)
	}
	if want := graph.Build(rep, []byte(edited)); !reflect.DeepEqual(*b.View, want) || len(b.View.Nodes) != 3 {
		t.Errorf("view = %+v, want the view of the default branch's policy %+v", *b.View, want)
	}
	if b.View.Incomplete {
		t.Error("a healthy report whose hard gates passed is shown as incomplete")
	}
	if want := policyBlob(repoName, mainBranch); b.PolicyHref != want {
		t.Errorf("policy_href = %q, want %q", b.PolicyHref, want)
	}
}

func TestGraphWithoutAReport(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	path := "/api/repos/" + repoName + "/graph"
	var b graphJSON
	c.getJSON(path, &b)
	if b.Unavailable != noReport || b.View != nil {
		t.Errorf("body = %+v, want %q and no view", b, noReport)
	}

	e.publishReport(repoName, []byte(`{"schema_version":"archfit.architecture-state.v1","surprise":1}`))
	b = graphJSON{}
	c.getJSON(path, &b)
	if b.Unavailable != reportUnavailable || b.View != nil {
		t.Errorf("body = %+v, want %q and no partial graph", b, reportUnavailable)
	}
}

func TestGraphReadsTheLatestSuccessfulPushRun(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	e.publishReport(repoName, artifact(t, "healthy"))
	for _, run := range []api.WorkflowRun{
		{ID: 78, Event: "push", Status: "completed", Conclusion: "failure"},
		{ID: 79, Event: "workflow_dispatch", Status: "completed", Conclusion: "success"},
		{ID: 80, Event: "push", Status: "in_progress"},
	} {
		e.gh.addRun(repoName, workflow, mainBranch, run)
	}

	var b graphJSON
	c.getJSON("/api/repos/"+repoName+"/graph", &b)
	if b.Unavailable != "" || b.View == nil {
		t.Errorf("no graph from the report of run 77 behind newer dispatch, failed and running runs: %+v", b)
	}
	if calls := e.gh.called("ArtifactID"); len(calls) != 1 || calls[0].detail != "77 archfit-report" {
		t.Errorf("ArtifactID calls = %+v, want run 77 only", calls)
	}
	if calls := e.gh.called("ArtifactByID"); len(calls) != 1 || calls[0].detail != fmt.Sprintf("77 %d", report.MaxBytes) {
		t.Errorf("ArtifactByID calls = %+v, want run 77's artifact under the report cap", calls)
	}
}

// TestPullGraphShowsWhatThePullRequestAdded reads the graph of a pumba-pilot#11-shaped report as the owner does: the
// two cycles stay violations, and a dependency is new only where the comment says this pull request added it: one
// direction of each cycle. The other directions and the untouched dependencies carry a pre-existing finding.
func TestPullGraphShowsWhatThePullRequestAdded(t *testing.T) {
	e := newEnv(t)
	e.checkPull(prArtifact(t, prPolicy))
	e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy)
	var b graphJSON
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4/graph", &b)
	if b.View == nil {
		t.Fatalf("no view: %+v", b)
	}
	v := *b.View

	type got struct {
		status graph.LinkStatus
		origin string
	}
	links := map[string]got{}
	for _, l := range v.Links {
		links[l.From+" -> "+l.To] = got{l.Status, l.Origin}
	}
	want := map[string]got{
		"pkg_chaos -> pkg_container": {graph.StatusViolation, report.PairAdded},
		"pkg_container -> pkg_chaos": {graph.StatusViolation, report.PairAdded},
		"pkg_chaos -> pkg_util":      {graph.StatusViolation, report.PairAdded},
		"pkg_util -> pkg_chaos":      {graph.StatusViolation, report.PairAdded},
		// the engine's "observed" pairs that a baselined advisory names: accepted, as the comment counts them
		"pkg_cmd -> pkg_chaos":      {graph.StatusAccepted, report.PairExisting},
		"pkg_cmd -> pkg_container":  {graph.StatusAccepted, report.PairExisting},
		"pkg_cmd -> pkg_util":       {graph.StatusAccepted, report.PairExisting},
		"pkg_container -> pkg_util": {graph.StatusAccepted, report.PairExisting},
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links = %+v\nwant %+v", links, want)
	}
	if b.Added != 4 {
		t.Errorf("introduced-finding pairs = %d, want four including mixed pairs", b.Added)
	}

	origins := map[string][]string{}
	for _, f := range v.Findings {
		k := f.From + " -> " + f.To
		origins[k] = append(origins[k], f.RuleID+" "+f.Status)
	}
	for k, w := range map[string][]string{
		"pkg_chaos -> pkg_container": {"no-module-cycles new", "bc/imbalanced_coupling baseline"},
		"pkg_container -> pkg_chaos": {"no-module-cycles new", "bc/imbalanced_coupling new"},
		"pkg_cmd -> pkg_chaos":       {"bc/imbalanced_coupling baseline"},
	} {
		if !reflect.DeepEqual(origins[k], w) {
			t.Errorf("findings of %s = %q, want %q", k, origins[k], w)
		}
	}
}

func TestPullGraphStates(t *testing.T) {
	payload := func(t *testing.T) []byte { return prArtifact(t, prPolicy) }
	blob := "https://github.com/" + repoName + "/blob/main/.archfit.yaml"
	tests := []struct {
		name  string
		setup func(t *testing.T, e *env)
		check func(t *testing.T, b graphJSON)
	}{
		{"no check on the head", func(*testing.T, *env) {}, func(t *testing.T, b graphJSON) {
			if b.Unavailable != noResult || b.View != nil || b.Pull == nil || b.Pull.Number != 4 {
				t.Errorf("body = %+v", b)
			}
		}},
		{"a withdrawn result shows no graph", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
			e.gh.checks[repoName+"@"+sha(4)+"#"+checkState][0].Conclusion = conclusionActionRequired
		}, func(t *testing.T, b graphJSON) {
			if b.Unavailable != actionRequired || b.View != nil {
				t.Errorf("body = %+v", b)
			}
		}},
		{"an expired report", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
			delete(e.gh.artifacts, 88)
		}, func(t *testing.T, b graphJSON) {
			if b.Unavailable != reportExpired || b.View != nil {
				t.Errorf("body = %+v", b)
			}
		}},
		{"a run that did not compare claims nothing new", func(t *testing.T, e *env) {
			e.checkPull(bytes.Replace(payload(t), []byte(`"origin_status":"comparable"`), []byte(`"origin_status":"unknown"`), 1))
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy)
		}, func(t *testing.T, b graphJSON) {
			if b.View == nil || b.Pull.Compared || b.Added != 0 {
				t.Fatalf("body = %+v", b)
			}
			for _, l := range b.View.Links {
				if l.Origin != "" {
					t.Errorf("%s -> %s origin %q without a comparison", l.From, l.To, l.Origin)
				}
			}
		}},
		{"a policy unchanged by the pull request comes from the base", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy+"# edited\n")
			e.gh.setFile(repoName, mainBranch, ".archfit.yaml", prPolicy)
		}, func(t *testing.T, b graphJSON) {
			if b.PolicyHref != blob || b.View == nil || b.View.Stale || b.Added == 0 {
				t.Errorf("body = %+v", b)
			}
		}},
		{"no policy of the report anywhere is stale", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
		}, func(t *testing.T, b graphJSON) {
			if b.PolicyHref != blob || b.View == nil || !b.View.Stale || b.StaleReason != noPolicy {
				t.Errorf("body = %+v", b)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.setup(t, e)
			var b graphJSON
			e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4/graph", &b)
			tt.check(t, b)
		})
	}
}

// TestDefaultGraphClaimsNothingNew: "added by this PR" belongs to the pull request pages, even when a default-branch
// report carries a comparable origin. The overview's findings, from the same report, claim nothing either.
func TestDefaultGraphClaimsNothingNew(t *testing.T) {
	e := newEnv(t)
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", prPolicy)
	e.publishReport(repoName, prArtifact(t, prPolicy))
	var b graphJSON
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/graph", &b)
	if b.View == nil || b.Pull != nil || b.Added != 0 {
		t.Fatalf("body = %+v", b)
	}
	for _, l := range b.View.Links {
		if l.Origin != "" {
			t.Errorf("%s -> %s origin %q on the default branch", l.From, l.To, l.Origin)
		}
	}
	var f repoFindingsJSON
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/findings", &f)
	if f.Unavailable != "" || f.Branch != mainBranch || f.ViolationCount == 0 || len(f.Cycles) == 0 {
		t.Fatalf("findings = %+v", f)
	}
	for _, c := range f.Cycles {
		for _, d := range c.Directions {
			if d.Origin != "" {
				t.Errorf("cycle direction %s -> %s origin %q on the default branch", d.From, d.To, d.Origin)
			}
		}
	}
	for _, x := range slices.Concat(f.Other, f.Advisories, f.Accepted) {
		if x.Origin != "" {
			t.Errorf("finding %s origin %q on the default branch", x.RuleID, x.Origin)
		}
	}
}

// Incident 2026-10-18: a session cookie outlived its 8 h GitHub user token, GitHub answered 401, and every page
// was a 502 until the cookie expired. A rejected token now ends the session and asks for a new sign-in.
func TestExpiredGitHubTokenSignsOutInsteadOfBadGateway(t *testing.T) {
	rejected := fmt.Errorf("github: GET /repos/{r}: %w", api.ErrUserTokenRejected)
	sessionDeleted := func(res *httptest.ResponseRecorder) bool {
		for _, ck := range res.Result().Cookies() {
			if ck.Name == "archfit_session" {
				return ck.MaxAge < 0 && ck.Value == ""
			}
		}
		return false
	}
	tests := []struct {
		name     string
		inject   func(*env)
		send     func(*client) *httptest.ResponseRecorder
		want     int
		location string
	}{
		{"pull request", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/api/repos/" + repoName + "/pulls/4") },
			http.StatusUnauthorized, ""},
		{"overview", func(e *env) { e.gh.fail["UserRepos"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/api/repos") },
			http.StatusUnauthorized, ""},
		{"repository overview", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/api/repos/" + repoName + "/overview") },
			http.StatusUnauthorized, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			e := newEnv(t)
			c := e.as("alice", permRead)
			tt.inject(e)

			res := tt.send(c)

			wantStatus(t, res, tt.want)
			if got := res.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
			if !sessionDeleted(res) {
				t.Errorf("Set-Cookie = %q, want the session cookie deleted", res.Header().Values("Set-Cookie"))
			}
			if !strings.Contains(logs.String(), `"outcome":"signed_out"`) {
				t.Errorf("ui request log has no signed_out outcome:\n%s", logs.String())
			}

			// The browser dropped the cookie: the next request is a plain signed-out one.
			delete(e.gh.fail, "Permission")
			delete(e.gh.fail, "UserRepos")
			if next := c.get("/r/" + repoName); next.Code != http.StatusSeeOther {
				t.Errorf("request after the sign-out = %d, want 303 to sign in", next.Code)
			}
		})
	}

	t.Run("other GitHub failures stay a bad gateway and keep the session", func(t *testing.T) {
		for name, key := range map[string]string{"repository overview": "Permission", "overview": "UserRepos"} {
			e := newEnv(t)
			c := e.as("alice", permRead)
			e.gh.fail[key] = errors.New("boom")
			path := "/api/repos"
			if key == "Permission" {
				path = "/api/repos/" + repoName + "/overview"
			}

			res := c.get(path)

			wantStatus(t, res, http.StatusBadGateway)
			if len(res.Result().Cookies()) != 0 {
				t.Errorf("%s: a 502 changed cookies: %q", name, res.Header().Values("Set-Cookie"))
			}
		}
	})
}

// The request line carries the GitHub call count and time (api.WithCallStats counts them; api's tests check the
// counting): one line per request, numbers only, never a URL.
func TestRequestLineCountsGitHubCalls(t *testing.T) {
	logs := captureLogs(t)
	e := newEnv(t)
	c := e.as("alice", permRead)
	wantStatus(t, c.get("/api/repos/"+repoName+"/overview"), http.StatusOK)

	var lines []map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil && m["msg"] == "ui request" && m["route"] == "GET /api/repos/{o}/{n}/overview" {
			lines = append(lines, m)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("ui request lines = %v, want one", lines)
	}
	for _, key := range []string{"gh_calls", "gh_ms"} {
		if _, ok := lines[0][key].(float64); !ok {
			t.Errorf("%s = %v, want a number", key, lines[0][key])
		}
	}
	if strings.Contains(logs.String(), "https://") {
		t.Errorf("the request line names a URL: %s", logs.String())
	}
}
