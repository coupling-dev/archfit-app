package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const editBranchAlice = "archfit/edit-alice"

func (e *env) draft() proposal.Draft { return *e.props.drafts[repoName+"@"+editBranchAlice] }

func (e *env) onBranch() string {
	e.gh.mu.Lock()
	defer e.gh.mu.Unlock()
	return string(e.gh.files[repoName+"@"+e.gh.heads[repoName+"@"+editBranchAlice]+":.archfit.yaml"])
}

// allNotFound sends every repository route and wants 404 from each.
func allNotFound(t *testing.T, e *env, c *client) {
	t.Helper()
	for _, rt := range repoRoutes(repoName) {
		if got := c.send(rt); got != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, got)
		}
	}
	e.noWrites(t)
}

func TestRevokedUserLosesEveryPage(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	for _, path := range []string{"", "/policy", "/graph"} {
		wantStatus(t, c.get("/r/"+repoName+path), http.StatusOK)
	}
	wantContains(t, c.get("/").Body.String(), repoName)

	e.grant("alice", repoName, "none")
	e.now = e.now.Add(61 * time.Second)

	allNotFound(t, e, c)
	if strings.Contains(c.get("/").Body.String(), repoName) {
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

func TestStaleDraftCannotBePublishedUnnoticed(t *testing.T) {
	base := "/r/" + repoName
	refused := func(t *testing.T, e *env, c *client, path string, form url.Values) {
		t.Helper()
		before := e.props.commits
		res := c.post(base+path, form)
		wantStatus(t, res, http.StatusConflict)
		wantContains(t, res.Body.String(), "# tab two", `href="/r/`+repoName+`/policy"`, "Reload the draft")
		if e.props.commits != before || len(e.props.readied) != 0 {
			t.Errorf("%s wrote: commits %d → %d, readied %v", path, before, e.props.commits, e.props.readied)
		}
	}

	t.Run("both tabs loaded the same draft head", func(t *testing.T) {
		e := newEnv(t)
		c := e.as("alice", permWrite)
		a := e.props.push(repoName, editBranchAlice, policyText+"# draft\n").HeadSHA

		wantStatus(t, c.post(base+"/policy", formOf("", a, policyText+"# tab one\n")), http.StatusSeeOther)
		if e.draft().HeadSHA == a {
			t.Fatal("tab one did not move the head")
		}
		refused(t, e, c, "/policy", formOf("", a, policyText+"# tab two\n"))
		refused(t, e, c, "/policy/ready", formOf("", a, policyText+"# tab two\n"))
		refused(t, e, c, "/assist", func() url.Values { f := assistForm(policyText + "# tab two\n"); f.Set("head_sha", a); return f }())
		if got := e.onBranch(); got != policyText+"# tab one\n" {
			t.Errorf("branch holds %q", got)
		}
		if e.draft().State != proposal.DraftOpen || len(e.assist.calls) != 0 {
			t.Errorf("draft state = %q, assist calls = %d", e.draft().State, len(e.assist.calls))
		}
	})

	t.Run("tab two loaded the default branch before any draft existed", func(t *testing.T) {
		e := newEnv(t)
		c := e.as("alice", permWrite)
		wantStatus(t, c.post(base+"/policy", formOf("", "", policyText+"# tab one\n")), http.StatusSeeOther)
		refused(t, e, c, "/policy", formOf("", "", policyText+"# tab two\n"))
		refused(t, e, c, "/policy/ready", formOf("", "", policyText+"# tab two\n"))
		if got := e.onBranch(); got != policyText+"# tab one\n" {
			t.Errorf("branch holds %q", got)
		}
	})
}

func TestInvalidYAMLIsRefusedAndGraphGoesStale(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	base := "/r/" + repoName
	payload := artifact(t, "healthy")
	rep, err := report.Decode(payload, "v3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	e.publishReport(repoName, payload)

	res := c.post(base+"/policy", formOf("", "", "modules:\n  a: [unclosed\n"))
	wantStatus(t, res, http.StatusUnprocessableEntity)
	wantContains(t, res.Body.String(), "a: [unclosed")
	if e.props.writes() != 0 {
		t.Fatal("broken YAML was committed")
	}

	view := func() (graph.View, string) {
		t.Helper()
		res := c.get(base + "/graph")
		wantStatus(t, res, http.StatusOK)
		var v graph.View
		if err := json.Unmarshal([]byte(embeddedJSON(t, res.Body.String())), &v); err != nil {
			t.Fatal(err)
		}
		return v, res.Body.String()
	}

	if got, body := view(); got.Incomplete || strings.Contains(body, `id="incomplete"`) {
		t.Error("a healthy report whose hard gates passed is shown as incomplete")
	}

	edited := policyText + "  c:\n    layer: edge\n"
	wantStatus(t, c.post(base+"/policy", formOf("", "", edited)), http.StatusSeeOther)
	got, body := view()
	if want := graph.Build(rep, []byte(edited)); !got.Stale || !reflect.DeepEqual(got, want) || len(got.Nodes) != 3 {
		t.Errorf("view = %+v, want the stale view of the draft %+v", got, want)
	}
	wantContains(t, body, `id="stale"`)

	e.props.push(repoName, editBranchAlice, "modules:\n\tbroken")
	got, body = view()
	want := graph.Build(rep, []byte(policyText))
	want.Stale = true
	if !reflect.DeepEqual(got, want) {
		t.Errorf("view = %+v, want the default-branch view %+v", got, want)
	}
	wantContains(t, body, `id="stale"`, "The draft does not parse")
}

func TestGraphWithoutAReport(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	res := c.get("/r/" + repoName + "/graph")
	wantStatus(t, res, http.StatusOK)
	wantContains(t, res.Body.String(), "No report available")

	e.publishReport(repoName, []byte(`{"schema_version":"archfit.architecture-state.v1","surprise":1}`))
	res = c.get("/r/" + repoName + "/graph")
	wantStatus(t, res, http.StatusOK)
	wantContains(t, res.Body.String(), "Report unavailable")
	if strings.Contains(res.Body.String(), "graph-data") {
		t.Error("a partial graph was rendered")
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

	res := c.get("/r/" + repoName + "/graph")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()
	if strings.Contains(body, "No report available") || !strings.Contains(body, "graph-data") {
		t.Errorf("no graph from the report of run 77 behind newer dispatch, failed and running runs: %.300s", body)
	}
	if calls := e.gh.called("Artifact"); len(calls) != 1 || calls[0].detail != fmt.Sprintf("77 archfit-report %d", report.MaxBytes) {
		t.Errorf("Artifact calls = %+v, want run 77 only", calls)
	}
}

// TestPullGraphShowsWhatThePullRequestAdded reads the graph of a pumba-pilot#11-shaped report as the owner does: the
// two cycles are new in this PR and stay violations, the accepted coupling advisories are pre-existing, and the
// links the pull request did not touch are drawn without a "new" claim.
func TestPullGraphShowsWhatThePullRequestAdded(t *testing.T) {
	e := newEnv(t)
	e.checkPull(prArtifact(t, prPolicy))
	e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy)
	body := e.as("alice", permRead).get("/r/" + repoName + "/pull/4/graph").Body.String()
	var v graph.View
	if err := json.Unmarshal([]byte(embeddedJSON(t, body)), &v); err != nil {
		t.Fatalf("embedded JSON: %v", err)
	}

	type got struct {
		status graph.LinkStatus
		new    bool
	}
	links := map[string]got{}
	for _, l := range v.Links {
		links[l.From+" -> "+l.To] = got{l.Status, l.Introduced}
	}
	want := map[string]got{
		"pkg_chaos -> pkg_container": {graph.StatusViolation, true},
		"pkg_container -> pkg_chaos": {graph.StatusViolation, true},
		"pkg_chaos -> pkg_util":      {graph.StatusViolation, true},
		"pkg_util -> pkg_chaos":      {graph.StatusViolation, true},
		"pkg_cmd -> pkg_chaos":       {graph.StatusObserved, false},
		"pkg_cmd -> pkg_container":   {graph.StatusObserved, false},
		"pkg_cmd -> pkg_util":        {graph.StatusObserved, false},
		"pkg_container -> pkg_util":  {graph.StatusObserved, false},
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links = %+v\nwant %+v", links, want)
	}

	origins := map[string][]string{}
	for _, f := range v.Findings {
		k := f.From + " -> " + f.To
		origins[k] = append(origins[k], f.RuleID+" "+f.Status+" "+f.Origin)
	}
	for k, w := range map[string][]string{
		"pkg_chaos -> pkg_container": {"no-module-cycles new introduced", "bc/imbalanced_coupling baseline pre_existing"},
		"pkg_container -> pkg_chaos": {"no-module-cycles new introduced", "bc/imbalanced_coupling new introduced"},
		"pkg_cmd -> pkg_chaos":       {"bc/imbalanced_coupling baseline pre_existing"},
	} {
		if !reflect.DeepEqual(origins[k], w) {
			t.Errorf("findings of %s = %q, want %q", k, origins[k], w)
		}
	}
	wantContains(t, body, "4 links have a finding the base branch does not have")
}

func TestPullGraphStates(t *testing.T) {
	payload := func(t *testing.T) []byte { return prArtifact(t, prPolicy) }
	tests := []struct {
		name      string
		setup     func(t *testing.T, e *env)
		wants     []string
		notWanted []string
	}{
		{"no check on the head", func(*testing.T, *env) {}, []string{noResult, `href="/r/` + repoName + `/pull/4"`}, []string{"graph-data"}},
		{"a withdrawn result shows no graph", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
			e.gh.checks[repoName+"@"+sha(4)+"#"+checkState][0].Conclusion = conclusionActionRequired
		}, []string{actionRequired}, []string{"graph-data"}},
		{"an expired report", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
			delete(e.gh.artifacts, 88)
		}, []string{reportExpired}, []string{"graph-data"}},
		{"a run that did not compare claims nothing new", func(t *testing.T, e *env) {
			e.checkPull(bytes.Replace(payload(t), []byte(`"origin_status":"comparable"`), []byte(`"origin_status":"unknown"`), 1))
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy)
		}, []string{`id="not-compared"`, "This run did not compare with the base branch, so the graph cannot show what this PR added.", "graph-data"},
			[]string{`"Introduced":true`, `"Origin":"introduced"`, `id="new-count"`}},
		{"a policy unchanged by the pull request comes from the base", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy+"# edited\n")
			e.gh.setFile(repoName, mainBranch, ".archfit.yaml", prPolicy)
		}, []string{`data-policy-url="https://github.com/` + repoName + `/blob/main/.archfit.yaml"`, `"Introduced":true`}, []string{`id="stale"`}},
		{"no policy of the report anywhere is stale", func(t *testing.T, e *env) {
			e.checkPull(payload(t))
		}, []string{`id="stale"`, noPolicy, `data-policy-url="https://github.com/` + repoName + `/blob/main/.archfit.yaml"`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.setup(t, e)
			res := e.as("alice", permRead).get("/r/" + repoName + "/pull/4/graph")
			wantStatus(t, res, http.StatusOK)
			body := html.UnescapeString(res.Body.String())
			wantContains(t, body, tt.wants...)
			for _, s := range tt.notWanted {
				if strings.Contains(body, s) {
					t.Errorf("body contains %q", s)
				}
			}
		})
	}
}

// TestDefaultGraphClaimsNothingNew: "new in this PR" belongs to the pull request graph, even when a default-branch
// report carries a comparable origin.
func TestDefaultGraphClaimsNothingNew(t *testing.T) {
	e := newEnv(t)
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", prPolicy)
	e.publishReport(repoName, prArtifact(t, prPolicy))
	body := e.as("alice", permRead).get("/r/" + repoName + "/graph").Body.String()
	wantContains(t, body, "graph-data", "pkg_chaos")
	for _, s := range []string{"new in this PR", `data-scope="pull"`, `id="not-compared"`, `id="new-count"`} {
		if strings.Contains(body, s) {
			t.Errorf("default-branch graph contains %q", s)
		}
	}
}

var actionPattern = regexp.MustCompile(`(?i)(form)?action="[^"]*(merge|approv|publish)`)

func TestUIAndGitHubFirstEditsShareOnePath(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	base := "/r/" + repoName

	wantStatus(t, c.post(base+"/policy", formOf("", "", policyText+"# from the editor\n")), http.StatusSeeOther)
	loaded := e.draft().HeadSHA
	pushed := e.props.push(repoName, editBranchAlice, policyText+"# from the editor\n# pushed on GitHub\n")

	if len(e.props.drafts) != 1 || e.props.proposes[0].change.Base != mainBranch || pushed.Number != e.draft().Number {
		t.Errorf("the two edits are not commits on one pull request against %s", mainBranch)
	}
	page := c.get(base + "/policy").Body.String()
	wantContains(t, page, `name="head_sha" value="`+pushed.HeadSHA+`"`, "# pushed on GitHub", pushed.URL)

	res := c.post(base+"/policy", formOf("", loaded, policyText+"# older load\n"))
	wantStatus(t, res, http.StatusConflict)
	if e.props.commits != 2 {
		t.Errorf("commits = %d, want 2", e.props.commits)
	}

	for _, path := range []string{base, base + "/policy", base + "/graph", base + "/consent"} {
		if m := actionPattern.FindString(c.get(path).Body.String()); m != "" {
			t.Errorf("%s offers %q", path, m)
		}
	}
	for _, path := range []string{"/policy/merge", "/policy/approve", "/policy/publish"} {
		if got := c.post(base+path, nil).Code; got != http.StatusNotFound && got != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d", path, got)
		}
	}
}

func TestUnknownYAMLConstructsSurviveASave(t *testing.T) {
	const exotic = "# Policy of acme/app — keep this comment.\n" +
		"version: 2\n\n" +
		"modules:\n  a: &defaults\n    layer: core   # trailing comment\n    paths: ['pkg/a/**']\n" +
		"  b:\n    <<: *defaults\n    paths: [\"pkg/b/**\"]\n  c: {layer: edge, paths: [\"pkg/c/**\"]}\n" +
		"exclude:\n  - !!str 123\n  - |\n    block text\n\n"
	tests := []struct {
		name, onMain, submitted, committed string
	}{
		{"bytes as submitted", exotic, exotic + "# one more line\n", exotic + "# one more line\n"},
		{"a browser's CRLF does not rewrite an LF file", exotic, strings.ReplaceAll(exotic+"# x\n", "\n", "\r\n"), exotic + "# x\n"},
		{"a CRLF file stays CRLF", strings.ReplaceAll(exotic, "\n", "\r\n"), strings.ReplaceAll(exotic+"# x\n", "\n", "\r\n"), strings.ReplaceAll(exotic+"# x\n", "\n", "\r\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.gh.setFile(repoName, mainBranch, ".archfit.yaml", tt.onMain)
			c := e.as("alice", permWrite)
			wantStatus(t, c.post("/r/"+repoName+"/policy", formOf("", "", tt.submitted)), http.StatusSeeOther)
			if got := e.onBranch(); got != tt.committed {
				t.Errorf("committed %q\nwant      %q", got, tt.committed)
			}
		})
	}
}

// managedAssist stands in for assist in managed mode: one provider call per Propose, the answer committed to the draft.
type managedAssist struct {
	e             *env
	providerCalls int
}

const providerAnswer = "PROVIDER-ANSWER: here is the policy you asked for"

func (m *managedAssist) propose(_ assist.Mode, r assist.Request) (assist.Result, error) {
	if m.e.draft().HeadSHA != r.HeadSHA {
		return assist.Result{}, proposal.ErrStale
	}
	m.providerCalls++
	_ = providerAnswer // the provider's text never crosses the Assist interface
	d := m.e.props.push(r.Repo.FullName, r.Branch, policyText+"  proposed:\n    layer: edge\n")
	return assist.Result{Mode: assist.ModeManaged, Draft: d}, nil
}

func TestAssistIsExplicitAndReviewable(t *testing.T) {
	e := newEnv(t)
	m := &managedAssist{e: e}
	e.assist.propose = m.propose
	c := e.as("alice", permWrite)
	base := "/r/" + repoName
	e.publishReport(repoName, artifact(t, "healthy"))

	wantStatus(t, c.post(base+"/policy", formOf("", "", policyText+"# saved\n")), http.StatusSeeOther)
	for _, path := range []string{"/", base, base + "/policy", base + "/graph"} {
		wantStatus(t, c.get(path), http.StatusOK)
	}
	if m.providerCalls != 0 || len(e.assist.calls) != 0 {
		t.Fatal("assist ran without an explicit request")
	}

	form := assistForm(policyText + "# saved\n# and edited\n")
	form.Set("mode", "managed")
	form.Set("head_sha", e.draft().HeadSHA)
	res := c.post(base+"/assist", form)
	wantStatus(t, res, http.StatusSeeOther)

	if m.providerCalls != 1 || len(e.assist.calls) != 1 {
		t.Fatalf("provider calls = %d, Propose calls = %d; want 1 and 1", m.providerCalls, len(e.assist.calls))
	}
	call := e.assist.calls[0]
	saved := e.props.proposes[len(e.props.proposes)-1]
	if string(saved.change.Files[0].Content) != policyText+"# saved\n# and edited\n" {
		t.Error("the text in the editor was not saved before the request")
	}
	wantReq := assist.Request{Repo: e.gh.repos[repoName], Branch: editBranchAlice, HeadSHA: sha(0xc0000 + 2), Instruction: "split module a", By: "alice"}
	if call.mode != assist.ModeManaged || !reflect.DeepEqual(call.req, wantReq) {
		t.Errorf("Propose(%q, %+v)\nwant %+v", call.mode, call.req, wantReq)
	}
	if e.props.commits != 3 || len(e.props.drafts) != 1 || e.draft().State != proposal.DraftOpen || len(e.props.readied) != 0 {
		t.Errorf("commits = %d, drafts = %d, state = %q; want a third commit on the same open draft", e.props.commits, len(e.props.drafts), e.draft().State)
	}
	after := c.get(res.Header().Get("Location"))
	wantContains(t, after.Body.String(), notices["proposed"], e.draft().URL)
	for _, body := range []string{res.Body.String(), after.Body.String()} {
		if strings.Contains(body, "PROVIDER-ANSWER") {
			t.Error("provider text in a response body")
		}
	}
}

func TestAssistFailureLeavesEditingWorking(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"provider failing", assist.ErrProvider, http.StatusServiceUnavailable},
		{"budget exhausted", account.ErrBudgetExhausted, http.StatusServiceUnavailable},
		{"managed unavailable", assist.ErrManagedUnavailable, http.StatusServiceUnavailable},
		{"stale head", proposal.ErrStale, http.StatusConflict},
	}
	sentences := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.assist.propose = failingAssist(tt.err)
			c := e.as("alice", permWrite)
			base := "/r/" + repoName
			head := e.props.push(repoName, editBranchAlice, policyText+"# draft\n").HeadSHA

			form := assistForm(policyText + "# draft\n")
			form.Set("mode", "managed")
			form.Set("head_sha", head)
			res := c.post(base+"/assist", form)
			wantStatus(t, res, tt.status)
			_, _, sentence := explain(tt.err)
			wantContains(t, res.Body.String(), sentence, "# draft", "<textarea")
			sentences[sentence] = true

			wantStatus(t, c.post(base+"/policy", formOf("", head, policyText+"# draft\n# still editing\n")), http.StatusSeeOther)
			wantStatus(t, c.get("/schema/policy.json"), http.StatusOK)
		})
	}
	if len(sentences) != len(tests) {
		t.Errorf("distinct sentences = %d, want %d", len(sentences), len(tests))
	}
}

var credentialField = regexp.MustCompile(`(?i)type="password"|name="[^"]*(token|secret|password|api_?key|credential)`)

func TestCustomerAssistShowsPendingFailedCompleted(t *testing.T) {
	e := newEnv(t)
	e.assist.propose = func(mode assist.Mode, _ assist.Request) (assist.Result, error) {
		return assist.Result{Mode: mode, RunURL: "https://github.com/" + repoName + "/actions/workflows/archfit-assist.yaml"}, nil
	}
	c := e.as("alice", permWrite)
	base := "/r/" + repoName
	head := e.props.push(repoName, editBranchAlice, policyText+"# draft\n").HeadSHA
	runKey := repoName + "@" + assist.AssistWorkflowPath + "@" + editBranchAlice
	setRun := func(run api.WorkflowRun) {
		e.gh.mu.Lock()
		defer e.gh.mu.Unlock()
		e.gh.runs[runKey] = []api.WorkflowRun{run}
	}

	if m := credentialField.FindString(c.get(base + "/policy").Body.String()); m != "" {
		t.Errorf("the form has a credential field: %q", m)
	}
	form := assistForm(policyText + "# draft\n")
	form.Set("head_sha", head)
	res := c.post(base+"/assist", form)
	wantStatus(t, res, http.StatusSeeOther)
	if e.assist.calls[0].mode != assist.ModeCustomer {
		t.Fatalf("mode = %q", e.assist.calls[0].mode)
	}
	editor := res.Header().Get("Location")
	state := func() string {
		t.Helper()
		page := c.get(editor)
		wantStatus(t, page, http.StatusOK)
		m := regexp.MustCompile(`AI proposal in your CI: <strong>([^<]+)</strong>`).FindStringSubmatch(page.Body.String())
		if m == nil {
			t.Fatal("no run state on the editor")
		}
		wantContains(t, page.Body.String(), `href="https://github.com/`+repoName+`/actions/workflows/archfit-assist.yaml"`)
		return m[1]
	}

	if got := state(); got != runPending {
		t.Errorf("before the run is visible: %q", got)
	}
	setRun(api.WorkflowRun{ID: 1, Status: "queued"})
	if got := state(); got != runPending {
		t.Errorf("queued: %q", got)
	}
	setRun(api.WorkflowRun{ID: 1, Status: "completed", Conclusion: "failure"})
	if got := state(); got != runFailed {
		t.Errorf("failed run: %q", got)
	}
	setRun(api.WorkflowRun{ID: 2, Status: "completed", Conclusion: "success"})
	e.props.push(repoName, editBranchAlice, policyText+"# draft\n# proposed in CI\n")
	if got := state(); got != runCompleted {
		t.Errorf("succeeded run with a new commit: %q", got)
	}
	wantContains(t, c.get(editor).Body.String(), "Reload the draft", "# proposed in CI")

	if e.consents.writes != 0 || len(e.acct.installations) != 0 {
		t.Error("customer-mode assist touched stored state")
	}
}

func TestCheckListsProblemsAndSavesNothing(t *testing.T) {
	const broken = "version: 2\nmodlues: {}\nrules:\n  - id: r\n    type: cycle\n    gate: fial\n"
	tests := []struct {
		name, content string
		want          []string
		clean         bool
	}{
		{"unknown key and enum value", broken, []string{
			"The check found problems",
			`<li><a href="#L2">Line 2</a>, column 1: Unknown key ` + "`modlues`. Did you mean `modules`?</li>",
			`<li><a href="#L6">Line 6</a>, column 11: ` + "`gate` must be one of `off`, `warn`, `fail`. Did you mean `fail`?</li>",
		}, false},
		{"a browser's CRLF keeps the line numbers", strings.ReplaceAll(broken, "\n", "\r\n"), []string{
			`<a href="#L2">Line 2</a>`, `<a href="#L6">Line 6</a>`,
		}, false},
		{"syntax error", "version: 2\nmodules:\n\ta: {}\n", []string{
			`<li><a href="#L3">Line 3</a>: YAML syntax: found character that cannot start any token. The problem is at line 3 or below it.</li>`,
		}, false},
		{"a problem without a line", "# nothing yet\n", []string{"<li>Missing required key `version`.</li>"}, false},
		{"a hostile key is escaped", "version: 2\n\"<img src=x onerror=alert(1)>\": 1\n", []string{
			"Unknown key `&lt;img src=x onerror=alert(1)&gt;`.",
		}, false},
		{"no problems", policyText, []string{"The check found no problems."}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permRead)
			files, gets := len(e.gh.called("File")), e.props.gets

			res := c.post("/r/"+repoName+"/policy/check", formOf("", "", tt.content))

			wantStatus(t, res, http.StatusOK)
			body := res.Body.String()
			wantContains(t, body, tt.want...)
			wantContains(t, body, "Full checks run in CI with <code>archfit config lint</code>.", html.EscapeString(tt.content))
			if strings.Contains(body, "<img src=x") {
				t.Error("the page carries an unescaped key of the text")
			}
			if got := strings.Contains(body, `id="diagnostics"`); got == tt.clean {
				t.Errorf("problem list shown = %v, want %v", got, !tt.clean)
			}
			e.noWrites(t)
			if len(e.gh.called("File")) != files || e.props.gets != gets {
				t.Error("Check asked GitHub for the policy or the draft")
			}
		})
	}
}

func TestRefusedSaveListsTheProblems(t *testing.T) {
	const broken = "version: 2\nrules:\n  - id: r\n    gate: warn\n    form: a/**\n"
	for _, route := range []struct {
		path string
		form url.Values
	}{{"/policy", formOf("", "", broken)}, {"/assist", assistForm(broken)}} {
		t.Run(route.path, func(t *testing.T) {
			e := newEnv(t)
			res := e.as("alice", permWrite).post("/r/"+repoName+route.path, route.form)
			wantStatus(t, res, http.StatusUnprocessableEntity)
			wantContains(t, res.Body.String(), policyProblemsSentence, html.EscapeString(broken),
				`<li><a href="#L3">Line 3</a>, column 5: Missing required key `+"`type`.</li>",
				`<li><a href="#L5">Line 5</a>, column 5: Unknown key `+"`form`. Did you mean `from`?</li>",
				`<script src="/static/editor.js" defer></script>`)
			e.noWrites(t)
			if len(e.assist.calls) != 0 {
				t.Error("assist was called with a text that has problems")
			}
		})
	}
}

func TestEditorKeepsALeadingBlankLine(t *testing.T) {
	const text = "\n# top\n" + policyText
	e := newEnv(t)
	e.gh.setFile(repoName, mainBranch, ".archfit.yaml", text)
	c := e.as("alice", permWrite)

	// A browser drops the one newline that follows <textarea>, so the page must carry one more.
	page := c.get("/r/" + repoName + "/policy").Body.String()
	wantContains(t, page, `spellcheck="false">`+"\n\n# top\n")
	checked := c.post("/r/"+repoName+"/policy/check", formOf("", "", text)).Body.String()
	wantContains(t, checked, `data-unsaved>`+"\n\n# top\n")

	wantStatus(t, c.post("/r/"+repoName+"/policy", formOf("", "", text+"# edit\n")), http.StatusSeeOther)
	if got := e.onBranch(); got != text+"# edit\n" {
		t.Errorf("committed %q", got)
	}
}

func TestOnlyAFormRerenderIsMarkedUnsaved(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	if page := c.get("/r/" + repoName + "/policy").Body.String(); strings.Contains(page, "data-unsaved") {
		t.Error("the text loaded from the branch is marked unsaved")
	}
	for path, form := range map[string]url.Values{
		"/policy/check": formOf("", "", policyText),
		"/policy":       formOf("", "", "version: 3\n"),
	} {
		wantContains(t, c.post("/r/"+repoName+path, form).Body.String(), "data-unsaved")
	}
}

// Owner decision: AI may be asked to fix the broken draft already on the branch; a changed broken text is refused.
func TestAssistOnABrokenDraft(t *testing.T) {
	const broken = "version: 2\nrules:\n  - id: r\n    gate: warn\n"
	tests := []struct {
		name, submitted string
		wantStatus      int
		wantCalls       int
	}{
		{"the text on the branch, CRLF from the browser", strings.ReplaceAll(broken, "\n", "\r\n"), http.StatusSeeOther, 1},
		{"a changed text with problems", broken + "# edited\n", http.StatusUnprocessableEntity, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			d := e.props.push(repoName, editBranchAlice, broken)
			writes := e.props.writes()
			form := assistForm(tt.submitted)
			form.Set("head_sha", d.HeadSHA)

			res := e.as("alice", permWrite).post("/r/"+repoName+"/assist", form)

			wantStatus(t, res, tt.wantStatus)
			if len(e.assist.calls) != tt.wantCalls {
				t.Errorf("assist calls = %d, want %d", len(e.assist.calls), tt.wantCalls)
			}
			if tt.wantCalls == 1 && e.assist.calls[0].req.HeadSHA != d.HeadSHA {
				t.Errorf("assist bound to %q, want the draft head %q", e.assist.calls[0].req.HeadSHA, d.HeadSHA)
			}
			if e.props.writes() != writes {
				t.Error("ui committed a text with problems")
			}
		})
	}
}
