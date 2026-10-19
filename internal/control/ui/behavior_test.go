package ui

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
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
		var b graphJSON
		c.getJSON("/api/repos/"+repoName+"/graph", &b)
		if b.View == nil {
			t.Fatalf("no view: %+v", b)
		}
		return *b.View, b.StaleReason
	}

	if got, _ := view(); got.Incomplete {
		t.Error("a healthy report whose hard gates passed is shown as incomplete")
	}

	edited := policyText + "  c:\n    layer: edge\n"
	wantStatus(t, c.post(base+"/policy", formOf("", "", edited)), http.StatusSeeOther)
	got, reason := view()
	if want := graph.Build(rep, []byte(edited)); !got.Stale || !reflect.DeepEqual(got, want) || len(got.Nodes) != 3 || reason != "" {
		t.Errorf("view = %+v (%q), want the stale view of the draft %+v", got, reason, want)
	}

	e.props.push(repoName, editBranchAlice, "modules:\n\tbroken")
	got, reason = view()
	want := graph.Build(rep, []byte(policyText))
	want.Stale = true
	if !reflect.DeepEqual(got, want) || reason != draftBroken {
		t.Errorf("view = %+v (%q), want the default-branch view %+v", got, reason, want)
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
	if calls := e.gh.called("Artifact"); len(calls) != 1 || calls[0].detail != fmt.Sprintf("77 archfit-report %d", report.MaxBytes) {
		t.Errorf("Artifact calls = %+v, want run 77 only", calls)
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
		"pkg_chaos -> pkg_container": {graph.StatusViolation, report.PairExisting},
		"pkg_container -> pkg_chaos": {graph.StatusViolation, report.PairAdded},
		"pkg_chaos -> pkg_util":      {graph.StatusViolation, report.PairExisting},
		"pkg_util -> pkg_chaos":      {graph.StatusViolation, report.PairAdded},
		"pkg_cmd -> pkg_chaos":       {graph.StatusObserved, report.PairExisting},
		"pkg_cmd -> pkg_container":   {graph.StatusObserved, report.PairExisting},
		"pkg_cmd -> pkg_util":        {graph.StatusObserved, report.PairExisting},
		"pkg_container -> pkg_util":  {graph.StatusObserved, report.PairExisting},
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links = %+v\nwant %+v", links, want)
	}
	if b.Added != 2 {
		t.Errorf("added = %d, want the two cycle directions the comment calls added", b.Added)
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
		{"repository page", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/r/" + repoName + "/pull/4") },
			http.StatusSeeOther, "/auth/login?return=" + url.QueryEscape("/r/"+repoName+"/pull/4")},
		{"repository page with a query", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder { return c.get("/r/" + repoName + "/policy?x=1") },
			http.StatusSeeOther, "/auth/login?return=" + url.QueryEscape("/r/"+repoName+"/policy?x=1")},
		{"form post", func(e *env) { e.gh.fail["Permission"] = rejected },
			func(c *client) *httptest.ResponseRecorder {
				return c.post("/r/"+repoName+"/policy", formOf("", "", policyText))
			},
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
		for name, key := range map[string]string{"repository page": "Permission", "overview": "UserRepos"} {
			e := newEnv(t)
			c := e.as("alice", permRead)
			e.gh.fail[key] = errors.New("boom")
			path := "/api/repos"
			if key == "Permission" {
				path = "/r/" + repoName
			}

			res := c.get(path)

			wantStatus(t, res, http.StatusBadGateway)
			if len(res.Result().Cookies()) != 0 {
				t.Errorf("%s: a 502 changed cookies: %q", name, res.Header().Values("Set-Cookie"))
			}
		}
	})
}
