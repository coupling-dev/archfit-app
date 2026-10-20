package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
	form              url.Values // a form body: the only form route left is the logout
	body              any        // a JSON body: an /api write
}

func (rt route) api() bool { return strings.HasPrefix(rt.path, apiPrefix) }

// repoRoutes is every repository route with its minimum permission and a body that succeeds.
func repoRoutes(fullName string) []route {
	base, api := "/r/"+fullName, "/api/repos/"+fullName
	return []route{
		{http.MethodGet, base, permRead, nil, nil},
		{http.MethodGet, api + "/overview", permRead, nil, nil},
		{http.MethodPost, api + "/onboarding", permAdmin, nil, onboardingJSON{Mode: "customer", Owners: "alice"}},
		{http.MethodPost, api + "/discover", permAdmin, nil, struct{}{}},
		{http.MethodGet, base + "/policy", permRead, nil, nil},
		{http.MethodGet, api + "/policy", permRead, nil, nil},
		{http.MethodPost, api + "/policy", permWrite, nil, editOf("", "", policyText+"# edit\n")},
		{http.MethodPost, api + "/policy/check", permRead, nil, checkPolicyJSON{Content: policyText + "# edit\n"}},
		{http.MethodPost, api + "/policy/ready", permWrite, nil, readyJSON{}}, // withHead fills in the head of an open draft
		{http.MethodGet, base + "/graph", permRead, nil, nil},
		{http.MethodGet, base + "/pull/4", permRead, nil, nil},
		{http.MethodGet, api + "/pulls/4", permRead, nil, nil},
		{http.MethodGet, api + "/pulls/4/graph", permRead, nil, nil},
		{http.MethodGet, api + "/graph", permRead, nil, nil},
		{http.MethodGet, api + "/findings", permRead, nil, nil},
		{http.MethodGet, base + "/pull/4/graph", permRead, nil, nil},
		{http.MethodPost, api + "/assist", permWrite, nil, assistOf("", policyText)},
		{http.MethodGet, base + "/data-sharing", permAdmin, nil, nil},
		{http.MethodGet, api + "/data-sharing", permAdmin, nil, nil},
		{http.MethodPost, api + "/data-sharing", permAdmin, nil, dataSharingJSON{Kind: "managed_llm", Action: actionAllow}},
	}
}

// withHead is rt with headSHA in its body, when the body is a write of the policy page.
func (rt route) withHead(headSHA string) route {
	switch b := rt.body.(type) {
	case editJSON:
		b.HeadSHA = headSHA
		rt.body = b
	case assistJSON:
		b.HeadSHA = headSHA
		rt.body = b
	case readyJSON:
		b.HeadSHA = headSHA
		rt.body = b
	}
	return rt
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
	// managed_ai is the Data sharing row, which names a consent and offers nothing.
	raw := strings.Replace(e.signIn("alice").get("/api/repos/"+repoName+"/overview").Body.String(), `"managed_ai":`, "", 1)
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
					rt = rt.withHead(e.props.push(repoName, "archfit/edit-alice", policyText+"# draft\n").HeadSHA)
				}
				got := c.send(rt)
				switch {
				case permissionRank[perm] < permissionRank[rt.min]:
					if got != http.StatusNotFound {
						t.Errorf("%s: status = %d, want 404", perm, got)
					}
				case got != http.StatusOK:
					t.Errorf("%s: status = %d, want 200", perm, got)
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
		for _, path := range []string{"/schema/policy.json", "/assets/app-1a2b.js", licensesRoute} {
			res := e.anonymous().get(path)
			wantStatus(t, res, http.StatusOK)
			if res.Body.Len() == 0 {
				t.Errorf("%s is empty", path)
			}
		}
		if got := e.anonymous().get("/assets/app-1a2b.js").Header().Get("Cache-Control"); got != immutableCache {
			t.Errorf("asset Cache-Control = %q, want %q", got, immutableCache)
		}
		licences := e.anonymous().get(licensesRoute)
		if got := licences.Header().Get("Cache-Control"); got != "no-cache" || licences.Body.String() != string(testWeb["assets/licenses-9f8e.txt"].Data) {
			t.Errorf("licences: Cache-Control %q, body %q; want no-cache and the build's licence file", got, licences.Body.String())
		}
		for _, path := range []string{"/static/app.css", "/assets/", "/assets/missing.js", "/index.html", "/api/nothing"} {
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

	page := c.policy("")
	if page.Branch != "archfit/edit-alice" || page.HeadSHA != draft.HeadSHA || page.Content != policyText+"# draft\n" {
		t.Fatalf("policy page = %+v", page)
	}

	content := policyText + "# draft\n# changed\n"
	res := c.postJSON(policyAPI, editOf(page.Branch, page.HeadSHA, content))
	wantStatus(t, res, http.StatusOK)

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
	after := decodeAs[policyJSON](t, res)
	if after.Draft != "draft" || after.DraftState != "draft" || after.Content != content || after.HeadSHA != e.props.drafts[repoName+"@archfit/edit-alice"].HeadSHA ||
		after.Notice != notices["saved"] || !after.CanWrite || !after.CanReady {
		t.Errorf("answer = %+v, want the saved draft", after)
	}
}

func TestFirstSaveOpensOneDraftPR(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permWrite)
	content := policyText + "# first\n"

	wantStatus(t, c.postJSON(policyAPI, editOf("", "", content)), http.StatusOK)
	retry := c.postJSON(policyAPI, editOf("", "", content))
	wantStatus(t, retry, http.StatusOK)
	if got := decodeAs[policyJSON](t, retry).Notice; got != notices["unchanged"] {
		t.Errorf("retry notice = %q, want %q", got, notices["unchanged"])
	}

	if len(e.props.drafts) != 1 || e.props.commits != 1 || len(e.props.proposes) != 1 {
		t.Errorf("drafts = %d, commits = %d, Propose calls = %d; want 1 each", len(e.props.drafts), e.props.commits, len(e.props.proposes))
	}

	different := c.postJSON(policyAPI, editOf("", "", content+"# other\n"))
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

	wantStatus(t, c.get("/r/"+repoName+"/graph"), http.StatusOK)
	path := "/api/repos/" + repoName + "/graph"
	var got graphJSON
	c.getJSON(path, &got)
	want := graph.Build(rep, []byte(policyText))
	if got.View == nil || !reflect.DeepEqual(*got.View, want) {
		t.Errorf("view = %+v\nwant %+v", got.View, want)
	}
	if len(want.Nodes) == 0 || len(want.Links) == 0 || want.Stale {
		t.Fatalf("fixture gives a useless view: %+v", want)
	}
	if got.PolicyHref != "/r/"+repoName+"/policy" || got.Branch != mainBranch || got.Pull != nil || got.Unavailable != "" || got.StaleReason != "" {
		t.Errorf("body = %+v", got)
	}
	// hard_gates: fail with nothing unevaluated: the violations are the message, so the page does not say incomplete.
	if want.Incomplete || got.View.Incomplete {
		t.Error("Incomplete = true for a failed hard gate with no unevaluated required rule")
	}
	if strings.Contains(c.get(path).Body.String(), "MARKER-") {
		t.Error("a field the decoder drops reached the API")
	}
	for _, call := range e.gh.called("ArtifactByID") {
		if call.detail != fmt.Sprintf("77 %d", report.MaxBytes) {
			t.Errorf("ArtifactByID(%s)", call.detail)
		}
	}
}

// TestGraphPageShowsTheEngineSeamPolicy: an engine v3.0.0 artifact decodes, and each link shows the engine's
// seams[].policy status, allowed included, raised by the accepted advisories it leaves out (graph.Build).
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

	var got graphJSON
	c.getJSON("/api/repos/"+repoName+"/graph", &got)
	if want := graph.Build(rep, []byte(policyText)); got.View == nil || !reflect.DeepEqual(*got.View, want) {
		t.Fatalf("view = %+v\nwant %+v", got.View, want)
	}
	statuses := map[graph.LinkStatus]bool{}
	for _, l := range got.View.Links {
		statuses[l.Status] = true
	}
	for _, s := range []graph.LinkStatus{graph.StatusViolation, graph.StatusAccepted, graph.StatusAllowed} {
		if !statuses[s] {
			t.Errorf("no %s link", s)
		}
	}
}

func TestDataSharingAllowAndRevoke(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	path := "/api/repos/" + repoName + "/data-sharing"
	feature := func(t *testing.T, body dataSharingBody, kind account.ConsentKind) featureJSON {
		t.Helper()
		for _, f := range body.Features {
			if f.Kind == string(kind) {
				return f
			}
		}
		t.Fatalf("no %s in %+v", kind, body)
		return featureJSON{}
	}
	allow := dataSharingJSON{Kind: string(account.ConsentManagedLLM), Action: actionAllow}
	revoke := dataSharingJSON{Kind: string(account.ConsentManagedLLM), Action: actionRevoke}

	var before dataSharingBody
	c.getJSON(path, &before)
	ai := feature(t, before, account.ConsentManagedLLM)
	owner, _, _ := strings.Cut(repoName, "/")
	if before.Owner != owner || ai.Allowed || ai.By != "" || ai.At != "" || len(before.Features) != len(sharedFeatures) {
		t.Errorf("before = %+v", before)
	}
	if want := []precondition{{ai.Checks[0].Label, checkNotMet}, {ai.Checks[1].Label, checkMet}}; !reflect.DeepEqual(ai.Checks, want) {
		t.Errorf("checks = %+v; want the consent unmet and data_mode: managed met", ai.Checks)
	}
	if discovery := feature(t, before, account.ConsentSourceProcessing); discovery.Note == "" || len(discovery.Checks) != 1 {
		t.Errorf("managed discovery = %+v; want the note that the App does not start it, and only the consent to check", discovery)
	}

	res := c.postJSON(path, allow)
	wantStatus(t, res, http.StatusOK)
	got := e.consents.recorded[installA][account.ConsentManagedLLM]
	alice := e.user("alice")
	if got.By != "alice" || got.ByID != alice.UserID || !got.At.Equal(e.now) || got.Kind != account.ConsentManagedLLM {
		t.Errorf("consent = %+v; want alice (%d) at %v", got, alice.UserID, e.now)
	}
	var after dataSharingBody
	if err := json.Unmarshal(res.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if ai := feature(t, after, account.ConsentManagedLLM); !ai.Allowed || ai.By != "alice" || ai.At != e.now.UTC().Format(time.RFC3339) ||
		ai.Checks[0].State != checkMet || after.Notice == "" {
		t.Errorf("after allow = %+v", after)
	}

	t.Run("allowing again keeps who allowed it and when", func(t *testing.T) {
		writes := e.consents.writes
		e.now = e.now.Add(time.Hour)
		wantStatus(t, e.as("bob", permAdmin).postJSON(path, allow), http.StatusOK)
		if again := e.consents.recorded[installA][account.ConsentManagedLLM]; again != got || e.consents.writes != writes {
			t.Errorf("consent = %+v after %d writes; want %+v unchanged", again, e.consents.writes-writes, got)
		}
	})

	wantStatus(t, c.postJSON(path, revoke), http.StatusOK)
	if e.consents.Has(t.Context(), installA, account.ConsentManagedLLM) {
		t.Error("consent still recorded after revoke")
	}
	wantStatus(t, c.postJSON(path, revoke), http.StatusOK) // revoking nothing is not an error

	for _, body := range []any{
		dataSharingJSON{Kind: "everything", Action: actionAllow},
		dataSharingJSON{Kind: "managed_llm", Action: "record"},
		dataSharingJSON{Kind: "managed_llm"},
		dataSharingJSON{Action: actionAllow},
	} {
		wantStatus(t, c.postJSON(path, body), http.StatusBadRequest)
	}
	if e.consents.Has(t.Context(), installA, account.ConsentManagedLLM) {
		t.Error("a refused write recorded a consent")
	}
}

func TestDataSharingNeverShowsAnOutageAsNotAllowed(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentManagedLLM, By: "alice", ByID: 1, At: e.now})
	e.consents.readErr = errors.New("store down")
	writes := e.consents.writes

	path := "/api/repos/" + repoName + "/data-sharing"
	wantStatus(t, c.get(path), http.StatusBadGateway)
	wantStatus(t, c.postJSON(path, dataSharingJSON{Kind: string(account.ConsentManagedLLM), Action: actionAllow}), http.StatusBadGateway)
	if e.consents.writes != writes {
		t.Error("allow wrote a consent it could not read first")
	}
	var body statusBody
	c.getJSON("/api/repos/"+repoName+"/overview", &body)
	if body.ManagedAI != checkUnknown {
		t.Errorf("overview managed_ai = %q, want %q", body.ManagedAI, checkUnknown)
	}

	t.Run("a change saved but not read back does not say nothing changed", func(t *testing.T) {
		e := newEnv(t)
		e.consents.readErrAfterWrite = errors.New("store down")
		res := e.as("alice", permAdmin).postJSON(path, dataSharingJSON{Kind: string(account.ConsentManagedLLM), Action: actionAllow})
		wantStatus(t, res, http.StatusBadGateway)
		wantContains(t, res.Body.String(), "The change was saved")
		if _, ok := e.consents.recorded[installA][account.ConsentManagedLLM]; !ok {
			t.Error("the consent was not recorded")
		}
	})
}

func TestDataSharingReadsTheRepositorySettings(t *testing.T) {
	for _, tt := range []struct {
		name, settings, want string
	}{
		{"managed", settingsText, checkMet},
		{"customer", strings.Replace(settingsText, "data_mode: managed", "data_mode: customer", 1), checkNotMet},
		{"no settings yet", "", checkUnknown},
		{"invalid settings", "version: [", checkUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.settings == "" {
				e.gh.deleteFile(repoName, mainBranch, settings.Path)
			} else {
				e.gh.setFile(repoName, mainBranch, settings.Path, tt.settings)
			}
			var body dataSharingBody
			e.as("alice", permAdmin).getJSON("/api/repos/"+repoName+"/data-sharing", &body)
			for _, f := range body.Features {
				if f.Kind == string(account.ConsentManagedLLM) && f.Checks[len(f.Checks)-1].State != tt.want {
					t.Errorf("data_mode check = %+v, want %s", f.Checks, tt.want)
				}
			}
		})
	}
}

func TestOverviewNamesDataSharingOnlyToAnAdministrator(t *testing.T) {
	e := newEnv(t)
	overview := "/api/repos/" + repoName + "/overview"
	var body statusBody
	e.as("alice", permAdmin).getJSON(overview, &body)
	if body.ManagedAI != "not_allowed" {
		t.Errorf("before consent: managed_ai = %q, want not_allowed", body.ManagedAI)
	}
	_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentManagedLLM, By: "alice", ByID: 1, At: e.now})
	before := len(e.consents.touched)
	e.as("alice", permAdmin).getJSON(overview, &body)
	if body.ManagedAI != "allowed" || len(e.consents.touched)-before != 1 {
		t.Errorf("after consent: managed_ai = %q after %d consent reads; want allowed after one", body.ManagedAI, len(e.consents.touched)-before)
	}

	before = len(e.consents.touched)
	raw := e.as("bob", permWrite).get(overview).Body.String()
	if strings.Contains(raw, "managed_ai") || len(e.consents.touched) != before {
		t.Errorf("a writer's overview read the consent or names it: %.200s", raw)
	}
}

func TestEditorSaysWhyManagedModeIsOff(t *testing.T) {
	sharing := "/r/" + repoName + "/data-sharing"
	customer := strings.Replace(settingsText, "data_mode: managed", "data_mode: customer", 1)
	for _, tt := range []struct {
		name     string
		perm     string
		settings string
		consent  bool
		want     managedJSON
		canWrite bool
	}{
		{"customer settings", permAdmin, customer, true,
			managedJSON{Off: "Managed mode is off: this repository's settings say data_mode: customer."}, true},
		{"invalid settings", permAdmin, "version: [", true,
			managedJSON{Off: "Managed mode is off: the default branch has no valid " + settings.Path + "."}, true},
		{"no consent, administrator", permAdmin, settingsText, false,
			managedJSON{Off: "Managed mode is off: managed AI is not allowed for acme.", DataSharing: sharing}, true},
		{"no consent, writer", permWrite, settingsText, false,
			managedJSON{Off: "Managed mode is off: managed AI is not allowed for acme."}, true},
		{"offered", permWrite, settingsText, true, managedJSON{Offer: true}, true},
		{"a reader is offered nothing", permRead, settingsText, true, managedJSON{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.gh.setFile(repoName, mainBranch, settings.Path, tt.settings)
			if tt.consent {
				_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentManagedLLM, By: "alice", ByID: 1, At: e.now})
			}
			page := e.as("alice", tt.perm).policy("")
			if page.Managed != tt.want || page.CanWrite != tt.canWrite {
				t.Errorf("managed = %+v, can_write = %v; want %+v, %v", page.Managed, page.CanWrite, tt.want, tt.canWrite)
			}
		})
	}
}

func TestOldConsentPageMovedToDataSharing(t *testing.T) {
	path := "/r/" + repoName + "/consent"
	for _, perm := range []string{"none", permRead, permWrite} {
		e := newEnv(t)
		res := e.as("alice", perm).get(path)
		wantStatus(t, res, http.StatusNotFound)
		if loc := res.Header().Get("Location"); loc != "" {
			t.Errorf("%s: Location %q tells a non-administrator the repository exists", perm, loc)
		}
	}
	e := newEnv(t)
	res := e.as("alice", permAdmin).get(path)
	wantStatus(t, res, http.StatusPermanentRedirect)
	if loc := res.Header().Get("Location"); loc != "/r/"+repoName+"/data-sharing" {
		t.Errorf("Location = %q", loc)
	}
	wantStatus(t, e.as("alice", permAdmin).get("/r/"+otherRepo+"/consent"), http.StatusNotFound)
}

// Each stage offers only its own action: an overview never shows a button for a step that is not the next one.
func TestOverviewOffersOnlyTheStepsAction(t *testing.T) {
	draft := &proposal.Draft{URL: "https://github.com/" + repoName + "/pull/9", State: proposal.DraftOpen}
	tests := []struct {
		name            string
		progress        onboarding.Progress
		start, discover bool
	}{
		{"installed", onboarding.Progress{Stage: onboarding.StageInstalled}, true, false},
		{"installed, onboarding pull request open", onboarding.Progress{Stage: onboarding.StageInstalled, OpenDraft: draft}, false, false},
		{"workflow merged", onboarding.Progress{Stage: onboarding.StageWorkflowMerged}, false, true},
		{"workflow merged, policy pull request open", onboarding.Progress{Stage: onboarding.StageWorkflowMerged, OpenDraft: draft}, false, false},
		{"report received, no policy", onboarding.Progress{Stage: onboarding.StageReportReceived}, false, true},
		{"policy merged", onboarding.Progress{Stage: onboarding.StagePolicyMerged}, false, false},
		{"gate advisory", onboarding.Progress{Stage: onboarding.StageAdvisory}, false, false},
		{"gate required", onboarding.Progress{Stage: onboarding.StageRequired}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.onb.progress = tt.progress
			var b statusBody
			e.as("alice", permAdmin).getJSON("/api/repos/"+repoName+"/overview", &b)
			if b.CanStart != tt.start || b.CanDiscover != tt.discover {
				t.Errorf("can_start = %v, can_discover = %v, want %v, %v", b.CanStart, b.CanDiscover, tt.start, tt.discover)
			}
			if tt.discover && b.Next != discoverNext.next {
				t.Errorf("next = %q, want the discovery step", b.Next)
			}
		})
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
	if err := json.Unmarshal(res.Body.Bytes(), &next); err != nil || next.Location != "/r/"+repoName+"/data-sharing" {
		t.Errorf("answer = %s, want Data sharing as location", res.Body.String())
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
	path := "/api/repos/" + repoName + "/pulls/4"
	check := func(e *env, externalID string) {
		e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"] = []api.CheckRun{{
			ID: 9, Name: "archfit/state", HeadSHA: sha(4), Status: "completed", Conclusion: "neutral",
			Title: "Architecture violations found · advisory · no baseline", ExternalID: externalID,
		}}
	}
	findings := func(b pullJSON) int { return len(b.Cycles) + len(b.Other) + len(b.Advisories) + len(b.Accepted) }
	tests := []struct {
		name   string
		setup  func(e *env)
		status int
		check  func(t *testing.T, b pullJSON)
	}{
		{"no check on the head", func(*env) {}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Title != "feat: read options" || b.Unavailable != noResult || b.Result != "" || b.HeadSHA != sha(4)[:shortSHA] {
				t.Errorf("body = %+v", b)
			}
		}},
		{"the report of the check", func(e *env) {
			check(e, "v1:88:1:"+digest)
			e.gh.artifacts[88] = payload
		}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Unavailable != "" || b.Headline != "Architecture violations found · advisory · no baseline" || b.Result != "violations found" ||
				b.Tone != "danger" || b.Required || !b.NoBaseline || b.Compared || b.RunID != 88 {
				t.Errorf("header = %+v", b)
			}
			if b.ViolationCount == 0 || len(b.Other) == 0 || b.Other[0].RuleID != "no_direct_b_dependency" || b.Other[0].Origin != "" {
				t.Errorf("violations = %d %+v", b.ViolationCount, b.Other)
			}
			if b.URL != "https://github.com/"+repoName+"/pull/4" {
				t.Errorf("url = %q", b.URL)
			}
		}},
		{"a report that does not match its check", func(e *env) {
			check(e, "v1:88:1:sha256:"+strings.Repeat("0", 64))
			e.gh.artifacts[88] = payload
		}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Unavailable != reportMismatch || findings(b) != 0 || b.Result != "violations found" { // the title says it, no report needed
				t.Errorf("body = %+v", b)
			}
		}},
		{"an expired report", func(e *env) { check(e, "v1:88:1:"+digest) }, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Unavailable != reportExpired || findings(b) != 0 {
				t.Errorf("body = %+v", b)
			}
		}},
		{"a result without an upload", func(e *env) { check(e, "v1:0:0:") }, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Unavailable != noUpload {
				t.Errorf("body = %+v", b)
			}
		}},
		{"an unknown pull request", func(e *env) { delete(e.gh.pulls, repoName+"#4") }, http.StatusNotFound, nil},
		{"a withdrawn result keeps its old run but shows no finding", func(e *env) {
			check(e, "v1:88:1:"+digest)
			e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"][0].Conclusion = "action_required"
			e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"][0].Title = "Action required"
			e.gh.artifacts[88] = payload
		}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Unavailable != actionRequired || b.Headline != "Action required" || b.Result != "action required" ||
				findings(b) != 0 || b.TaskCount != 0 {
				t.Errorf("body = %+v", b)
			}
		}},
		{"a report that does not decode", func(e *env) {
			broken := []byte(`{"schema_version":"nope"}`)
			s := sha256.Sum256(broken)
			check(e, "v1:88:1:sha256:"+hex.EncodeToString(s[:]))
			e.gh.artifacts[88] = broken
		}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Unavailable != reportUnavailable || findings(b) != 0 {
				t.Errorf("body = %+v", b)
			}
		}},
		{"the newest check run wins", func(e *env) {
			check(e, "v1:88:1:"+digest)
			e.gh.artifacts[88] = payload
			key := repoName + "@" + sha(4) + "#archfit/state"
			older := api.CheckRun{ID: 3, Name: "archfit/state", Status: "completed", Conclusion: "action_required", Title: "Older", ExternalID: "v1:0:0:"}
			e.gh.checks[key] = append([]api.CheckRun{older}, e.gh.checks[key]...)
		}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if b.Headline == "Older" || len(b.Other) == 0 {
				t.Errorf("body = %+v", b)
			}
		}},
		{"added only with a comparison", func(e *env) {
			compared := bytes.Replace(payload, []byte(`"comparison":{"status":"not_requested"`), []byte(`"comparison":{"status":"not_requested","origin_status":"comparable"`), 1)
			// The top-level finding is the last object with this id; dimension references come first.
			id := []byte(`{"id":"ba3803eca947bf3c1b7efa8f37854d5e"`)
			at := bytes.LastIndex(compared, id) + len(id)
			compared = append(compared[:at:at], append([]byte(`,"origin":"introduced"`), compared[at:]...)...)
			s := sha256.Sum256(compared)
			check(e, "v1:88:1:sha256:"+hex.EncodeToString(s[:]))
			e.gh.artifacts[88] = compared
		}, http.StatusOK, func(t *testing.T, b pullJSON) {
			if !b.Compared || len(b.Other) == 0 || b.Other[0].Origin != "added" {
				t.Errorf("compared = %v, other = %+v", b.Compared, b.Other)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.setup(e)
			res := e.as("alice", permRead).get(path)
			wantStatus(t, res, tt.status)
			if tt.check == nil {
				return
			}
			var b pullJSON
			if err := json.Unmarshal(res.Body.Bytes(), &b); err != nil {
				t.Fatal(err)
			}
			tt.check(t, b)
		})
	}
}

func TestPullPageNumberMustBeAPull(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	for _, n := range []string{"0", "-4", "x", "4x", "5"} {
		if n != "5" { // a number that is no pull request is found only by the API's GitHub call
			wantStatus(t, c.get("/r/"+repoName+"/pull/"+n), http.StatusNotFound)
		}
		wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/"+n), http.StatusNotFound)
		if n != "5" {
			wantStatus(t, c.get("/r/"+repoName+"/pull/"+n+"/graph"), http.StatusNotFound)
		}
		wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/"+n+"/graph"), http.StatusNotFound)
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

	wantStatus(t, e.as("alice", permRead).get("/r/"+repoName+"/pull/4/graph"), http.StatusOK)
	var got graphJSON
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4/graph", &got)
	if want := graph.Build(rep, []byte(prPolicy)); got.View == nil || !reflect.DeepEqual(*got.View, want) {
		t.Fatalf("view = %+v\nwant %+v", got.View, want)
	}
	if got.View.Stale || got.StaleReason != "" {
		t.Error("the head's policy is the report's, yet the view is stale")
	}
	for _, call := range e.gh.called("ArtifactID") {
		if !strings.HasPrefix(call.detail, "88 ") {
			t.Errorf("ArtifactID(%s): the graph read another run than the check's", call.detail)
		}
	}
	if calls := e.gh.called("LatestRun"); len(calls) != 0 {
		t.Errorf("LatestRun called %d times: the default branch's report has no place here", len(calls))
	}
	h := got.Pull
	if h == nil || h.Number != 4 || h.Title != "feat: read options" || h.HeadSHA != sha(4)[:shortSHA] ||
		h.Headline != "Architecture violations found · advisory" || h.URL != "https://github.com/"+repoName+"/pull/4" ||
		!h.Compared || h.Unavailable != "" {
		t.Errorf("pull header = %+v", h)
	}
	if want := "https://github.com/" + repoName + "/blob/" + sha(4) + "/.archfit.yaml"; got.PolicyHref != want {
		t.Errorf("policy_href = %q, want %q", got.PolicyHref, want)
	}
	added := 0
	for _, l := range got.View.Links {
		if l.Origin == report.PairAdded {
			added++
		}
	}
	if got.Added != added || added == 0 {
		t.Errorf("added = %d, want the %d added links", got.Added, added)
	}
}

func TestPullPageReadsTheGateModeOfTheBase(t *testing.T) {
	e := newEnv(t)
	pull := e.gh.pulls[repoName+"#4"]
	pull.BaseRef = "release"
	e.gh.pulls[repoName+"#4"] = pull
	e.gh.setFile(repoName, "release", settings.Path, strings.Replace(settingsText, "gate: advisory", "gate: required", 1))
	var body pullJSON
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &body)
	if !body.Required {
		t.Error("the base requires the gate, the page says advisory")
	}
}

// The page states the engine's decision as the report gives it, the balancing move of each advisory's seam, and the
// findings whose agent task leaves the move to the owner.
func TestPullPageStatesTheEngineDecision(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(prArtifact(t, prPolicy), &doc); err != nil {
		t.Fatal(err)
	}
	read := func(t *testing.T, doc map[string]any) pullJSON {
		t.Helper()
		payload, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		e := newEnv(t)
		e.checkPull(payload)
		var body pullJSON
		e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &body)
		return body
	}

	t.Run("as the pilot's report gives it", func(t *testing.T) {
		b := read(t, doc)
		want := decisionJSON{HardGates: "fail", Blockers: 4, Unevaluated: []report.UnevaluatedRule{}, OriginReasons: []string{}}
		if b.Decision == nil || !reflect.DeepEqual(*b.Decision, want) {
			t.Errorf("decision = %+v, want %+v", b.Decision, want)
		}
		if len(b.Advisories) == 0 {
			t.Fatal("no advisory to read")
		}
		for _, f := range b.Advisories {
			if f.Move != report.BalancingMoves["follow_rule"] || f.OwnerDecision {
				t.Errorf("advisory %s -> %s: move %q, owner decision %v", f.From, f.To, f.Move, f.OwnerDecision)
			}
		}
	})

	t.Run("unevaluated rules, attribution notes and an owner decision", func(t *testing.T) {
		doc["decision"].(map[string]any)["unevaluated_required_rules"] = []any{map[string]any{"rule_id": "layers", "reason": "selector matches no module"}}
		doc["comparison"].(map[string]any)["origin_reasons"] = []any{"base report lacks finding identities"}
		advisory := doc["findings"].([]any)[4].(map[string]any)
		doc["agent_tasks"] = []any{map[string]any{"finding_id": advisory["id"], "rule_id": advisory["rule_id"],
			"repair_kind": "needs_owner_decision", "goal": "Decide", "constraints": []any{}, "files": []any{}, "validation": []any{}}}
		b := read(t, doc)
		if d := b.Decision; d == nil || len(d.Unevaluated) != 1 || d.Unevaluated[0] != (report.UnevaluatedRule{RuleID: "layers", Reason: "selector matches no module"}) ||
			!slices.Equal(d.OriginReasons, []string{"base report lacks finding identities"}) {
			t.Errorf("decision = %+v", b.Decision)
		}
		owners := 0
		for _, f := range b.Advisories {
			if f.OwnerDecision {
				owners++
			}
		}
		if owners != 1 {
			t.Errorf("owner decisions = %d, want the one finding its task names", owners)
		}
	})
}

// The pull request page and its graph read their parts side by side, yet each part fails the answer exactly as when
// they were read in turn.
func TestPullPartsFailAsWhenReadInTurn(t *testing.T) {
	baseSettings, download := "File release:"+settings.Path, "ArtifactByID"
	tests := []struct {
		name string
		path string
		fail map[string]error
		want int
	}{
		{"settings of the base", "/pulls/4", map[string]error{baseSettings: api.ErrRateLimited}, http.StatusServiceUnavailable},
		{"the report", "/pulls/4", map[string]error{download: api.ErrRateLimited}, http.StatusServiceUnavailable},
		{"both, the settings' first", "/pulls/4", map[string]error{baseSettings: api.ErrRateLimited, download: errors.New("boom")}, http.StatusServiceUnavailable},
		{"the policy of the head", "/pulls/4/graph", map[string]error{"File " + sha(4) + ":" + policy.PolicyPath: api.ErrRateLimited}, http.StatusServiceUnavailable},
		{"the policy of the base, once the head's is the report's", "/pulls/4/graph", map[string]error{"File release:" + policy.PolicyPath: api.ErrRateLimited}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			pull := e.gh.pulls[repoName+"#4"]
			pull.BaseRef = "release"
			e.gh.pulls[repoName+"#4"] = pull
			e.checkPull(prArtifact(t, prPolicy))
			e.gh.setFile(repoName, sha(4), policy.PolicyPath, prPolicy)
			maps.Copy(e.gh.fail, tt.fail)
			wantStatus(t, e.as("alice", permRead).get("/api/repos/"+repoName+tt.path), tt.want)
		})
	}
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
		want := pullRow{Number: 4, Title: "feat: read options", State: "violations found", Tone: "danger", Reason: "Architecture violations found · advisory", Href: "/r/" + repoName + "/pull/4"}
		if len(body.Pulls) != 1 || body.Pulls[0] != want {
			t.Errorf("pulls = %+v, want [%+v]", body.Pulls, want)
		}
		if !stepOf(body, "Advisory").Current || !stepOf(body, "Policy merged").Done || !strings.Contains(body.Next, "Make the gate required") || !strings.Contains(body.NextHow, "gate: required") {
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
	t.Run("every optional part failing at once leaves the page", func(t *testing.T) {
		e := newEnv(t)
		setup(e, onboarding.StageAdvisory)
		e.gh.fail["CheckRuns"] = errors.New("boom")
		e.gh.fail["OpenPulls"] = errors.New("boom")
		e.acct.err = errors.New("store down")
		e.consents.readErr = errors.New("store down")
		var body statusBody
		e.as("alice", permAdmin).getJSON(path, &body)
		if !body.Admin || body.ManagedAI != checkUnknown || body.MainState != stateUnknown || !body.PullsError || body.Banner != "" {
			t.Errorf("admin %v, managed AI %q, main %q, pulls error %v, banner %q", body.Admin, body.ManagedAI, body.MainState, body.PullsError, body.Banner)
		}
		if !stepOf(body, "Advisory").Current {
			t.Errorf("steps = %+v", body.Steps)
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
