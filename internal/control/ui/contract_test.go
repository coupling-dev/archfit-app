package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

type route struct {
	method, path, min string
	form              url.Values
	body              any // a JSON body: an /api write
}

func (rt route) api() bool { return strings.HasPrefix(rt.path, apiPrefix) }

// repoRoutes is every repository route with its minimum permission and a form that succeeds.
func repoRoutes(fullName string) []route {
	base, api := "/r/"+fullName, "/api/repos/"+fullName
	return []route{
		{http.MethodGet, base, permRead, nil, nil},
		{http.MethodGet, api + "/overview", permRead, nil, nil},
		{http.MethodPost, api + "/onboarding", permAdmin, nil, onboardingJSON{Mode: "customer", Owners: "alice"}},
		{http.MethodPost, api + "/discover", permAdmin, nil, struct{}{}},
		{http.MethodGet, base + "/policy", permRead, nil, nil},
		{http.MethodPost, base + "/policy", permWrite, formOf("", "", policyText+"# edit\n"), nil},
		{http.MethodPost, base + "/policy/check", permRead, formOf("", "", policyText+"# edit\n"), nil},
		{http.MethodPost, base + "/policy/ready", permWrite, nil, nil}, // the form is filled in by the test: it needs an open draft
		{http.MethodGet, base + "/graph", permRead, nil, nil},
		{http.MethodGet, base + "/pull/4", permRead, nil, nil},
		{http.MethodGet, base + "/pull/4/graph", permRead, nil, nil},
		{http.MethodPost, base + "/assist", permWrite, url.Values{"mode": {"customer"}, "instruction": {"split module a"}, "content": {policyText}}, nil},
		{http.MethodGet, base + "/consent", permAdmin, nil, nil},
		{http.MethodPost, base + "/consent", permAdmin, url.Values{"kind": {"managed_llm"}, "action": {"record"}}, nil},
	}
}

var permissionRank = map[string]int{"none": 0, permRead: 1, permWrite: 2, permAdmin: 3}

func (c *client) send(rt route) int {
	if rt.method == http.MethodGet {
		return c.get(rt.path).Code
	}
	if rt.api() {
		return c.postJSON(rt.path, rt.body).Code
	}
	form := url.Values{}
	for k, v := range rt.form {
		form[k] = v
	}
	return c.post(rt.path, form).Code
}

func TestOnboardingOffersOnlyCustomerDiscovery(t *testing.T) {
	e := newEnv(t)
	e.onb.progress.Stage = onboarding.StageInstalled
	var body statusBody
	e.as("alice", permAdmin).getJSON("/api/repos/"+repoName+"/overview", &body)
	if !body.Admin || !body.CanStart {
		t.Errorf("admin = %v, can start = %v; want an administrator who can start onboarding", body.Admin, body.CanStart)
	}
	raw := e.signIn("alice").get("/api/repos/" + repoName + "/overview").Body.String()
	if strings.Contains(raw, "managed") {
		t.Error("onboarding offers managed discovery without an available sandbox")
	}
}

func TestRoutesAndMinimumPermissions(t *testing.T) {
	for _, rt := range repoRoutes(repoName) {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			e := newEnv(t)
			res := e.anonymous().do(requestOf(rt))
			if rt.method == http.MethodGet && !rt.api() {
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
				case rt.method == http.MethodGet, strings.HasSuffix(rt.path, "/policy/check"), rt.api():
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
		wantContains(t, res.Body.String(), `src="/assets/app-1a2b.js"`)
		for _, path := range []string{"/api/session", "/api/repos"} {
			wantStatus(t, e.anonymous().get(path), http.StatusUnauthorized)
			wantStatus(t, e.as("alice", permRead).get(path), http.StatusOK)
		}
	})
	t.Run("logout", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.anonymous().post("/auth/logout", nil), http.StatusUnauthorized)
		c := e.as("alice", "none")
		wantStatus(t, c.get("/api/session"), http.StatusOK)
		wantStatus(t, c.post("/auth/logout", nil), http.StatusSeeOther)
		wantStatus(t, c.get("/api/session"), http.StatusUnauthorized)
	})
	t.Run("public assets", func(t *testing.T) {
		e := newEnv(t)
		for _, path := range []string{"/static/app.css", "/static/editor.js", "/static/graph.js", "/schema/policy.json", "/assets/app-1a2b.js"} {
			res := e.anonymous().get(path)
			wantStatus(t, res, http.StatusOK)
			if res.Body.Len() == 0 {
				t.Errorf("%s is empty", path)
			}
		}
		if got := e.anonymous().get("/assets/app-1a2b.js").Header().Get("Cache-Control"); got != immutableCache {
			t.Errorf("asset Cache-Control = %q, want %q", got, immutableCache)
		}
		for _, path := range []string{"/static/", "/assets/", "/assets/missing.js", "/index.html", "/api/nothing"} {
			wantStatus(t, e.anonymous().get(path), http.StatusNotFound)
		}
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
	c := e.signIn("alice")
	res := c.get("/api/repos")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()
	var got overviewBody
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	states := map[string]string{}
	for _, r := range got.Repos {
		states[r.FullName] = r.State
	}
	for _, row := range rows {
		if states[row.name] != row.want {
			t.Errorf("%s: row reads %q, want %q", row.name, states[row.name], row.want)
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
	rep, err := report.Decode(payload, "v3.0.0")
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
	wantContains(t, body, "violation", `id="graph-tables" open`, `id="g-list"`, `id="g-canvas"`, `data-gview="matrix"`)
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

// TestGraphPageShowsTheEngineSeamPolicy: an engine v3.0.0 artifact decodes, and
// each link shows the engine's seams[].policy status, allowed included.
func TestGraphPageShowsTheEngineSeamPolicy(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	payload, err := os.ReadFile(filepath.Join("..", "..", "wire", "testdata", "report", "real-seam-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := report.Decode(payload, "v3.0.0")
	if err != nil {
		t.Fatalf("capture does not decode: %v", err)
	}
	e.publishReport(repoName, payload)

	res := c.get("/r/" + repoName + "/graph")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()

	var got graph.View
	if err := json.Unmarshal([]byte(embeddedJSON(t, body)), &got); err != nil {
		t.Fatalf("embedded JSON: %v", err)
	}
	if want := graph.Build(rep, []byte(policyText)); !reflect.DeepEqual(got, want) {
		t.Errorf("embedded view = %+v\nwant %+v", got, want)
	}
	for _, status := range []string{"violation", "accepted", "allowed", "observed"} {
		wantContains(t, body, `<tr class="`+status+`">`)
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
	base := "/api/repos/" + repoName

	res := c.postJSON(base+"/onboarding", onboardingJSON{Mode: "customer", Owners: "alice\r\n\r\n  acme/platform-team \n"})
	wantStatus(t, res, http.StatusOK)
	wantContains(t, res.Body.String(), `"notice":"`+notices["onboarding"]+`"`)
	if len(e.onb.customer) != 1 || !reflect.DeepEqual(e.onb.customer[0].owners, []string{"alice", "acme/platform-team"}) {
		t.Errorf("StartCustomer calls = %+v", e.onb.customer)
	}
	if e.onb.customer[0].repo.InstallationID != installA {
		t.Errorf("StartCustomer repo = %+v", e.onb.customer[0].repo)
	}

	res = c.postJSON(base+"/onboarding", onboardingJSON{Mode: "managed", Owners: "alice"})
	wantStatus(t, res, http.StatusOK)
	var next noticeJSON
	if err := json.Unmarshal(res.Body.Bytes(), &next); err != nil || next.Location != "/r/"+repoName+"/consent" {
		t.Errorf("answer = %s, want the consent page as location", res.Body.String())
	}
	if len(e.onb.managed) != 0 {
		t.Error("StartManaged was called without consent")
	}
	_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentSourceProcessing})
	wantStatus(t, c.postJSON(base+"/onboarding", onboardingJSON{Mode: "managed", Owners: "alice"}), http.StatusOK)
	if len(e.onb.managed) != 1 {
		t.Errorf("StartManaged calls = %d, want 1", len(e.onb.managed))
	}

	wantStatus(t, c.postJSON(base+"/discover", struct{}{}), http.StatusConflict)
	if len(e.gh.called("Dispatch")) != 0 {
		t.Fatal("discovery was dispatched before workflow_merged")
	}
	e.onb.progress.Stage = onboarding.StageWorkflowMerged
	wantStatus(t, c.postJSON(base+"/discover", struct{}{}), http.StatusOK)
	calls := e.gh.called("Dispatch")
	if len(calls) != 1 || calls[0].detail != workflow+"@"+mainBranch+" map[mode:discovery]" || calls[0].installationID != installA {
		t.Errorf("Dispatch calls = %+v", calls)
	}
}

func TestPullPageShowsTheReportOfItsCheck(t *testing.T) {
	payload := artifact(t, "real-blocked-no-baseline")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	path := "/r/" + repoName + "/pull/4"
	check := func(e *env, externalID string) {
		e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"] = []api.CheckRun{{
			ID: 9, Name: "archfit/state", HeadSHA: sha(4), Status: "completed", Conclusion: "neutral",
			Title: "Architecture violations found · advisory · no baseline", ExternalID: externalID,
		}}
	}
	tests := []struct {
		name      string
		setup     func(e *env)
		status    int
		wants     []string
		notWanted []string
	}{
		{"no check on the head", func(*env) {}, http.StatusOK, []string{"feat: read options", noResult, `id="unavailable"`}, []string{`id="result"`}},
		{"the report of the check", func(e *env) {
			check(e, "v1:88:1:"+digest)
			e.gh.artifacts[88] = payload
		}, http.StatusOK, []string{
			`id="result"`, "Architecture violations found · advisory · no baseline", "gate: advisory", `id="violations"`,
			"no_direct_b_dependency", "No baseline in this run", "PR attribution unavailable", "Run 88",
		}, []string{`id="unavailable"`, "added by this PR"}},
		{"a report that does not match its check", func(e *env) {
			check(e, "v1:88:1:sha256:"+strings.Repeat("0", 64))
			e.gh.artifacts[88] = payload
		}, http.StatusOK, []string{reportMismatch, "Architecture violations found"}, []string{"no_direct_b_dependency"}},
		{"an expired report", func(e *env) { check(e, "v1:88:1:"+digest) }, http.StatusOK, []string{reportExpired}, []string{"no_direct_b_dependency"}},
		{"a result without an upload", func(e *env) { check(e, "v1:0:0:") }, http.StatusOK, []string{noUpload}, nil},
		{"an unknown pull request", func(e *env) { delete(e.gh.pulls, repoName+"#4") }, http.StatusNotFound, nil, nil},
		{"a withdrawn result keeps its old run but shows no finding", func(e *env) {
			check(e, "v1:88:1:"+digest)
			e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"][0].Conclusion = "action_required"
			e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"][0].Title = "Action required"
			e.gh.artifacts[88] = payload
		}, http.StatusOK, []string{actionRequired, "Action required"}, []string{"no_direct_b_dependency", "For coding agents"}},
		{"a report that does not decode", func(e *env) {
			broken := []byte(`{"schema_version":"nope"}`)
			s := sha256.Sum256(broken)
			check(e, "v1:88:1:sha256:"+hex.EncodeToString(s[:]))
			e.gh.artifacts[88] = broken
		}, http.StatusOK, []string{reportUnavailable}, []string{"no_direct_b_dependency"}},
		{"the newest check run wins", func(e *env) {
			check(e, "v1:88:1:"+digest)
			e.gh.artifacts[88] = payload
			key := repoName + "@" + sha(4) + "#archfit/state"
			older := api.CheckRun{ID: 3, Name: "archfit/state", Status: "completed", Conclusion: "action_required", Title: "Older", ExternalID: "v1:0:0:"}
			e.gh.checks[key] = append([]api.CheckRun{older}, e.gh.checks[key]...)
		}, http.StatusOK, []string{"no_direct_b_dependency"}, []string{"Older"}},
		{"added by this PR only with a comparison", func(e *env) {
			compared := bytes.Replace(payload, []byte(`"comparison":{"status":"not_requested"`), []byte(`"comparison":{"status":"not_requested","origin_status":"comparable"`), 1)
			// The top-level finding is the last object with this id; dimension references come first.
			id := []byte(`{"id":"ba3803eca947bf3c1b7efa8f37854d5e"`)
			at := bytes.LastIndex(compared, id) + len(id)
			compared = append(compared[:at:at], append([]byte(`,"origin":"introduced"`), compared[at:]...)...)
			s := sha256.Sum256(compared)
			check(e, "v1:88:1:sha256:"+hex.EncodeToString(s[:]))
			e.gh.artifacts[88] = compared
		}, http.StatusOK, []string{"added by this PR", "compared with the base branch"}, []string{"PR attribution unavailable"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.setup(e)
			res := e.as("alice", permRead).get(path)
			wantStatus(t, res, tt.status)
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

func TestPullPageNumberMustBeAPull(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	for _, n := range []string{"0", "-4", "x", "4x", "5"} {
		wantStatus(t, c.get("/r/"+repoName+"/pull/"+n), http.StatusNotFound)
		wantStatus(t, c.get("/r/"+repoName+"/pull/"+n+"/graph"), http.StatusNotFound)
	}
}

// TestPullGraphDrawsTheReportOfItsCheck: the pull request graph is the report of the run the head's archfit/state
// check names, the one the pull request page shows, never the default branch's latest report.
func TestPullGraphDrawsTheReportOfItsCheck(t *testing.T) {
	e := newEnv(t)
	e.publishReport(repoName, artifact(t, "healthy"))
	payload := prArtifact(t, prPolicy)
	e.checkPull(payload)
	e.gh.setFile(repoName, sha(4), policy.PolicyPath, prPolicy)
	rep, err := report.Decode(payload, "v3.0.1")
	if err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}

	res := e.as("alice", permRead).get("/r/" + repoName + "/pull/4/graph")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()
	var got graph.View
	if err := json.Unmarshal([]byte(embeddedJSON(t, body)), &got); err != nil {
		t.Fatalf("embedded JSON: %v", err)
	}
	if want := graph.Build(rep, []byte(prPolicy)); !reflect.DeepEqual(got, want) {
		t.Errorf("embedded view = %+v\nwant %+v", got, want)
	}
	if got.Stale {
		t.Error("the head's policy is the report's, yet the view is stale")
	}
	for _, call := range e.gh.called("Artifact") {
		if !strings.HasPrefix(call.detail, "88 ") {
			t.Errorf("Artifact(%s): the graph read another run than the check's", call.detail)
		}
	}
	if calls := e.gh.called("LatestRun"); len(calls) != 0 {
		t.Errorf("LatestRun called %d times: the default branch's report has no place here", len(calls))
	}
	wantContains(t, html.UnescapeString(body),
		"feat: read options", "#4", "<code>"+sha(4)[:7]+"</code>", "Architecture violations found · advisory",
		`href="/r/`+repoName+`/pull/4"`, `href="https://github.com/`+repoName+`/pull/4"`, `data-scope="pull"`,
		`data-policy-url="https://github.com/`+repoName+`/blob/`+sha(4)+`/.archfit.yaml"`,
		`id="new-count"`, "4 links have a finding the base branch does not have: new in this PR.")
	for _, s := range []string{`id="not-compared"`, `id="stale"`} {
		if strings.Contains(body, s) {
			t.Errorf("body contains %q", s)
		}
	}
}

func TestPullPageLinksItsGraph(t *testing.T) {
	e := newEnv(t)
	body := e.as("alice", permRead).get("/r/" + repoName + "/pull/4").Body.String()
	wantContains(t, body, `id="pull-graph"`, `href="/r/`+repoName+`/pull/4/graph"`, `aria-current="page">Findings</a>`)
}

func TestPullPageReadsTheGateModeOfTheBase(t *testing.T) {
	e := newEnv(t)
	pull := e.gh.pulls[repoName+"#4"]
	pull.BaseRef = "release"
	e.gh.pulls[repoName+"#4"] = pull
	e.gh.setFile(repoName, "release", settings.Path, strings.Replace(settingsText, "gate: advisory", "gate: required", 1))
	body := e.as("alice", permRead).get("/r/" + repoName + "/pull/4").Body.String()
	wantContains(t, body, "gate: required")
}

func TestParseExternalID(t *testing.T) {
	tests := []struct {
		id     string
		run    int64
		digest string
		ok     bool
	}{
		{"v1:39113331372:1:sha256:2f34", 39113331372, "2f34", true},
		{"v1:7:1:4444", 7, "4444", true},
		{"v1:0:0:", 0, "", true},
		{"v2:7:1:abc", 0, "", false},
		{"v1:x:1:abc", 0, "", false},
		{"", 0, "", false},
	}
	for _, tt := range tests {
		run, digest, ok := parseExternalID(tt.id)
		if run != tt.run || digest != tt.digest || ok != tt.ok {
			t.Errorf("parseExternalID(%q) = %d, %q, %v; want %d, %q, %v", tt.id, run, digest, ok, tt.run, tt.digest, tt.ok)
		}
	}
}

func TestStatusPageShowsBranchPullsAndSetup(t *testing.T) {
	setup := func(e *env, stage onboarding.Stage) {
		e.onb.progress = onboarding.Progress{Stage: stage}
		e.gh.checks[repoName+"@"+sha(10)+"#"+checkState] = []api.CheckRun{{ID: 1, Conclusion: "neutral", Title: "Architecture needs attention"}}
		e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{{ID: 2, Conclusion: "neutral", Title: "Architecture violations found · advisory"}}
	}
	path := "/api/repos/" + repoName + "/overview"
	read := func(e *env) statusBody {
		var body statusBody
		e.as("alice", permRead).getJSON(path, &body)
		return body
	}
	stepOf := func(body statusBody, label string) step {
		for _, s := range body.Steps {
			if s.Label == label {
				return s
			}
		}
		t.Fatalf("no step %q in %+v", label, body.Steps)
		return step{}
	}
	t.Run("advisory", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StageAdvisory)
		body := read(e)
		if body.DefaultBranch != mainBranch || body.MainState != "attention" || body.MainTone != "attention" || body.MainReason != "Architecture needs attention" {
			t.Errorf("default branch = %q %q %q", body.DefaultBranch, body.MainState, body.MainReason)
		}
		want := pullRow{Number: 4, Title: "feat: read options", State: "attention", Tone: "attention", Reason: "Architecture violations found · advisory", Href: "/r/" + repoName + "/pull/4"}
		if len(body.Pulls) != 1 || body.Pulls[0] != want {
			t.Errorf("pulls = %+v, want [%+v]", body.Pulls, want)
		}
		if !stepOf(body, "Advisory").Current || !stepOf(body, "Policy merged").Done || !strings.Contains(body.Next, "Rehearse the gate") {
			t.Errorf("steps = %+v, next = %q", body.Steps, body.Next)
		}
	})
	t.Run("before the first report there is nothing to read", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StageWorkflowMerged)
		body := read(e)
		if body.MainState != "" || body.Pulls != nil || len(e.gh.called("OpenPulls")) != 0 {
			t.Errorf("pull requests looked up before any report")
		}
		if !stepOf(body, "Workflow merged").Current {
			t.Errorf("steps = %+v", body.Steps)
		}
	})
	t.Run("a merged policy without a report reads nothing and marks no report", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StagePolicyMerged)
		body := read(e)
		if body.MainState != "" || len(e.gh.called("OpenPulls")) != 0 {
			t.Errorf("state or pull requests read before a report")
		}
		if first := stepOf(body, "First report"); first.Done || first.Current || !stepOf(body, "Policy merged").Current {
			t.Errorf("steps = %+v", body.Steps)
		}
	})
	t.Run("required still names the ruleset", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StageRequired)
		if next := read(e).Next; !strings.Contains(next, "required check in a GitHub ruleset") {
			t.Errorf("next = %q", next)
		}
	})
	t.Run("a failed list leaves the page", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StageAdvisory)
		e.gh.fail["OpenPulls"] = errors.New("boom")
		body := read(e)
		if !body.PullsError || body.MainState != "attention" {
			t.Errorf("pulls error = %v, main state = %q", body.PullsError, body.MainState)
		}
	})
	t.Run("at most maxStatusPulls are looked up", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StageAdvisory)
		for n := 100; n < 100+maxStatusPulls+3; n++ {
			e.gh.pulls[repoName+"#"+strconv.Itoa(n)] = api.Pull{Number: n, Title: "p", State: "open", HeadSHA: sha(n)}
		}
		body := read(e)
		if len(body.Pulls) != maxStatusPulls || body.PullsMore != 4 {
			t.Errorf("pulls = %d, more = %d; want %d and 4", len(body.Pulls), body.PullsMore, maxStatusPulls)
		}
		if got := len(e.gh.called("CheckRuns")); got != maxStatusPulls+1 {
			t.Errorf("CheckRuns calls = %d, want %d (one per listed pull request and one for the default branch)", got, maxStatusPulls+1)
		}
	})
}
