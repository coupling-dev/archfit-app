package ui

import (
	"encoding/json"
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
	rep, err := report.Decode(payload)
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
		"metrics:\n  x-unknown-to-the-app:\n    - !!binary aGVsbG8=\n    - |\n      block text\n\n"
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
		e.gh.runs[runKey] = run
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
