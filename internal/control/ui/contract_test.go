package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

type route struct {
	method, path, min string
	form              url.Values
}

// repoRoutes is every repository route with its minimum permission and a form that succeeds.
func repoRoutes(fullName string) []route {
	base := "/r/" + fullName
	return []route{
		{http.MethodGet, base, permRead, nil},
		{http.MethodPost, base + "/onboarding", permAdmin, url.Values{"mode": {"customer"}, "owners": {"alice"}}},
		{http.MethodPost, base + "/discover", permAdmin, nil},
		{http.MethodGet, base + "/policy", permRead, nil},
		{http.MethodPost, base + "/policy", permWrite, formOf("", "", policyText+"# edit\n")},
		{http.MethodPost, base + "/policy/check", permRead, formOf("", "", policyText+"# edit\n")},
		{http.MethodPost, base + "/policy/ready", permWrite, nil}, // the form is filled in by the test: it needs an open draft
		{http.MethodGet, base + "/graph", permRead, nil},
		{http.MethodPost, base + "/assist", permWrite, url.Values{"mode": {"customer"}, "instruction": {"split module a"}, "content": {policyText}}},
		{http.MethodGet, base + "/consent", permAdmin, nil},
		{http.MethodPost, base + "/consent", permAdmin, url.Values{"kind": {"managed_llm"}, "action": {"record"}}},
	}
}

var permissionRank = map[string]int{"none": 0, permRead: 1, permWrite: 2, permAdmin: 3}

func (c *client) send(rt route) int {
	if rt.method == http.MethodGet {
		return c.get(rt.path).Code
	}
	form := url.Values{}
	for k, v := range rt.form {
		form[k] = v
	}
	return c.post(rt.path, form).Code
}

func TestRoutesAndMinimumPermissions(t *testing.T) {
	for _, rt := range repoRoutes(repoName) {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			e := newEnv(t)
			res := e.anonymous().do(requestOf(rt))
			if rt.method == http.MethodGet {
				wantStatus(t, res, http.StatusSeeOther)
				if got, want := res.Header().Get("Location"), "/auth/login?return="+url.QueryEscape(rt.path); got != want {
					t.Errorf("Location = %q, want %q", got, want)
				}
			} else {
				wantStatus(t, res, http.StatusUnauthorized)
			}

			for _, perm := range []string{"none", permRead, permWrite, permAdmin} {
				e := newEnv(t)
				c := e.as("alice", perm)
				if strings.HasSuffix(rt.path, "/ready") {
					d := e.props.push(repoName, "archfit/edit-alice", policyText+"# draft\n")
					rt.form = formOf("", d.HeadSHA, "")
				}
				got := c.send(rt)
				switch {
				case permissionRank[perm] < permissionRank[rt.min]:
					if got != http.StatusNotFound {
						t.Errorf("%s: status = %d, want 404", perm, got)
					}
				case rt.method == http.MethodGet, strings.HasSuffix(rt.path, "/policy/check"):
					if got != http.StatusOK {
						t.Errorf("%s: status = %d, want 200", perm, got)
					}
				default:
					if got != http.StatusSeeOther {
						t.Errorf("%s: status = %d, want 303", perm, got)
					}
				}
			}
		})
	}

	t.Run("overview", func(t *testing.T) {
		e := newEnv(t)
		res := e.anonymous().get("/")
		wantStatus(t, res, http.StatusOK)
		wantContains(t, res.Body.String(), `href="/auth/login"`)
		wantStatus(t, e.as("alice", permRead).get("/"), http.StatusOK)
	})
	t.Run("logout", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.anonymous().post("/auth/logout", nil), http.StatusUnauthorized)
		c := e.as("alice", "none")
		wantStatus(t, c.post("/auth/logout", nil), http.StatusSeeOther)
		wantContains(t, c.get("/").Body.String(), `href="/auth/login"`)
	})
	t.Run("public assets", func(t *testing.T) {
		e := newEnv(t)
		for _, path := range []string{"/static/app.css", "/static/editor.js", "/static/graph.js", "/schema/policy.json"} {
			res := e.anonymous().get(path)
			wantStatus(t, res, http.StatusOK)
			if res.Body.Len() == 0 {
				t.Errorf("%s is empty", path)
			}
		}
		wantStatus(t, e.anonymous().get("/static/"), http.StatusNotFound)
	})
}

func requestOf(rt route) *http.Request {
	req, _ := http.NewRequest(rt.method, baseURL+rt.path, strings.NewReader(rt.form.Encode()))
	if rt.method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func TestOverviewReadsStateOnDefaultBranchHead(t *testing.T) {
	e := newEnv(t)
	rows := []struct {
		name, want string
		runs       []api.CheckRun
		headFails  bool
	}{
		{"acme/a-healthy", "healthy", []api.CheckRun{{Name: checkState, Status: "completed", Conclusion: "success", Title: "No new findings"}}, false},
		{"acme/b-neutral", "attention", []api.CheckRun{{Name: checkState, Status: "completed", Conclusion: "neutral", Title: "Coverage is partial"}}, false},
		{"acme/c-none", stateNone, nil, false},
		{"acme/d-broken", stateUnknown, nil, true},
	}
	for i, row := range rows {
		e.addRepo(row.name, installA, int64(30+i))
		e.grant("alice", row.name, permRead)
		e.gh.checks[row.name+"@"+sha(30+i)+"#"+checkState] = row.runs
		e.gh.checks[row.name+"@"+sha(30+i)+"#"+checkGate] = []api.CheckRun{{Name: checkGate, Conclusion: "failure", Title: "GATE-MARKER"}}
		if row.headFails {
			e.gh.fail["BranchHead "+row.name] = errors.New("boom")
		}
	}
	res := e.signIn("alice").get("/")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()
	for _, row := range rows {
		cell := regexp.MustCompile(regexp.QuoteMeta(row.name) + `</a></td>\s*<td>` + regexp.QuoteMeta(row.want) + `</td>`)
		if !cell.MatchString(body) {
			t.Errorf("%s: row does not read %q", row.name, row.want)
		}
	}
	wantContains(t, body, "No new findings", "Coverage is partial")
	if strings.Contains(body, "GATE-MARKER") {
		t.Error("archfit/gate is shown on the overview")
	}
	if got := len(e.gh.called("BranchHead")); got != len(rows) {
		t.Errorf("BranchHead calls = %d, want %d", got, len(rows))
	}
	checks := e.gh.called("CheckRuns")
	if len(checks) != len(rows)-1 {
		t.Errorf("CheckRuns calls = %d, want %d", len(checks), len(rows)-1)
	}
	for _, c := range checks {
		if !strings.HasSuffix(c.detail, "#"+checkState) {
			t.Errorf("CheckRuns looked up %q", c.detail)
		}
	}
}

func TestSaveProposesBoundChange(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	draft := e.props.push(repoName, "archfit/edit-alice", policyText+"# draft\n")

	page := c.get("/r/" + repoName + "/policy").Body.String()
	wantContains(t, page, `name="head_sha" value="`+draft.HeadSHA+`"`, "# draft")

	content := policyText + "# draft\n# changed\n"
	res := c.post("/r/"+repoName+"/policy", formOf("archfit/edit-alice", draft.HeadSHA, content))
	wantStatus(t, res, http.StatusSeeOther)

	if len(e.props.proposes) != 1 {
		t.Fatalf("Propose calls = %d, want 1", len(e.props.proposes))
	}
	got := e.props.proposes[0]
	want := proposal.Change{
		Repo: api.RepoRef{InstallationID: installA, FullName: repoName}, Branch: "archfit/edit-alice", Base: mainBranch,
		Title: "Archfit policy edit by @alice", Body: editBody, Message: "archfit: edit policy (@alice)",
		Files: []api.FileChange{{Path: ".archfit.yaml", Content: []byte(content)}},
	}
	if !reflect.DeepEqual(got.change, want) {
		t.Errorf("Change = %+v\nwant %+v", got.change, want)
	}
	if got.expectHead != draft.HeadSHA {
		t.Errorf("expected head = %q, want %q", got.expectHead, draft.HeadSHA)
	}
	loc := res.Header().Get("Location")
	if !strings.HasPrefix(loc, "/r/"+repoName+"/policy") {
		t.Fatalf("Location = %q", loc)
	}
	wantContains(t, c.get(loc).Body.String(), `<strong class="draft-state">draft</strong>`, "# changed")
}

func TestFirstSaveOpensOneDraftPR(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	content := policyText + "# first\n"

	wantStatus(t, c.post("/r/"+repoName+"/policy", formOf("", "", content)), http.StatusSeeOther)
	retry := c.post("/r/"+repoName+"/policy", formOf("", "", content))
	wantStatus(t, retry, http.StatusSeeOther)
	wantContains(t, c.get(retry.Header().Get("Location")).Body.String(), notices["unchanged"])

	if len(e.props.drafts) != 1 || e.props.commits != 1 || len(e.props.proposes) != 1 {
		t.Errorf("drafts = %d, commits = %d, Propose calls = %d; want 1 each", len(e.props.drafts), e.props.commits, len(e.props.proposes))
	}

	different := c.post("/r/"+repoName+"/policy", formOf("", "", content+"# other\n"))
	wantStatus(t, different, http.StatusConflict)
	if e.props.commits != 1 {
		t.Errorf("commits = %d after the refused retry", e.props.commits)
	}
}

func TestGraphPageBuildsFromArtifact(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	payload := artifact(t, "real-blocked-no-baseline")
	payload = bytes.ReplaceAll(payload, []byte(`"from_glob":"pkg/a/**"`), []byte(`"from_glob":"MARKER-matched-by/**"`))
	payload = bytes.ReplaceAll(payload, []byte(`"go/packages":"go1.27.1"`), []byte(`"go/packages":"MARKER-measurement"`))
	if bytes.Count(payload, []byte("MARKER-")) < 2 {
		t.Fatal("markers were not placed in the fixture")
	}
	rep, err := report.Decode(payload, "v2.3.1")
	if err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	e.publishReport(repoName, payload)

	res := c.get("/r/" + repoName + "/graph")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()

	var got graph.View
	if err := json.Unmarshal([]byte(embeddedJSON(t, body)), &got); err != nil {
		t.Fatalf("embedded JSON: %v", err)
	}
	want := graph.Build(rep, []byte(policyText))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("embedded view = %+v\nwant %+v", got, want)
	}
	if len(want.Nodes) == 0 || len(want.Links) == 0 || want.Stale {
		t.Fatalf("fixture gives a useless view: %+v", want)
	}
	for _, n := range want.Nodes {
		wantContains(t, body, fmt.Sprintf(`href="/r/%s/policy#L%d"`, repoName, n.PolicyLine))
	}
	wantContains(t, body, "violation")
	if strings.Contains(body, `id="stale"`) {
		t.Error("stale banner on a fresh report")
	}
	// hard_gates: fail. An observed link of this report is not proof that the policy permits it.
	if !want.Incomplete || !strings.Contains(body, `id="incomplete"`) {
		t.Errorf("Incomplete = %v and no incomplete banner for a report whose hard gates did not pass", want.Incomplete)
	}
	if strings.Contains(body, "MARKER-") {
		t.Error("a field the decoder drops reached the page")
	}
	for _, call := range e.gh.called("Artifact") {
		if call.detail != fmt.Sprintf("77 archfit-report %d", report.MaxBytes) {
			t.Errorf("Artifact(%s)", call.detail)
		}
	}
}

var graphData = regexp.MustCompile(`(?s)<script type="application/json" id="graph-data">(.*?)</script>`)

func embeddedJSON(t *testing.T, body string) string {
	t.Helper()
	m := graphData.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no embedded graph data in: %.300s", body)
	}
	return m[1]
}

func TestConsentRecordAndRevoke(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	path := "/r/" + repoName + "/consent"

	wantStatus(t, c.post(path, url.Values{"kind": {"managed_llm"}, "action": {"record"}}), http.StatusSeeOther)
	if !e.consents.Has(t.Context(), installA, account.ConsentManagedLLM) {
		t.Fatal("consent not recorded for the installation of the authorized repository")
	}
	got := e.consents.recorded[installA][account.ConsentManagedLLM]
	if got.By != "alice" || !got.At.Equal(e.now) || got.Kind != account.ConsentManagedLLM {
		t.Errorf("consent = %+v", got)
	}
	wantContains(t, c.get(path).Body.String(), "<strong>recorded</strong>", "whole GitHub App installation")

	wantStatus(t, c.post(path, url.Values{"kind": {"managed_llm"}, "action": {"revoke"}}), http.StatusSeeOther)
	if e.consents.Has(t.Context(), installA, account.ConsentManagedLLM) {
		t.Error("consent still recorded after revoke")
	}
	for _, form := range []url.Values{{"kind": {"everything"}, "action": {"record"}}, {"kind": {"managed_llm"}, "action": {"sell"}}} {
		wantStatus(t, c.post(path, form), http.StatusUnprocessableEntity)
	}
}

func TestOnboardingAndDiscoverActions(t *testing.T) {
	e := newEnv(t)
	e.onb.progress = onboarding.Progress{Stage: onboarding.StageInstalled, Diagnostic: "Start onboarding."}
	c := e.as("alice", permAdmin)
	base := "/r/" + repoName

	res := c.post(base+"/onboarding", url.Values{"mode": {"customer"}, "owners": {"alice\r\n\r\n  acme/platform-team \n"}})
	wantStatus(t, res, http.StatusSeeOther)
	if len(e.onb.customer) != 1 || !reflect.DeepEqual(e.onb.customer[0].owners, []string{"alice", "acme/platform-team"}) {
		t.Errorf("StartCustomer calls = %+v", e.onb.customer)
	}
	if e.onb.customer[0].repo.InstallationID != installA {
		t.Errorf("StartCustomer repo = %+v", e.onb.customer[0].repo)
	}

	res = c.post(base+"/onboarding", url.Values{"mode": {"managed"}, "owners": {"alice"}})
	wantStatus(t, res, http.StatusSeeOther)
	if got := res.Header().Get("Location"); got != base+"/consent" {
		t.Errorf("Location = %q, want the consent page", got)
	}
	if len(e.onb.managed) != 0 {
		t.Error("StartManaged was called without consent")
	}
	_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentSourceProcessing})
	wantStatus(t, c.post(base+"/onboarding", url.Values{"mode": {"managed"}, "owners": {"alice"}}), http.StatusSeeOther)
	if len(e.onb.managed) != 1 {
		t.Errorf("StartManaged calls = %d, want 1", len(e.onb.managed))
	}

	wantStatus(t, c.post(base+"/discover", nil), http.StatusConflict)
	if len(e.gh.called("Dispatch")) != 0 {
		t.Fatal("discovery was dispatched before workflow_merged")
	}
	e.onb.progress.Stage = onboarding.StageWorkflowMerged
	wantStatus(t, c.post(base+"/discover", nil), http.StatusSeeOther)
	calls := e.gh.called("Dispatch")
	if len(calls) != 1 || calls[0].detail != workflow+"@"+mainBranch+" map[discover:true]" || calls[0].installationID != installA {
		t.Errorf("Dispatch calls = %+v", calls)
	}
}
